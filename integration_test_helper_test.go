package ormx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	integrationRunEnv = "ORM_RUN_INTEGRATION"
	integrationDSNEnv = "ORM_TEST_DSN"
)

type integrationMySQLHarness struct {
	adminDB *sql.DB
	baseDSN *mysqldriver.Config
}

func newIntegrationMySQLHarness(t *testing.T) *integrationMySQLHarness {
	t.Helper()

	if os.Getenv(integrationRunEnv) != "1" {
		t.Skipf("set %s=1 to run integration tests", integrationRunEnv)
	}

	rawDSN := os.Getenv(integrationDSNEnv)
	if rawDSN == "" {
		t.Skipf("set %s to a MySQL DSN without a fixed schema, e.g. root:root@tcp(127.0.0.1:3306)/", integrationDSNEnv)
	}

	baseDSN, err := mysqldriver.ParseDSN(rawDSN)
	if err != nil {
		t.Fatalf("ParseDSN(%s): %v", integrationDSNEnv, err)
	}

	adminDSN := baseDSN.Clone()
	adminDSN.DBName = ""

	connector, err := mysqldriver.NewConnector(adminDSN)
	if err != nil {
		t.Fatalf("NewConnector(): %v", err)
	}

	adminDB := sql.OpenDB(connector)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pingErr := adminDB.PingContext(ctx)
	if pingErr != nil {
		_ = adminDB.Close()
		t.Fatalf("PingContext(): %v", pingErr)
	}

	t.Cleanup(func() {
		closeErr := adminDB.Close()
		if closeErr != nil {
			t.Fatalf("adminDB.Close(): %v", closeErr)
		}
	})

	return &integrationMySQLHarness{
		adminDB: adminDB,
		baseDSN: baseDSN,
	}
}

func (h *integrationMySQLHarness) openClient(t *testing.T, name string) *Client {
	t.Helper()

	cfg := h.newConfig(t, name)
	client, err := cfg.Open(context.Background())
	if err != nil {
		t.Fatalf("Open(%q): %v", name, err)
	}

	t.Cleanup(func() {
		closeErr := client.Close()
		if closeErr != nil {
			t.Fatalf("Close(%q): %v", name, closeErr)
		}
	})

	return client
}

func (h *integrationMySQLHarness) newConfig(t *testing.T, name string) Config {
	t.Helper()

	dbName := h.createDatabase(t, name)

	opts := []Option{
		WithName(name),
		WithNetwork(h.baseDSN.Net),
		WithAddress(h.baseDSN.Addr),
		WithDatabase(dbName),
		WithUser(h.baseDSN.User),
		WithPassword(h.baseDSN.Passwd),
		WithParseTime(h.baseDSN.ParseTime),
		WithDSNParams(h.baseDSN.Params),
	}

	if h.baseDSN.Loc != nil {
		opts = append(opts, WithLocation(h.baseDSN.Loc))
	}
	if h.baseDSN.Timeout > 0 {
		opts = append(opts, WithTimeout(h.baseDSN.Timeout))
	}
	if h.baseDSN.ReadTimeout > 0 {
		opts = append(opts, WithReadTimeout(h.baseDSN.ReadTimeout))
	}
	if h.baseDSN.WriteTimeout > 0 {
		opts = append(opts, WithWriteTimeout(h.baseDSN.WriteTimeout))
	}
	if h.baseDSN.TLSConfig != "" {
		opts = append(opts, WithTLSConfig(h.baseDSN.TLSConfig))
	}
	if h.baseDSN.Collation != "" {
		opts = append(opts, WithCollation(h.baseDSN.Collation))
	}
	if h.baseDSN.ConnectionAttributes != "" {
		opts = append(opts, WithConnectionAttributes(h.baseDSN.ConnectionAttributes))
	}

	return NewConfig(opts...)
}

func (h *integrationMySQLHarness) createDatabase(t *testing.T, name string) string {
	t.Helper()

	dbName := fmt.Sprintf("orm_%s_%d", sanitizeDatabaseName(name), time.Now().UnixNano())
	query := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName)
	if _, err := h.adminDB.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}

	t.Cleanup(func() {
		dropQuery := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName)
		if _, err := h.adminDB.ExecContext(context.Background(), dropQuery); err != nil {
			t.Fatalf("drop database %s: %v", dbName, err)
		}
	})

	return dbName
}

func sanitizeDatabaseName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "test"
	}
	return b.String()
}
