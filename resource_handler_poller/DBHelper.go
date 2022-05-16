package main

import (
	"context"
	"database/sql"
	"fmt"
	"k8s_prot/podcom"
	"path/filepath"
	consts "rh_poller/protocols/constsandutils"
	"rh_poller/protocols/cytolog"
	"strings"
	"time"

	"go.uber.org/multierr"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/pkg/errors"
)

func getConnection(ctx context.Context, secretName, secretNamespace string) (*sql.DB, error) {
	username, pwd, err := getDBAuthenticationFromSecret(ctx, secretName, secretNamespace)
	if err != nil {
		panic(fmt.Sprintf("could not get username and password from %s.%s: %s", secretNamespace, secretName, err.Error()))
	}
	connstring := fmt.Sprintf(
		"server=%s;user id=%s;password=%s;port=%d;database=%s;",
		consts.PromDBServer,
		username,
		pwd,
		consts.PromDBPort,
		consts.PromDBName,
	)
	return sql.Open("sqlserver", connstring)
}

func getDBAuthenticationFromSecret(ctx context.Context, secret, namespace string) (string, string, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
		if err != nil {
			return "", "", errors.WithStack(err)
		}
	}
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", "", errors.WithStack(err)
	}
	secretClient := clientSet.CoreV1().Secrets(namespace)
	cytolog.Info(fmt.Sprintf("accessing the SA with this secret name: %s in namespace: %s", secret, namespace))
	secretObject, err := secretClient.Get(ctx, secret, v1.GetOptions{})
	if err != nil {
		return "", "", errors.WithStack(err)
	}
	DBUser, err := getSecretValue(secretObject, "poller-username")
	cytolog.Debug("DBUser: %s", DBUser)
	if err != nil {
		return "", "", err
	}
	DBPassword, err := getSecretValue(secretObject, "poller-pwd")
	cytolog.Debug("DBPsw: %s", DBPassword)
	if err != nil {
		return "", "", err
	}
	return DBUser, DBPassword, nil
}

func getSecretValue(s *apiv1.Secret, field string) (string, error) {
	value, ok := s.Data[field]
	if !ok {
		return "", errors.New(fmt.Sprintf("Secret (%s.%s) does not have the following field: %s", s.Namespace, s.Name, field))
	}
	return string(value), nil
}

// returns whether deletion was successful
func deleteMetadataFromDB(ctx context.Context, podUID, podType string, db *sql.DB) bool {
	err := db.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not deleted", err)
		return false
	}
	var errors error
	tsqlBase := "DELETE FROM dbo.%s WHERE PodUID = @PodUID"
	// delete from Experiment
	_, err = db.ExecContext(
		ctx,
		fmt.Sprintf(tsqlBase, "Experiment"),
		sql.Named("PodUID", podUID),
	)
	errors = multierr.Append(errors, err)
	// delete from Metrics
	_, err = db.ExecContext(
		ctx,
		fmt.Sprintf(tsqlBase, "Metrics"),
		sql.Named("PodUID", podUID),
	)
	errors = multierr.Append(errors, err)
	// delete from Metadata
	switch podcom.PipelinePodType(podcom.PipelinePodType_value[podType]) {
	case podcom.PipelinePodType_INPUT_CREATOR:
		_, err = db.ExecContext(
			ctx,
			fmt.Sprintf(tsqlBase, "Metadata_InputCreator"),
			sql.Named("PodUID", podUID),
		)
	case podcom.PipelinePodType_CORE:
		_, err = db.ExecContext(
			ctx,
			fmt.Sprintf(tsqlBase, "Metadata_Core"),
			sql.Named("PodUID", podUID),
		)
	case podcom.PipelinePodType_POST_PROCESSING_SINGLE:
		// on delete cascade is set on child tables (SinglePP_Run), so only Metadata_SinglePP is enough
		_, err = db.ExecContext(
			ctx,
			fmt.Sprintf(tsqlBase, "Metadata_SinglePP"),
			sql.Named("PodUID", podUID),
		)
	case podcom.PipelinePodType_POST_PROCESSING_MULTI:
		// on delete cascade is set on child tables (MultiPP_Condition and MultiPP_Complxes) so Metadata_MultiPP is enough
		_, err = db.ExecContext(
			ctx,
			fmt.Sprintf(tsqlBase, "Metadata_MultiPP"),
			sql.Named("PodUID", podUID),
		)
	default:
		cytolog.Error("unknown podtype: %s", podType)
		return false
	}
	errors = multierr.Append(errors, err)
	return multierr.Errors(errors) == nil
}

