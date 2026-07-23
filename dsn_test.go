package ormx

import (
	"errors"
	"testing"
	"time"
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
		{name: "缺 host", cfg: MySQLConfig{Port: "3306"}, wantErr: ErrAddressRequired},
		{name: "缺 port", cfg: MySQLConfig{Host: "127.0.0.1"}, wantErr: ErrAddressRequired},
		{name: "全空", cfg: MySQLConfig{}, wantErr: ErrAddressRequired},
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
		SystemVariables:      map[string]string{"time_zone": "'+00:00'"},
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
	if cfg.Params["time_zone"] != "'+00:00'" {
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
	src := map[string]string{"time_zone": "'+00:00'"}
	cfg, err := MySQLConfig{Addr: "db:3306", SystemVariables: src}.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}

	src["time_zone"] = "'+08:00'"
	if cfg.Params["time_zone"] != "'+00:00'" {
		t.Fatalf("expected params to be cloned, got %v", cfg.Params)
	}
}

func TestMySQLConfigDriverConfigNilLocKeepsDriverDefault(t *testing.T) {
	// 直接构造、未设 Loc（nil）时，不应覆盖驱动默认时区。
	cfg, err := MySQLConfig{Addr: "db:3306"}.driverConfig()
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Loc == nil {
		t.Fatal("expected driver default Loc to be preserved, got nil")
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
	if _, err := (MySQLConfig{}).driverConfig(); !errors.Is(err, ErrAddressRequired) {
		t.Fatalf("expected ErrAddressRequired, got %v", err)
	}
}
