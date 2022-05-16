package main

import (
	"context"
	"flag"
	"time"
)

var (
	queryFrequency  = time.Duration(*flag.Int("queryFreq", 5, "(seconds) how frequently new pods should be added")) * time.Second
	uploadFrequency = time.Duration(*flag.Int("uploadFreq", 20, "(seconds) how frequently metadata and runtime should be tried to upload (?)")) * time.Second
	deleteFrequency = time.Duration(*flag.Int("deleteFreq", 120, "how frequently finished pods should be deleted (should be larger than how freqently k8sh deletes pods to avoid getting picked up again)")) * time.Second
	labelKey        = flag.String("labelKey", "monitoring", "pods with this label should be watched")
	labelValue      = flag.String("labelValue", "true", "pods with label labelKey and this value should be watched")
	secretName      = flag.String("secretName", "prom-db-creds", "name of the secret that contains the DB credentials")
	secretNamespace = flag.String("secretNamespace", "monitoring", "namespace of the secret that contains the DB credentials")
)

func main() {
	ctx := context.Background()
	flag.Parse()
	poller := MakePoller(ctx, queryFrequency, uploadFrequency, deleteFrequency, *labelKey, *labelValue, *secretName, *secretNamespace)
	poller.start(ctx)
}
