package main

import (
	"context"
	"encoding/json"
	"event_hub_receiver/protocols/cytolog"
	"flag"
	"fmt"
	"os"

	eventhubs "github.com/Azure/azure-event-hubs-go/v3"
	"github.com/Azure/azure-event-hubs-go/v3/eph"
	_ "github.com/denisenkom/go-mssqldb"
)

const (
	eventhub = "prometheus-hub-test"
)

var (
	secretNamespace = flag.String("secretNamespace", "monitoring", "Namespace where the DBAuth secret is located")
	secretName      = flag.String("secretName", "prom-db-creds", "Name of the secret for the DBAuth")
)

func receiveEvent(ctx context.Context, event *eventhubs.Event) error {
	metric := Metric{}
	//cytolog.Info("got message: %v", event.Data)
	err := json.Unmarshal(event.Data, &metric)
	if err != nil {
		errorMsg := fmt.Sprintf("there was an error during unmarshaling of %v: %s", event.Data, err.Error())
		cytolog.Warning(errorMsg)
		return fmt.Errorf(errorMsg)
	}
	switch metric.name {
	case "own_memory_usage":
		err = metric.UploadMemory(ctx)
	case "own_cpu_usage":
		err = metric.UploadCPU(ctx)
	default:
		err = fmt.Errorf("this metric type is not supported: %s", metric.name)
	}
	if err != nil {
		cytolog.Error("error processing event %s: %s", string(event.Data), err)
	}
	return err
}

func main() {
	flag.Parse()
	ctx := context.Background()
	partitionID := os.Getenv("PARTITION_ID")
	cytolog.Info("This EPH will be responsible for partition %s\n", partitionID)
	hub_connstring := os.Getenv("EVENTHUB_CONN")
	err := InitConnection(ctx, *secretName, *secretNamespace)
	if err != nil {
		panic(fmt.Sprintf("could not connect to DB: %s", err.Error()))
	}
	leaserCheckpointer := NewSQLLeaserCheckpointer(hub_connstring, eventhub, partitionID)
	p, err := eph.NewFromConnectionString(
		ctx,
		hub_connstring,
		leaserCheckpointer,
		leaserCheckpointer,
	)
	if err != nil {
		panic(fmt.Sprintf("Failed to create EPH: %s\n", err))
	}
	defer p.Close(ctx)
	_, err = p.RegisterHandler(ctx, receiveEvent)
	if err != nil {
		panic(fmt.Sprintf("Failed to register handler: %s\n", err))
	}
	err = p.Start(ctx)
	if err != nil {
		panic(fmt.Sprintf("Failed to start EPH: %s\n", err))
	}
}
