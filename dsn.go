package ormx

// 本文件集中维护 MySQL 驱动连接配置（DSN）的构建。

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// MySQL 驱动的网络类型取值。
const (
	netTCP  = "tcp"
	netUnix = "unix"
)

// ErrAddressRequired 表示缺少连接地址：既未提供 Addr、又未同时提供 Host 与 Port，
// 或使用 unix 网络（Net=="unix"）时未用 WithAddress 指定 socket 路径。可用 errors.Is 判定。
var ErrAddressRequired = errors.New("ormx: mysql address is required")

// ErrSystemVariableNameRequired 表示系统变量名为空或纯空白字符；可用 errors.Is 判定。
var ErrSystemVariableNameRequired = errors.New("ormx: system variable name must not be empty")

// ErrDSNUnsupported 表示 DSN 级设置无法经当前 API 表达：WithCharset 传入 charset 回退列表
// （如 "utf8mb4,utf8"，回退列表仅能经 WithDSN 的 charset 参数设置）、试图用 WithCharset("")
// 清除来自 WithDSN 的 charset，或 DSN 使用驱动已移除的参数（如 strict）。可用 errors.Is 判定。
var ErrDSNUnsupported = errors.New("ormx: dsn settings not supported")

// dsnState 保存 WithDSN 的内部解析状态：完整驱动配置基底（含本库未建模、原样透传给驱动的
// 参数）、DSN 中 charset 参数原值（逗号连接，用于识别 WithCharset 覆盖），或解析失败的粘滞
// 错误。解析完成后不再修改，可在 Config 副本间只读共享。
type dsnState struct {
	base    *mysqldriver.Config
	charset string
	err     error
}

// address 解析最终连接地址：Addr 优先，否则 JoinHostPort(Host, Port)。
func (c MySQLConfig) address() (string, error) {
	if c.Addr != "" {
		return c.Addr, nil
	}
	// unix 网络必须用 WithAddress 指定 socket 路径；否则默认 Host/Port 会被拼成
	// 形如 unix(127.0.0.1:3306) 的非法地址。
	if c.Net == netUnix {
		return "", ErrAddressRequired
	}
	if c.Host == "" || c.Port == "" {
		return "", ErrAddressRequired
	}
	return net.JoinHostPort(c.Host, c.Port), nil
}

// driverConfig 构建 go-sql-driver 的连接配置。dsn 非 nil（WithDSN 场景）时以其保留的
// 完整驱动配置克隆为基底，本结构的建模字段无条件叠加其上——未建模参数原样透传，
// 建模部分始终以当前 MySQLConfig 为准。
func (c MySQLConfig) driverConfig(dsn *dsnState) (*mysqldriver.Config, error) {
	if dsn != nil && dsn.err != nil {
		return nil, dsn.err
	}

	network := c.Net
	if network == "" {
		network = netTCP
	}

	addr, err := c.address()
	if err != nil {
		return nil, err
	}

	cfg := mysqldriver.NewConfig()
	dsnCharset := ""
	if dsn != nil && dsn.base != nil {
		cfg = dsn.base.Clone()
		// TLS 配置名被后续 Option 覆盖时，重置基底中已解析的 TLS，让驱动按新名称重新解析。
		if cfg.TLSConfig != c.TLSConfig {
			cfg.TLS = nil
		}
		dsnCharset = dsn.charset
	}
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = network
	cfg.Addr = addr
	cfg.DBName = c.Database
	cfg.Params = maps.Clone(c.SystemVariables)
	for key := range cfg.Params {
		if strings.TrimSpace(key) == "" {
			return nil, ErrSystemVariableNameRequired
		}
	}
	cfg.ConnectionAttributes = c.ConnectionAttributes
	cfg.Collation = c.Collation
	// Charset 与 DSN 原值一致时基底已携带（含回退列表），无需也无法重复应用；
	// 相对原值被覆盖（或非 WithDSN 路径下非空）时经驱动公开 API 应用单一字符集。
	if c.Charset != dsnCharset {
		if applyErr := c.applyCharset(cfg, dsnCharset); applyErr != nil {
			return nil, applyErr
		}
	}
	// 仅在显式提供时覆盖，避免把驱动默认的 time.UTC 改成 nil 导致时间解析 panic。
	if c.Loc != nil {
		cfg.Loc = c.Loc
	}
	cfg.TLSConfig = c.TLSConfig
	cfg.Timeout = c.Timeout
	cfg.ReadTimeout = c.ReadTimeout
	cfg.WriteTimeout = c.WriteTimeout
	cfg.ParseTime = c.ParseTime
	return cfg, nil
}

