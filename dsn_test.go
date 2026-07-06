package ormx

import (
	"errors"
	"fmt"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestMySQLConfigAddress(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MySQLConfig
		want    string
		wantErr error
	}{
		{name: "addr 优先于 host/port", cfg: MySQLConfig{Addr: "db:3307", Host: "ignored", Port: "1"}, want: "db:3307"},
		{name: "host+port 拼接", cfg: MySQLConfig{Host: "127.0.0.1", Port: "3306"}, want: "127.0.0.1:3306"},
		{name: "ipv6 host 加方括号", cfg: MySQLConfig{Host: "::1", Port: "3306"}, want: "[::1]:3306"},
		{name: "缺 host", cfg: MySQLConfig{Port: "3306"}, wantErr: errAddressRequired},
		{name: "缺 port", cfg: MySQLConfig{Host: "127.0.0.1"}, wantErr: errAddressRequired},
		{name: "全空", cfg: MySQLConfig{}, wantErr: errAddressRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.address()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("address() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("address() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMySQLConfigDriverConfig(t *testing.T) {
	loc := time.UTC
	c := MySQLConfig{
		User:                 "alice",
		Password:             "secret",
		Host:                 "127.0.0.1",
		Port:                 "3306",
		Database:             "app",
		Params:               map[string]string{"charset": "utf8mb4"},
		ConnectionAttributes: "program_name:demo",
		Collation:            "utf8mb4_general_ci",
		Loc:                  loc,
		TLSConfig:            "custom",
		Timeout:              3 * time.Second,
		ReadTimeout:          5 * time.Second,
		WriteTimeout:         7 * time.Second,
		ParseTime:            true,
	}

	cfg, err := c.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.User != "alice" || cfg.Passwd != "secret" {
		t.Fatalf("unexpected credentials: %q/%q", cfg.User, cfg.Passwd)
	}
	if cfg.Net != "tcp" {
		t.Fatalf("expected default net tcp, got %q", cfg.Net)
	}
	if cfg.Addr != "127.0.0.1:3306" {
		t.Fatalf("unexpected addr %q", cfg.Addr)
	}
	if cfg.DBName != "app" {
		t.Fatalf("unexpected dbname %q", cfg.DBName)
	}
	if cfg.Params["charset"] != "utf8mb4" {
		t.Fatalf("unexpected params %v", cfg.Params)
	}
	if cfg.ConnectionAttributes != "program_name:demo" {
		t.Fatalf("unexpected connection attributes %q", cfg.ConnectionAttributes)
	}
	if cfg.Collation != "utf8mb4_general_ci" {
		t.Fatalf("unexpected collation %q", cfg.Collation)
	}
	if cfg.Loc != loc {
		t.Fatalf("unexpected loc %v", cfg.Loc)
	}
	if cfg.TLSConfig != "custom" {
		t.Fatalf("unexpected tls config %q", cfg.TLSConfig)
	}
	if cfg.Timeout != 3*time.Second || cfg.ReadTimeout != 5*time.Second || cfg.WriteTimeout != 7*time.Second {
		t.Fatalf("unexpected timeouts %v/%v/%v", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
	if !cfg.ParseTime {
		t.Fatal("expected ParseTime to be set")
	}
}

func TestMySQLConfigDriverConfigClonesParams(t *testing.T) {
	src := map[string]string{"charset": "utf8mb4"}
	cfg, err := MySQLConfig{Addr: "db:3306", Params: src}.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}

	src["charset"] = "latin1"
	if cfg.Params["charset"] != "utf8mb4" {
		t.Fatalf("expected params to be cloned, got %v", cfg.Params)
	}
}

func TestMySQLConfigDriverConfigKeepsCustomNet(t *testing.T) {
	cfg, err := MySQLConfig{Net: "unix", Addr: "/tmp/mysql.sock"}.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Net != "unix" {
		t.Fatalf("expected net unix, got %q", cfg.Net)
	}
}

func TestMySQLConfigDriverConfigAddressError(t *testing.T) {
	if _, err := (MySQLConfig{}).driverConfig(); !errors.Is(err, errAddressRequired) {
		t.Fatalf("expected errAddressRequired, got %v", err)
	}
}

func TestIsDeadlock(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "死锁 1213", err: &mysqldriver.MySQLError{Number: 1213}, want: true},
		{name: "锁等待超时 1205", err: &mysqldriver.MySQLError{Number: 1205}, want: true},
		{name: "wrapped 死锁", err: fmt.Errorf("tx: %w", &mysqldriver.MySQLError{Number: 1213}), want: true},
		{name: "其他 MySQL 错误", err: &mysqldriver.MySQLError{Number: 1062}, want: false},
		{name: "非 MySQL 错误", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDeadlock(tt.err); got != tt.want {
				t.Fatalf("isDeadlock(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetryBackoffWithinJitterRange(t *testing.T) {
	const (
		baseWait = 10 * time.Millisecond
		maxWait  = time.Second
	)
	for attempt := range 4 {
		floor := baseWait << attempt
		ceil := floor + floor/2 // 抖动最多 50%
		for range 50 {
			got := retryBackoff(attempt, baseWait, maxWait)
			if got < floor || got > ceil {
				t.Fatalf("retryBackoff(attempt=%d) = %v, want in [%v, %v]", attempt, got, floor, ceil)
			}
		}
	}
}

func TestRetryBackoffCappedByMaxWait(t *testing.T) {
	const maxWait = 20 * time.Millisecond
	if got := retryBackoff(3, 10*time.Millisecond, maxWait); got != maxWait {
		t.Fatalf("retryBackoff = %v, want capped at %v", got, maxWait)
	}
}

// attempt 极大时左移溢出为负，必须按已达上限处理而不是 panic。
func TestRetryBackoffOverflowReturnsMaxWait(t *testing.T) {
	const maxWait = 50 * time.Millisecond
	for _, attempt := range []int{41, 62, 63} {
		if got := retryBackoff(attempt, 5*time.Millisecond, maxWait); got != maxWait {
			t.Fatalf("retryBackoff(attempt=%d) = %v, want %v", attempt, got, maxWait)
		}
	}
}
