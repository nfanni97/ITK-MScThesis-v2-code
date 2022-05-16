package main

import (
	"context"
	"database/sql"
	"event_hub_receiver/protocols/cytolog"
	"fmt"

	"github.com/Azure/azure-event-hubs-go/v3/eph"
	"github.com/Azure/azure-event-hubs-go/v3/persist"
)

type (
	SQLLeaserCheckpointer struct {
		partitionID  string
		DBHandle     *sql.DB
		eventHubName string
		processor    *eph.EventProcessorHost
		done         func()
	}

	// need to wrap eph.Lease as we have custom IsExpired method (all others will be deferred to Lease)
	SQLLease struct {
		Lease *eph.Lease
	}
)

const (
	checkpointTableName     = "EventHubCheckpoint"
	checkpointTableFullName = "[dbo].[" + checkpointTableName + "]"
)

func NewSQLLeaserCheckpointer(connectionString, eventHubName, partition string) *SQLLeaserCheckpointer {
	return &SQLLeaserCheckpointer{
		DBHandle:     DBConn,
		eventHubName: eventHubName,
		partitionID:  partition,
	}
}

func (lcp *SQLLeaserCheckpointer) SetEventHostProcessor(eph *eph.EventProcessorHost) {
	lcp.processor = eph
	_, cancel := context.WithCancel(context.Background())
	lcp.done = cancel
}

//check if table exists
func (lcp *SQLLeaserCheckpointer) StoreExists(ctx context.Context) (bool, error) {
	cytolog.Info("Checking if the table %s exists...\n", checkpointTableName)
	err := lcp.DBHandle.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return false, err
	}
	tsql := "SELECT * FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = @tableName"
	res, err := lcp.DBHandle.ExecContext(
		ctx,
		tsql,
		sql.Named("tableName", checkpointTableName),
	)
	if err != nil {
		return false, err
	}
	if rownum, err := res.RowsAffected(); err != nil || rownum != 1 {
		return false, err
	}
	cytolog.Debug("The table %s exists\n", checkpointTableName)
	return true, nil

}

//if table not exist, create it
func (lcp *SQLLeaserCheckpointer) EnsureStore(ctx context.Context) error {
	cytolog.Info("Ensuring that table %s exists\n", checkpointTableName)
	ok, err := lcp.StoreExists(ctx)
	if err != nil {
		return err
	}
	if !ok {
		cytolog.Debug("The table %s does not exist, creating now\n", checkpointTableName)
		err := lcp.DBHandle.PingContext(ctx)
		if err != nil {
			cytolog.Warning("error with db (%s), data was not sent", err)
			return err
		}
		tsql := fmt.Sprintf("CREATE TABLE %s ("+
			"Topic varchar(100) NOT NULL,"+
			"PartitionID varchar(100) NOT NULL,"+
			"SequenceNumber bigint NOT NULL,"+
			"Offset varchar(20) NOT NULL,"+
			"CONSTRAINT PK_%s PRIMARY KEY CLUSTERED (Topic, PartitionID)"+
			")", checkpointTableName, checkpointTableName)
		_, err = lcp.DBHandle.ExecContext(
			ctx,
			tsql,
		)
		return err
	}
	return nil
}

func (lcp *SQLLeaserCheckpointer) DeleteStore(ctx context.Context) error {

	err := lcp.DBHandle.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return err
	}
	tsql := fmt.Sprintf("DROP TABLE %s", checkpointTableFullName)
	_, err = lcp.DBHandle.ExecContext(
		ctx,
		tsql,
	)
	return err
}

// get leases on all partitions
func (lcp *SQLLeaserCheckpointer) GetLeases(ctx context.Context) ([]eph.LeaseMarker, error) {
	partitionIDs := lcp.processor.GetPartitionIDs()
	leases := make([]eph.LeaseMarker, len(partitionIDs))
	for i, partitionID := range partitionIDs {
		leases[i] = getLease(partitionID)
	}
	return leases, nil
}

// create lease if it doesn't exist
func (lcp *SQLLeaserCheckpointer) EnsureLease(ctx context.Context, partitionID string) (eph.LeaseMarker, error) {
	return getLease(partitionID), nil
}

func getLease(partitionID string) *SQLLease {
	return &SQLLease{
		Lease: &eph.Lease{
			PartitionID: partitionID,
		},
	}
}

// delete lease from DB
func (lcp *SQLLeaserCheckpointer) DeleteLease(ctx context.Context, partitionID string) error {
	return nil
}

func (lcp *SQLLeaserCheckpointer) AcquireLease(ctx context.Context, partitionID string) (eph.LeaseMarker, bool, error) {
	//can only acquire lease if this is the replica with the needed partitionID
	if partitionID == lcp.partitionID {
		cytolog.Info("EPH#%s acquiring lease for partition %s\n", lcp.partitionID, partitionID)
		lease := getLease(partitionID)
		lease.Lease.Owner = lcp.processor.GetName()
		lease.IncrementEpoch()
		return lease, true, nil
	} else {
		return nil, false, fmt.Errorf("this is not the replica responsible for %s", partitionID)
	}
}

func (lcp *SQLLeaserCheckpointer) RenewLease(ctx context.Context, partitionID string) (eph.LeaseMarker, bool, error) {
	//cytolog.Info("EPH#%s renewing lease for partition %s\n", lcp.partitionID, partitionID)
	return getLease(partitionID), true, nil
}

