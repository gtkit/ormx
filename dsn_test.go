package ormx

import (
	"errors"
	"fmt"
	"strings"
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

	cfg, err := c.driverConfig(nil)
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
	cfg, err := MySQLConfig{Addr: "db:3306", SystemVariables: src}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}

	src["time_zone"] = "'+08:00'"
	if cfg.Params["time_zone"] != "'+00:00'" {
		t.Fatalf("expected params to be cloned, got %v", cfg.Params)
	}
}

func TestMySQLConfigUnixRequiresAddr(t *testing.T) {
	// unix 网络未设 Addr 应报错，而不是把默认 Host/Port 拼成 unix(127.0.0.1:3306)。
	if _, err := (MySQLConfig{Net: "unix", Host: "127.0.0.1", Port: "3306"}).driverConfig(nil); !errors.Is(err, ErrAddressRequired) {
		t.Fatalf("expected ErrAddressRequired for unix without Addr, got %v", err)
	}
	// unix + socket 路径应正常。
	cfg, err := MySQLConfig{Net: "unix", Addr: "/tmp/mysql.sock"}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Net != "unix" || cfg.Addr != "/tmp/mysql.sock" {
		t.Fatalf("unexpected net/addr: %q/%q", cfg.Net, cfg.Addr)
	}
}

func TestMySQLConfigDriverConfigNilLocKeepsDriverDefault(t *testing.T) {
	// 直接构造、未设 Loc（nil）时，不应覆盖驱动默认时区。
	cfg, err := MySQLConfig{Addr: "db:3306"}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Loc == nil {
		t.Fatal("expected driver default Loc to be preserved, got nil")
	}
}

func TestMySQLConfigDriverConfigKeepsCustomNet(t *testing.T) {
	cfg, err := MySQLConfig{Net: "unix", Addr: "/tmp/mysql.sock"}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if cfg.Net != "unix" {
		t.Fatalf("expected net unix, got %q", cfg.Net)
	}
}

func TestMySQLConfigDriverConfigAddressError(t *testing.T) {
	if _, err := (MySQLConfig{}).driverConfig(nil); !errors.Is(err, ErrAddressRequired) {
		t.Fatalf("expected ErrAddressRequired, got %v", err)
	}
}

func TestMySQLConfigDriverConfigCharset(t *testing.T) {
	cfg, err := MySQLConfig{Addr: "db:3306", Charset: "utf8mb4"}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if dsn := cfg.FormatDSN(); !strings.Contains(dsn, "charset=utf8mb4") {
		t.Fatalf("expected dsn to contain charset=utf8mb4, got %q", dsn)
	}

	// 与 collation 协同：两者都进 DSN，连接期发 SET NAMES <charset> COLLATE <collation>。
	cfg, err = MySQLConfig{Addr: "db:3306", Charset: "utf8mb4", Collation: "utf8mb4_general_ci"}.driverConfig(nil)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	dsn := cfg.FormatDSN()
	if !strings.Contains(dsn, "charset=utf8mb4") || !strings.Contains(dsn, "collation=utf8mb4_general_ci") {
		t.Fatalf("expected dsn to contain charset and collation, got %q", dsn)
	}
	if cfg.Collation != "utf8mb4_general_ci" {
		t.Fatalf("unexpected collation %q", cfg.Collation)
	}
}

func TestMySQLConfigDriverConfigCharsetFallbackListRejected(t *testing.T) {
	_, err := MySQLConfig{Addr: "db:3306", Charset: "utf8mb4,utf8"}.driverConfig(nil)
	if !errors.Is(err, ErrDSNUnsupported) {
		t.Fatalf("expected ErrDSNUnsupported for charset fallback list, got %v", err)
	}
}

