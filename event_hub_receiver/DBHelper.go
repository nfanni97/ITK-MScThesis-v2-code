package main

import (
	"context"
	"database/sql"
	consts "event_hub_receiver/protocols/constsandutils"
	"event_hub_receiver/protocols/cytolog"
	"fmt"
	"path/filepath"

	"github.com/pkg/errors"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var DBConn *sql.DB

func InitConnection(ctx context.Context, secretName, secretNamespace string) error {
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
	DBConn, err = sql.Open("sqlserver", connstring)
	return err
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
	DBUser, err := getSecretValue(secretObject, "receiver-username")
	cytolog.Debug("DBUser: %s", DBUser)
	if err != nil {
		return "", "", err
	}
	DBPassword, err := getSecretValue(secretObject, "receiver-pwd")
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

func insertMetric(ctx context.Context, tableName, columnName, podUID string, newValue float64) error {
	// not ideal as it affects all rows every time but can't bear to part with it yet
	/*tsql := "IF EXISTS(SELECT 1 FROM dbo.Metrics WHERE PodUID=@PodUID)" +
	"BEGIN" +
	"UPDATE dbo.Metrics SET MemoryUsage=" +
	"CASE WHEN PodUID=@PodUID AND (MemoryUsage IS NULL OR MemoryUsage < @NewMemory)" +
	"THEN @NewMemory" +
	"ELSE MemoryUsage END" +
	"END ELSE BEGIN" +
	"INSERT INTO dbo.Metrics(PodUID,MemoryUsage) VALUES(@PodUID,@NewMemory)" +
	"END"
	*/
	err := DBConn.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("could not ping db: %s", err.Error())
	}
	tsql := fmt.Sprintf("SELECT %s FROM dbo.%s WHERE PodUID = @PodUID", columnName, tableName)
	row := DBConn.QueryRowContext(
		ctx,
		tsql,
		sql.Named("PodUID", podUID),
	)
	var prev float64
	err = row.Scan(&prev)
	tsql = ""
	if err != sql.ErrNoRows {
		// row already exists, check if null or smaller than the new value
		if prev == 0 || prev < newValue {
			tsql = fmt.Sprintf("UPDATE dbo.%s SET %s=@NewValue WHERE PodUID=@PodUID", tableName, columnName)
			cytolog.Debug("old value was %v, updating to %v", prev, newValue)
		}
	} else {
		// row doesn't exist, add it
		tsql = fmt.Sprintf("INSERT INTO dbo.%s(PodUID, %s) VALUES(@PodUID,@NewValue)", tableName, columnName)
		cytolog.Debug("was not in db, inserting %v", newValue)
	}
	if len(tsql) > 0 {
		_, err = DBConn.ExecContext(
			ctx,
			tsql,
			sql.Named("PodUID", podUID),
			sql.Named("NewValue", newValue),
		)
	}
	return err
}
