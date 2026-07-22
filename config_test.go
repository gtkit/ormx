package ormx

import (
	"context"
	"errors"
	"fmt"
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

func TestPoolConfigDirectFieldAssignmentApplies(t *testing.T) {
	sqlDB, _ := newStubDB()
	defer sqlDB.Close()

	// 直接对导出字段赋值（不经 Option）应生效；nil 字段表示不设置、保持默认。
	cfg := NewConfig(WithStartupPing(false), WithSkipInitializeWithVersion(true))
	cfg.Pool.MaxOpenConns = new(7)
	cfg.Pool.ConnMaxIdleTime = nil

	client, err := cfg.OpenWithDB(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("OpenWithDB() error = %v", err)
	}
	defer client.Close()

	if got := client.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("expected direct-assigned MaxOpenConns 7 to apply, got %d", got)
	}
}

func TestConfigCloneDeepCopiesPointerFields(t *testing.T) {
	base := NewConfig()
	base.Dialect.DefaultDatetimePrecision = new(3)

	clone := base.Clone()

	// 改动副本指针所指的值，不得回写到原配置。
	*clone.Pool.MaxOpenConns = 7
	*clone.Dialect.DefaultDatetimePrecision = 6

	if got := *base.Pool.MaxOpenConns; got != defaultMaxOpenConns {
		t.Fatalf("expected original MaxOpenConns to stay %d, got %d", defaultMaxOpenConns, got)
	}
	if got := *base.Dialect.DefaultDatetimePrecision; got != 3 {
		t.Fatalf("expected original DefaultDatetimePrecision to stay 3, got %d", got)
	}
}

func TestRedactedDSNMasksParamsAndAttributes(t *testing.T) {
	cfg := NewConfig(
		WithUser("u"),
		WithPassword("pw-secret"),
		WithDSNParam("session_secret", "param-secret"),
		WithConnectionAttributes("attribute-secret"),
	)

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN() error = %v", err)
	}
	for _, secret := range []string{"pw-secret", "param-secret", "attribute-secret"} {
		if strings.Contains(dsn, secret) {
			t.Fatalf("redacted dsn leaked %q: %s", secret, dsn)
		}
	}
	if !strings.Contains(dsn, "******") {
		t.Fatalf("expected masked values in redacted dsn, got %s", dsn)
	}
}

func TestMySQLConfigStringRedactsSecrets(t *testing.T) {
	cfg := NewConfig(
		WithUser("u"),
		WithPassword("pw-secret"),
		WithDSNParam("session_secret", "param-secret"),
		WithConnectionAttributes("attribute-secret"),
	)
	for _, printed := range []string{
		fmt.Sprintf("%v", cfg.MySQL),
		fmt.Sprintf("%+v", cfg.MySQL),
		fmt.Sprintf("%#v", cfg.MySQL),
	} {
		for _, secret := range []string{"pw-secret", "param-secret", "attribute-secret"} {
			if strings.Contains(printed, secret) {
				t.Fatalf("printing MySQLConfig leaked %q: %s", secret, printed)
			}
		}
	}
	// 脱敏在副本上进行，不得污染原始 Params。
	if cfg.MySQL.Params["session_secret"] != "param-secret" {
		t.Fatalf("printing mutated original Params: %v", cfg.MySQL.Params)
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