func TestWithDSNMapsFields(t *testing.T) {
	cfg := NewConfig(WithDSN(
		"alice:secret@tcp(db.internal:3307)/app" +
			"?charset=utf8mb4&loc=Local&parseTime=true&readTimeout=5s&time_zone=%27%2B00%3A00%27",
	))

	m := cfg.MySQL
	if m.User != "alice" || m.Password != "secret" {
		t.Fatalf("unexpected credentials: %q/%q", m.User, m.Password)
	}
	if m.Net != "tcp" || m.Addr != "db.internal:3307" {
		t.Fatalf("unexpected net/addr: %q/%q", m.Net, m.Addr)
	}
	// TCP 地址同步拆出 Host/Port，支撑 WithHost/WithPort 的单字段覆盖。
	if m.Host != "db.internal" || m.Port != "3307" {
		t.Fatalf("unexpected host/port: %q/%q", m.Host, m.Port)
	}
	if m.Database != "app" {
		t.Fatalf("unexpected database %q", m.Database)
	}
	if m.Charset != "utf8mb4" {
		t.Fatalf("unexpected charset %q", m.Charset)
	}
	if !m.ParseTime || m.Loc != time.Local || m.ReadTimeout != 5*time.Second {
		t.Fatalf("unexpected parseTime/loc/readTimeout: %v/%v/%v", m.ParseTime, m.Loc, m.ReadTimeout)
	}
	if m.SystemVariables["time_zone"] != "'+00:00'" {
		t.Fatalf("unexpected system variables %v", m.SystemVariables)
	}

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	if !strings.HasPrefix(dsn, "alice:******@tcp(db.internal:3307)/app") {
		t.Fatalf("unexpected redacted dsn %q", dsn)
	}
}

func TestWithDSNReplacesPackageDefaults(t *testing.T) {
	// WithDSN 整体替换 MySQL 子配置：DSN 未写的参数按驱动默认，不叠加 DefaultConfig。
	cfg := NewConfig(WithDSN("user@tcp(db:3306)/app"))
	if cfg.MySQL.ParseTime {
		t.Fatal("expected ParseTime to follow dsn semantics (false)")
	}
	if cfg.MySQL.Timeout != 0 || cfg.MySQL.ReadTimeout != 0 || cfg.MySQL.WriteTimeout != 0 {
		t.Fatalf("expected no timeouts, got %v/%v/%v", cfg.MySQL.Timeout, cfg.MySQL.ReadTimeout, cfg.MySQL.WriteTimeout)
	}
	if cfg.MySQL.Loc != time.UTC {
		t.Fatalf("expected driver default loc UTC, got %v", cfg.MySQL.Loc)
	}
}

func TestWithDSNLaterOptionOverrides(t *testing.T) {
	cfg := NewConfig(WithDSN("alice:pw@tcp(db:3306)/app?parseTime=true"), WithDatabase("other"))
	if cfg.MySQL.Database != "other" {
		t.Fatalf("expected later option to override database, got %q", cfg.MySQL.Database)
	}
	if cfg.MySQL.User != "alice" || !cfg.MySQL.ParseTime {
		t.Fatalf("expected other dsn fields to survive, got %q/%v", cfg.MySQL.User, cfg.MySQL.ParseTime)
	}
}

func TestWithDSNPreservesUnmodeledParams(t *testing.T) {
	// 本库未建模的驱动参数（含 charset 回退列表）原样保留、透传给驱动，不丢失也不拒绝。
	cfg := NewConfig(WithDSN(
		"user@tcp(db:3306)/app?charset=utf8mb4,utf8&compress=true&maxAllowedPacket=16777216&multiStatements=true",
	))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	for _, want := range []string{"charset=utf8mb4,utf8", "compress=true", "maxAllowedPacket=16777216", "multiStatements=true"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("expected redacted dsn to preserve %q, got %q", want, dsn)
		}
	}
	if cfg.MySQL.Charset != "utf8mb4,utf8" {
		t.Fatalf("expected charset field to mirror dsn value, got %q", cfg.MySQL.Charset)
	}
}

func TestWithDSNHostPortOverride(t *testing.T) {
	// WithHost/WithPort 会清空 Addr，DSN 映射必须同步拆出 Host/Port 才能支撑单字段覆盖。
	cfg := NewConfig(WithDSN("user@tcp(old-db:3306)/app"), WithHost("new-db"))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN after WithHost: %v", err)
	}
	if !strings.Contains(dsn, "tcp(new-db:3306)") {
		t.Fatalf("expected host override to take effect, got %q", dsn)
	}

	cfg = NewConfig(WithDSN("user@tcp(db:3306)/app"), WithPort("4406"))
	dsn, err = cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN after WithPort: %v", err)
	}
	if !strings.Contains(dsn, "tcp(db:4406)") {
		t.Fatalf("expected port override to take effect, got %q", dsn)
	}
}