// applyCharset 校验并把 c.Charset 应用到驱动配置。charset/collation 会被驱动拼入
// 原始 SQL（SET NAMES），库层限定标识符仅含字母、数字与下划线，降低误配置与注入风险。
func (c MySQLConfig) applyCharset(cfg *mysqldriver.Config, dsnCharset string) error {
	if c.Charset == "" {
		// 驱动无公开 API 清除基底中的 charset，明确报错而不是静默保留。
		return fmt.Errorf("ormx: cannot clear charset %q inherited from WithDSN: %w", dsnCharset, ErrDSNUnsupported)
	}
	// 驱动无公开 API 设置 charset 回退列表，只能经 mysqldriver.Charset 设单一字符集。
	if strings.Contains(c.Charset, ",") {
		return fmt.Errorf("ormx: charset fallback list %q, use a single charset: %w", c.Charset, ErrDSNUnsupported)
	}
	if !isMySQLIdent(c.Charset) {
		return fmt.Errorf("ormx: invalid charset %q: only letters, digits and underscore are allowed", c.Charset)
	}
	if c.Collation != "" && !isMySQLIdent(c.Collation) {
		return fmt.Errorf("ormx: invalid collation %q: only letters, digits and underscore are allowed", c.Collation)
	}
	if err := cfg.Apply(mysqldriver.Charset(c.Charset, c.Collation)); err != nil {
		return fmt.Errorf("ormx: apply charset: %w", err)
	}
	return nil
}

// parseDSN 调用驱动 ParseDSN，并把驱动对已移除参数（如 strict）的主动 panic 转换为错误，
// 维持本库"除 Must* 外不 panic"的契约。recover 范围仅覆盖这一次第三方解析调用。
func parseDSN(dsn string) (cfg *mysqldriver.Config, err error) {
	defer func() {
		if r := recover(); r != nil {
			cfg = nil
			err = fmt.Errorf("ormx: parse dsn: %v: %w", r, ErrDSNUnsupported)
		}
	}()
	cfg, err = mysqldriver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("ormx: parse dsn: %w", err)
	}
	return cfg, nil
}

// parseDSNConfig 解析完整 DSN：返回映射到本库建模字段的 MySQLConfig，以及保留完整
// 驱动配置的内部状态——本库未建模的参数（multiStatements、maxAllowedPacket、charset
// 回退列表等）经 dsnState.base 原样透传给驱动，不丢失。
func parseDSNConfig(dsn string) (MySQLConfig, *dsnState, error) {
	parsed, err := parseDSN(dsn)
	if err != nil {
		return MySQLConfig{}, nil, err
	}

	charsets := charsetsFromCanonicalDSN(parsed.FormatDSN())
	for _, cs := range charsets {
		if !isMySQLIdent(cs) {
			return MySQLConfig{}, nil,
				fmt.Errorf("ormx: invalid charset %q in dsn: only letters, digits and underscore are allowed", cs)
		}
	}
	charset := strings.Join(charsets, ",")

	m := MySQLConfig{
		User:                 parsed.User,
		Password:             parsed.Passwd,
		Net:                  parsed.Net,
		Addr:                 parsed.Addr,
		Database:             parsed.DBName,
		SystemVariables:      maps.Clone(parsed.Params),
		ConnectionAttributes: parsed.ConnectionAttributes,
		Charset:              charset,
		Collation:            parsed.Collation,
		Loc:                  parsed.Loc,
		TLSConfig:            parsed.TLSConfig,
		Timeout:              parsed.Timeout,
		ReadTimeout:          parsed.ReadTimeout,
		WriteTimeout:         parsed.WriteTimeout,
		ParseTime:            parsed.ParseTime,
	}
	// 同步拆出 Host/Port：WithHost/WithPort 覆盖时会清空 Addr 转而依赖 Host/Port，
	// 只映射 Addr 会让 WithDSN 之后的单字段覆盖直接报 ErrAddressRequired。
	if m.Net == netTCP {
		if host, port, splitErr := net.SplitHostPort(parsed.Addr); splitErr == nil {
			m.Host, m.Port = host, port
		}
	}
	return m, &dsnState{base: parsed, charset: charset}, nil
}

// isMySQLIdent 报告 s 是否为仅含字母、数字与下划线的非空标识符。
func isMySQLIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// charsetsFromCanonicalDSN 从 FormatDSN 规范形中提取 charset 参数值列表。
// 驱动的 charsets 字段未导出，规范形 DSN 是解析结果中该值的唯一可读来源。
func charsetsFromCanonicalDSN(canonical string) []string {
	// 规范形中库名经 PathEscape、参数值经 QueryEscape，最后一个 '/' 与其后第一个 '?'
	// 必然是库名与参数段的分隔符。
	rest := canonical[strings.LastIndexByte(canonical, '/')+1:]
	_, q, _ := strings.Cut(rest, "?")
	for kv := range strings.SplitSeq(q, "&") {
		if v, ok := strings.CutPrefix(kv, "charset="); ok {
			return strings.Split(v, ",")
		}
	}
	return nil
}