func uploadRuntimeToDB(ctx context.Context, runtime time.Duration, podUID string, db *sql.DB) bool {
	err := db.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return false
	}
	experimentExists, err := checkIfRowExists(ctx, podUID, "Metrics", db)
	if err != nil {
		return false
	}
	var tsql string
	if !experimentExists {
		cytolog.Debug("data for pod %s not yet in db, creating row", podUID)
		tsql = "INSERT INTO dbo.Metrics(PodUID,Runtime) " +
			"VALUES(@PodUID, @Runtime)"
	} else {
		tsql = "UPDATE dbo.Metrics SET Runtime = @Runtime WHERE PodUID = @PodUID"
	}
	cytolog.Debug("uploading runtime of %s", podUID)
	_, err = db.ExecContext(
		ctx,
		tsql,
		sql.Named("Runtime", runtime.Seconds()),
		sql.Named("PodUID", podUID),
	)
	if err != nil {
		cytolog.Warning("could not insert runtime into experiment %s: %s", podUID, err)
		return false
	}
	return true
}

func checkIfRowExists(ctx context.Context, podUID, table string, db *sql.DB) (bool, error) {
	tsql := fmt.Sprintf("SELECT 1 FROM dbo.%s WHERE PodUID = @PodUID", strings.ToTitle(strings.ToLower(table)))
	rows, err := db.QueryContext(
		ctx,
		tsql,
		sql.Named("PodUID", podUID),
	)
	if err != nil {
		cytolog.Error("could not query dbo.%s: %s", table, err)
		return false, err
	}
	if rows.Next() {
		cytolog.Debug("poduid %s already present in table %s", podUID, table)
		return true, nil
	}
	return false, nil
}

func uploadMetadataToDB(ctx context.Context, labels map[string]string, CPULimit, memLimit int64, podUID string, db *sql.DB) bool {
	err := db.PingContext(ctx)
	if err != nil {
		cytolog.Warning("error with db (%s), data was not sent", err)
		return false
	}
	experimentExists, err := checkIfRowExists(ctx, podUID, "Experiment", db)
	if err != nil {
		return false
	}
	if !experimentExists {
		// uploading labels and resource limits
		cytolog.Debug("uploading metadata of %s, %s", podUID, labels["ID"])
		tsql := "INSERT INTO dbo.Experiment(PodUID, Username, ExperimentName, Type, ContainerTag, IDName,CPULimit, MemoryLimit)" +
			"VALUES(@PodUid,@Username, @Experimentname, @Type, @ContainerTag, @IDName, @CPULimit, @MemLimit);"
		_, err = db.ExecContext(
			ctx,
			tsql,
			sql.Named("PodUid", podUID),
			sql.Named("Username", labels["metadata/username"]),
			sql.Named("Experimentname", labels["metadata/experimentName"]),
			sql.Named("Type", labels["metadata/cont-name"]),
			sql.Named("ContainerTag", labels["metadata/cont-tag"]),
			sql.Named("IDName", labels["ID"]),
			sql.Named("CPULimit", CPULimit),
			sql.Named("MemLimit", memLimit),
		)
		if err != nil {
			cytolog.Warning("could not upload labels and limits to Experiment table: %s", err)
			return false
		}
	}
	return true
}
