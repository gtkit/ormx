package ormx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDriverConfigAndRedactedDSN(t *testing.T) {
	cfg := NewConfig(
		WithHost("db.internal"),
		WithPort("4406"),
		WithDatabase("app/main"),
		WithUser("alice"),
		WithPassword("secret"),
		WithTimeout(15*time.Second),
		WithReadTimeout(3*time.Second),
		WithWriteTimeout(4*time.Second),
		WithDSNParam("loc", "ignored-by-driver-config"),
	)

	driverCfg, err := cfg.DriverConfig()
	if err != nil {
		t.Fatalf("DriverConfig() error = %v", err)
	}

	if driverCfg.Addr != "db.internal:4406" {
		t.Fatalf("expected addr db.internal:4406, got %q", driverCfg.Addr)
	}
	if driverCfg.DBName != "app/main" {
		t.Fatalf("expected db name app/main, got %q", driverCfg.DBName)
	}
	if driverCfg.Timeout != 15*time.Second {
		t.Fatalf("expected timeout 15s, got %v", driverCfg.Timeout)
	}
	if driverCfg.ReadTimeout != 3*time.Second {
		t.Fatalf("expected read timeout 3s, got %v", driverCfg.ReadTimeout)
	}
	if driverCfg.WriteTimeout != 4*time.Second {
		t.Fatalf("expected write timeout 4s, got %v", driverCfg.WriteTimeout)
	}
	if _, ok := driverCfg.Params["charset"]; ok {
		t.Fatalf("expected charset param to be omitted, got %#v", driverCfg.Params)
	}

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN() error = %v", err)
	}
	if strings.Contains(dsn, "secret") {
		t.Fatalf("expected redacted dsn to hide password, got %q", dsn)
	}
	if !strings.Contains(dsn, "/app%2Fmain") {
		t.Fatalf("expected database name to be escaped, got %q", dsn)
	}
}

func TestConfigCloneIsIsolated(t *testing.T) {
	base := DefaultConfig()
	clone := base.With(WithDSNParam("readPreference", "secondary"))
	clone.MySQL.Params["readPreference"] = "primary"

	if _, ok := base.MySQL.Params["readPreference"]; ok {
		t.Fatalf("expected original params to stay isolated")
	}
	if got := clone.MySQL.Params["readPreference"]; got != "primary" {
		t.Fatalf("expected clone param update to stay local, got %q", got)
	}
}

func TestOpenWithDBUsesExternalPool(t *testing.T) {
	sqlDB, state := newStubDB()
	defer sqlDB.Close()

	cfg := NewConfig(
		WithMaxOpenConns(20),
		WithMaxIdleConns(8),
		WithConnMaxLifetime(time.Minute),
		WithConnMaxIdleTime(30*time.Second),
		WithSkipInitializeWithVersion(true),
	)

	client, err := cfg.OpenWithDB(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("OpenWithDB() error = %v", err)
	}

	if client.DB() == nil {
		t.Fatalf("expected gorm db to be initialized")
	}
	if client.SQLDB() != sqlDB {
		t.Fatalf("expected wrapped sql.DB to be preserved")
	}

	stats := client.Stats()
	if stats.MaxOpenConnections != 20 {
		t.Fatalf("expected max open connections 20, got %d", stats.MaxOpenConnections)
	}

	if got := state.pingCount.Load(); got != 1 {
		t.Fatalf("expected startup ping once, got %d", got)
	}

	if closeErr := client.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	if pingErr := sqlDB.PingContext(context.Background()); pingErr != nil {
		t.Fatalf("expected external sql.DB to remain open, got %v", pingErr)
	}
	if got := state.pingCount.Load(); got != 2 {
		t.Fatalf("expected ping count 2 after manual ping, got %d", got)
	}
}

func TestOpenWithoutStartupPingDoesNotDialImmediately(t *testing.T) {
	client, err := Open(
		context.Background(),
		WithHost("127.0.0.1"),
		WithPort("1"),
		WithDatabase("app"),
		WithUser("root"),
		WithPassword("secret"),
		WithStartupPing(false),
		WithSkipInitializeWithVersion(true),
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer client.Close()

	if client.DB() == nil || client.SQLDB() == nil {
		t.Fatalf("expected client to expose initialized db handles")
	}
}

func TestOpenRetriesStartupPing(t *testing.T) {
	sqlDB, state := newStubDB(withStubPingErrorOnce(context.DeadlineExceeded))
	defer sqlDB.Close()

	cfg := NewConfig(
		WithStartupPingRetry(1, time.Millisecond, 5*time.Millisecond),
		WithSkipInitializeWithVersion(true),
	)

	client, err := cfg.OpenWithDB(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("OpenWithDB() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
	if got := state.pingCount.Load(); got != 2 {
		t.Fatalf("expected 2 startup pings, got %d", got)
	}
}

func TestOpenClampsNegativeStartupPingRetries(t *testing.T) {
	sqlDB, state := newStubDB(withStubPingError(context.DeadlineExceeded))
	defer sqlDB.Close()

	cfg := DefaultConfig()
	cfg.StartupPingMaxRetries = -1
	cfg.Dialect.SkipInitializeWithVersion = true

	client, err := cfg.OpenWithDB(context.Background(), sqlDB)
	if err == nil {
		t.Fatalf("expected ping error, got client %#v", client)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if got := state.pingCount.Load(); got != 1 {
		t.Fatalf("expected one startup ping, got %d", got)
	}
}
