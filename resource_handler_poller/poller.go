package main

import (
	"context"
	"database/sql"
	"fmt"
	"rh_poller/protocols/cytolog"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type PodData struct {
	podObj           *v1.Pod
	uploadedRuntime  bool
	uploadedMetadata bool
}

type Poller struct {
	queryTimer   *time.Ticker
	uploadTicker *time.Ticker
	deleteTicker *time.Ticker
	pods         sync.Map //map[poduid]*PodData
	selector     metav1.ListOptions
	podInterface corev1.PodInterface
	DBHandle     *sql.DB
}

func MakePoller(ctx context.Context, queryFrequency, uploadFrequency, deleteFrequency time.Duration, labelKey, labelValue, secretName, secretNamespace string) *Poller {
	req, _ := labels.NewRequirement(labelKey, selection.Equals, []string{labelValue})
	selector := labels.NewSelector().Add(*req)

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(fmt.Errorf("error creating in-cluster k8s config: %s", err))
	}
	clientSet := kubernetes.NewForConfigOrDie(config)
	db, err := getConnection(ctx, secretName, secretNamespace)
	if err != nil {
		panic(fmt.Errorf("could not connecto to db: %s", err))
	}
	poller := &Poller{
		queryTimer:   time.NewTicker(queryFrequency),
		uploadTicker: time.NewTicker(uploadFrequency),
		deleteTicker: time.NewTicker(deleteFrequency),
		selector: metav1.ListOptions{
			LabelSelector: selector.String(),
		},
		podInterface: clientSet.CoreV1().Pods(""),
		DBHandle:     db,
	}
	return poller
}

func getLimits(pod *v1.Pod) (int64, int64) {
	var cpu int64 = 0
	var mem int64 = 0
	for _, cont := range pod.Spec.Containers {
		cpu += cont.Resources.Limits.Cpu().ScaledValue(resource.Milli)
		mem += cont.Resources.Limits.Memory().ScaledValue(resource.Mega)
	}
	return cpu, mem
}

func (p *Poller) deletePods(ctx context.Context) {
	cytolog.Debug("deleting pods...")
	removed := make([]types.UID, 0)
	p.pods.Range(func(keyInt, valueInt interface{}) bool {
		select {
		case <-ctx.Done():
			return false
		default:
			value := valueInt.(*PodData)
			if value.uploadedMetadata && value.uploadedRuntime {
				key := keyInt.(types.UID)
				removed = append(removed, key)
			}
			return true
		}
	})
	cytolog.Debug("scheduled to be deleted: %v", removed)
	for _, uid := range removed {
		p.pods.LoadAndDelete(uid)
	}
}

func (p *Poller) uploadPods(ctx context.Context) {
	cytolog.Debug("uploading pod data...")
	p.pods.Range(func(key, valueI interface{}) bool {
		select {
		case <-ctx.Done():
			return false
		default:
			value := valueI.(*PodData)
			//cytolog.Debug("UP: podid: %s\tpodname: %s\tmetadata: %v\t,runtime: %v", key.(types.UID), value.podObj.Labels["ID"], value.uploadedMetadata, value.uploadedRuntime)
			// if pod has failed, delete all metadata of it from DB
			if value.podObj.Status.Phase == v1.PodFailed {
				go func(pod *v1.Pod, podid types.UID) {
					podType := pod.Labels["metadata/cont-name"]
					value.uploadedMetadata = deleteMetadataFromDB(
						ctx,
						string(podid),
						podType,
						p.DBHandle,
					)
					value.uploadedRuntime = value.uploadedMetadata
				}(value.podObj, value.podObj.UID)
				return true
			}
			if !value.uploadedMetadata {
				go func(pod *v1.Pod, value *PodData, podid types.UID, labels map[string]string) {
					CPULim, MemLim := getLimits(pod)
					value.uploadedMetadata = uploadMetadataToDB(
						ctx,
						labels,
						CPULim,
						MemLim,
						string(podid),
						p.DBHandle,
					)
				}(value.podObj, value, value.podObj.UID, value.podObj.Labels)
			}
			if needToUploadRuntime(value.podObj) && !value.uploadedRuntime {
				runtime := getRuntime(value.podObj)
				//cytolog.Debug("UP: trying to upload runtime of pod %s", value.podObj.Labels["ID"])
				go func(runtime *time.Duration, podid types.UID) {
					value.uploadedRuntime = uploadRuntimeToDB(
						ctx,
						*runtime,
						string(podid),
						p.DBHandle,
					)
				}(runtime, value.podObj.UID)
			}
			return true
		}
	})
}

func (p *Poller) addNewPods(ctx context.Context) {
	podList, err := p.podInterface.List(ctx, p.selector)
	if err != nil {
		cytolog.Warning("error with finding new pods: %s", err)
	}
	for _, pod := range podList.Items {
		//cytolog.Debug("ADD: working on pod %s,%s", pod.Labels["ID"], pod.UID)
		newPodData := &PodData{
			podObj:           pod.DeepCopy(),
			uploadedRuntime:  false,
			uploadedMetadata: false,
		}
		oldPodDataI, loaded := p.pods.LoadOrStore(pod.UID, newPodData)
		oldPodData := oldPodDataI.(*PodData)
		if loaded {
			//cytolog.Debug("ADD: loaded, old name: %s, new name: %s", oldPodData.podObj.Labels["ID"], newPodData.podObj.Labels["ID"])
			oldPodData.podObj = pod.DeepCopy()
			//cytolog.Debug("ADD: loaded and updated, old name: %s, new name: %s", oldPodData.podObj.Labels["ID"], newPodData.podObj.Labels["ID"])
		} else {
			cytolog.Debug("found new pod: %s, %s", pod.Labels["ID"], pod.UID)
			//cytolog.Debug("ADD: stored, old name: %s, new name: %s", oldPodData.podObj.Labels["ID"], newPodData.podObj.Labels["ID"])
		}
	}
}

func getRuntime(pod *v1.Pod) *time.Duration {
	startTime := pod.CreationTimestamp.Time
	endTime := pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time
	for _, contStatus := range pod.Status.ContainerStatuses {
		if endTime.Before(contStatus.State.Terminated.FinishedAt.Time) {
			endTime = contStatus.State.Terminated.FinishedAt.Time
		}
	}
	runtime := endTime.Sub(startTime)
	return &runtime
}

func needToUploadRuntime(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodSucceeded
}

func (p *Poller) start(ctx context.Context) {
	for {
		select {
		case <-p.queryTimer.C:
			go p.addNewPods(ctx)
		case <-p.deleteTicker.C:
			go p.deletePods(ctx)
		case <-p.uploadTicker.C:
			go p.uploadPods(ctx)
		case <-ctx.Done():
			cytolog.Info("Context closed, poller exits.")
			return
		}
	}
}