func (lcp *SQLLeaserCheckpointer) ReleaseLease(ctx context.Context, partitionID string) (bool, error) {
	//cytolog.Info("EPH#%s releasing lease for partition %s\n", lcp.partitionID, partitionID)
	return true, nil
}

func (lcp *SQLLeaserCheckpointer) UpdateLease(ctx context.Context, partitionID string) (eph.LeaseMarker, bool, error) {
	//cytolog.Info("EPH#%s updating lease for partition %s\n", lcp.partitionID, partitionID)
	return lcp.RenewLease(ctx, partitionID)

}

func (lcp *SQLLeaserCheckpointer) GetCheckpoint(ctx context.Context, partitionID string) (persist.Checkpoint, bool) {
	//cytolog.Info("EPH#%s getting checkpoint for partition %s\n", lcp.partitionID, partitionID)
	if checkpoint, err := lcp.EnsureCheckpoint(ctx, partitionID); err == nil {
		return checkpoint, true
	} else {
		return checkpoint, false
	}
}

func (lcp *SQLLeaserCheckpointer) EnsureCheckpoint(ctx context.Context, partitionID string) (persist.Checkpoint, error) {
	if lcp.partitionID != partitionID {
		cytolog.Warning("Attempting to get checkpoint for partition %s while this EPH is for partition %s", partitionID, lcp.partitionID)
	}
	err := lcp.DBHandle.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return persist.NewCheckpointFromStartOfStream(), err
	}
	tsql := fmt.Sprintf("SELECT SequenceNumber, Offset FROM %s WHERE PartitionID = @PartitionID AND Topic = @Topic", checkpointTableFullName)
	res, err := lcp.DBHandle.QueryContext(
		ctx,
		tsql,
		sql.Named("PartitionID", partitionID),
		sql.Named("Topic", lcp.eventHubName),
	)
	if err != nil {
		cytolog.Error("EnsureCheckpoint: error with QueryContext: %s\n", err)
		return persist.NewCheckpointFromStartOfStream(), err
	}
	defer res.Close()
	ok := res.Next()
	if !ok && res.Err() == nil {
		cytolog.Info("There was no checkpoint in the database, returning a new one and persisting it\n")
		tsql = fmt.Sprintf("INSERT INTO %s VALUES (@Topic, @PartitionID, @SequenceNumber, @Offset)", checkpointTableFullName)
		checkpoint := persist.NewCheckpointFromStartOfStream()
		_, err := lcp.DBHandle.ExecContext(
			ctx,
			tsql,
			sql.Named("Topic", lcp.eventHubName),
			sql.Named("PartitionID", partitionID),
			sql.Named("SequenceNumber", checkpoint.SequenceNumber),
			sql.Named("Offset", checkpoint.Offset),
		)
		if err != nil {
			cytolog.Error("Error with inserting new checkpoint into table: %s\n", err)
			return checkpoint, err
		}
		return checkpoint, nil
	}
	var sequenceNumber int64
	var offset string
	err = res.Scan(&sequenceNumber, &offset)
	if err != nil {
		cytolog.Error("EnsureCheckpoint: error with result Scan: %s\n", err)
		return persist.NewCheckpointFromStartOfStream(), err
	}
	return persist.Checkpoint{
		Offset:         offset,
		SequenceNumber: sequenceNumber,
	}, nil
}

func (lcp *SQLLeaserCheckpointer) UpdateCheckpoint(ctx context.Context, partitionID string, checkpoint persist.Checkpoint) error {
	//cytolog.Info("EPH#%s updating checkpoint for partition %s\n", lcp.partitionID, partitionID)
	err := lcp.DBHandle.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return err
	}
	tsql := fmt.Sprintf("UPDATE %s SET SequenceNumber = @SequenceNumber, Offset = @Offset WHERE PartitionID = @PartitionID AND Topic = @Topic", checkpointTableFullName)
	_, err = lcp.DBHandle.ExecContext(
		ctx,
		tsql,
		sql.Named("SequenceNumber", checkpoint.SequenceNumber),
		sql.Named("Offset", checkpoint.Offset),
		sql.Named("PartitionID", partitionID),
		sql.Named("Topic", lcp.eventHubName),
	)
	return err
}

func (lcp *SQLLeaserCheckpointer) DeleteCheckpoint(ctx context.Context, partitionID string) error {
	cytolog.Info("EPH#%s deleting checkpoint for partition %s\n", lcp.partitionID, partitionID)
	err := lcp.DBHandle.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return err
	}
	tsql := fmt.Sprintf("DELETE FROM %s WHERE PartitionID = @PartitionID AND Topic = @Topic", checkpointTableFullName)
	_, err = lcp.DBHandle.ExecContext(
		ctx,
		tsql,
		sql.Named("PartitionID", partitionID),
		sql.Named("Topic", lcp.eventHubName),
	)
	return err
}

func (lcp *SQLLeaserCheckpointer) Close() error {
	if lcp.done != nil {
		lcp.done()
	}
	return nil
}

func (lease *SQLLease) IsExpired(ctx context.Context) bool {
	return false
}

func (lease *SQLLease) GetEpoch() int64 {
	return lease.Lease.Epoch
}

func (lease *SQLLease) GetOwner() string {
	return lease.Lease.Owner
}

func (lease *SQLLease) GetPartitionID() string {
	return lease.Lease.PartitionID
}

func (lease *SQLLease) IncrementEpoch() int64 {
	return lease.Lease.IncrementEpoch()
}

func (lease *SQLLease) String() string {
	return lease.Lease.String()
}