func TestWithDSNCharsetOverrideAndClear(t *testing.T) {
	// WithCharset 覆盖 DSN 中的 charset。
	cfg := NewConfig(WithDSN("user@tcp(db:3306)/app?charset=latin1"), WithCharset("utf8mb4"))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") || strings.Contains(dsn, "latin1") {
		t.Fatalf("expected charset override to utf8mb4, got %q", dsn)
	}

	// 驱动无公开 API 清除基底 charset：置空报可判定错误，而不是静默保留。
	cfg = NewConfig(WithDSN("user@tcp(db:3306)/app?charset=utf8mb4"), WithCharset(""))
	if _, err = cfg.RedactedDSN(); !errors.Is(err, ErrDSNUnsupported) {
		t.Fatalf("expected ErrDSNUnsupported when clearing dsn charset, got %v", err)
	}
}

func TestWithDSNTLSOverrideResetsDerivedState(t *testing.T) {
	// tls=preferred 解析时驱动派生 AllowFallbackToPlaintext=true；
	// 覆盖成严格 tls=true 后不得残留明文回退。
	cfg := NewConfig(WithDSN("user@tcp(old-db:3306)/app?tls=preferred"), WithTLSConfig("true"))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	if strings.Contains(dsn, "allowFallbackToPlaintext") {
		t.Fatalf("strict tls override must not keep plaintext fallback, got %q", dsn)
	}
	if !strings.Contains(dsn, "tls=true") {
		t.Fatalf("expected tls=true, got %q", dsn)
	}

	dc, err := cfg.MySQL.driverConfig(cfg.dsn)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if dc.AllowFallbackToPlaintext {
		t.Fatal("expected AllowFallbackToPlaintext to be reset on tls override")
	}
}

func TestWithDSNEndpointOverrideResetsDerivedTLS(t *testing.T) {
	// tls=true 解析时驱动按旧地址推导 TLS.ServerName；改 Host 后必须清空派生 TLS，
	// 让驱动按新地址重新推导，避免证书校验仍用旧主机名。
	cfg := NewConfig(WithDSN("user@tcp(old-db:3306)/app?tls=true"), WithHost("new-db"))
	dc, err := cfg.MySQL.driverConfig(cfg.dsn)
	if err != nil {
		t.Fatalf("driverConfig: %v", err)
	}
	if dc.Addr != "new-db:3306" {
		t.Fatalf("expected addr new-db:3306, got %q", dc.Addr)
	}
	if dc.TLS != nil {
		t.Fatalf("expected derived TLS to be reset on endpoint override, got ServerName=%q", dc.TLS.ServerName)
	}
	if dc.TLSConfig != "true" {
		t.Fatalf("expected tls config name preserved, got %q", dc.TLSConfig)
	}

	// 端到端：最终 DSN 交回驱动标准化后，证书 ServerName 必须是新主机名。
	reparsed, err := mysqldriver.ParseDSN(dc.FormatDSN())
	if err != nil {
		t.Fatalf("ParseDSN(final dsn): %v", err)
	}
	if reparsed.TLS == nil || reparsed.TLS.ServerName != "new-db" {
		t.Fatalf("expected TLS ServerName new-db after driver normalize, got %+v", reparsed.TLS)
	}
}

func TestIsTCPNetwork(t *testing.T) {
	tests := []struct {
		network string
		want    bool
	}{
		{"tcp", true},
		{"tcp4", true},
		{"tcp6", true},
		{"unix", false},
		{"", false},
		{"tcpx", false},       // 前缀相同的自定义网络名不得误判
		{"tcp-custom", false}, // 同上
	}

	for _, tt := range tests {
		t.Run("net="+tt.network, func(t *testing.T) {
			if got := isTCPNetwork(tt.network); got != tt.want {
				t.Fatalf("isTCPNetwork(%q) = %v, want %v", tt.network, got, tt.want)
			}
		})
	}
}

func TestWithDSNPreferredTLSKeptWithoutOverride(t *testing.T) {
	// 未覆盖时 tls=preferred 的明文回退语义原样保留。
	cfg := NewConfig(WithDSN("user@tcp(db:3306)/app?tls=preferred"))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	if !strings.Contains(dsn, "allowFallbackToPlaintext=true") || !strings.Contains(dsn, "tls=preferred") {
		t.Fatalf("expected preferred tls semantics preserved, got %q", dsn)
	}
}

func TestWithDSNTCP6HostPortOverride(t *testing.T) {
	// tcp4/tcp6 同为 host:port 形式，单字段覆盖必须与 tcp 一致可用。
	cfg := NewConfig(WithDSN("user@tcp6([::1]:3306)/app"), WithPort("4406"))
	dsn, err := cfg.RedactedDSN()
	if err != nil {
		t.Fatalf("RedactedDSN: %v", err)
	}
	if !strings.Contains(dsn, "tcp6([::1]:4406)") {
		t.Fatalf("expected port override on tcp6, got %q", dsn)
	}
}

func TestWithDSNStrictParamDoesNotPanic(t *testing.T) {
	// 驱动 v1.10.0 对已移除的 strict 参数会 panic，本库须转换为可判定错误。
	cfg := NewConfig(WithDSN("user@tcp(db:3306)/app?strict=true"))
	if _, err := cfg.RedactedDSN(); !errors.Is(err, ErrDSNUnsupported) {
		t.Fatalf("expected ErrDSNUnsupported for removed strict param, got %v", err)
	}
}

func TestCharsetIdentifierValidation(t *testing.T) {
	// charset/collation 作为原始 SQL 片段拼入 SET NAMES，非法标识符必须拒绝。
	if _, err := (MySQLConfig{Addr: "db:3306", Charset: "utf8mb4; DROP"}).driverConfig(nil); err == nil ||
		!strings.Contains(err.Error(), "invalid charset") {
		t.Fatalf("expected invalid charset error, got %v", err)
	}
	if _, err := (MySQLConfig{Addr: "db:3306", Charset: "utf8mb4", Collation: "bad collation"}).driverConfig(nil); err == nil ||
		!strings.Contains(err.Error(), "invalid collation") {
		t.Fatalf("expected invalid collation error, got %v", err)
	}
	if _, err := NewConfig(WithDSN("user@tcp(db:3306)/app?charset=utf8mb4;drop")).RedactedDSN(); err == nil ||
		!strings.Contains(err.Error(), "invalid charset") {
		t.Fatalf("expected invalid charset error from dsn, got %v", err)
	}
}

func TestMySQLConfigStringOmitsInternalState(t *testing.T) {
	// MySQLConfig 不携带内部状态字段，%+v/%#v 输出保持稳定、不泄漏实现细节。
	cfg := NewConfig(WithDSN("alice:secret@tcp(db:3306)/app?charset=utf8mb4"))
	for _, s := range []string{fmt.Sprintf("%+v", cfg.MySQL), fmt.Sprintf("%#v", cfg.MySQL)} {
		if strings.Contains(s, "dsn") || strings.Contains(s, "secret") {
			t.Fatalf("unexpected internal state or secret in output: %s", s)
		}
	}
}

func TestWithDSNParseErrorStickyAndCleared(t *testing.T) {
	// 解析失败的错误保留至构建期，即便后续 Option 覆盖了字段。
	cfg := NewConfig(WithDSN("no-slash-dsn"), WithAddress("db:3306"))
	if _, err := cfg.RedactedDSN(); err == nil || !strings.Contains(err.Error(), "parse dsn") {
		t.Fatalf("expected sticky parse dsn error, got %v", err)
	}

	// 之后成功的 WithDSN 整体替换并清除粘滞错误。
	cfg = NewConfig(WithDSN("no-slash-dsn"), WithDSN("user@tcp(db:3306)/app"))
	if _, err := cfg.RedactedDSN(); err != nil {
		t.Fatalf("expected sticky error cleared by later WithDSN, got %v", err)
	}
}
