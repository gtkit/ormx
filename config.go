package ormx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// 连接与连接池默认值。
const (
	defaultDialTimeout         = 10 * time.Second
	defaultReadTimeout         = 30 * time.Second
	defaultWriteTimeout        = 30 * time.Second
	defaultIdentifierMaxLength = 64
	defaultMaxOpenConns        = 50
	defaultMaxIdleConns        = 10
	defaultConnMaxLifetime     = 30 * time.Minute
	defaultConnMaxIdleTime     = 10 * time.Minute
	defaultHealthCheckTimeout  = 5 * time.Second
	defaultStartupPingRetryMax = 5 * time.Second
)

// redactedMask 是日志脱敏时替换敏感值（密码、参数值、连接属性）的占位符。
const redactedMask = "******"

// ErrNilSQLDB 表示向 OpenWithDB 传入了 nil *sql.DB；可用 errors.Is 判定。
var ErrNilSQLDB = errors.New("ormx: nil *sql.DB")

// Config 汇总建立 MySQL 连接所需的全部配置：驱动连接参数（MySQL）、
// 连接池（Pool）、GORM 行为（GORM）、方言（Dialect）以及启动期 Ping 重试策略。
// Config 通过 With / Clone 返回隔离的深拷贝副本，不修改原值。注意普通赋值（cfg2 := cfg）
// 只是浅拷贝，仍与原值共享 Params map 与连接池等指针字段；需要独立副本时用 Clone 或 With。
// 字段全部导出以便从配置文件直接映射，但直接修改字段会绕过 Option 的防御逻辑，
// 合法性由调用方自行保证；优先使用 Option 构建配置。
// 注意：零值 Config 不携带任何默认值——建议以 DefaultConfig()（或 NewConfig）的返回值为基底再覆盖字段，
// 以继承推荐的连接池大小、超时与启动 Ping 等默认。Pool 各字段为指针，nil 表示不设置、保持 database/sql 默认。
type Config struct {
	Name                     string
	MySQL                    MySQLConfig
	Pool                     PoolConfig
	GORM                     GORMConfig
	Dialect                  MySQLDialectConfig
	HealthProbe              HealthProbeFunc
	TxRetryObserver          TxRetryObserver
	StartupPing              bool
	StartupPingMaxRetries    int
	StartupPingRetryBaseWait time.Duration
	StartupPingRetryMaxWait  time.Duration
}

// MySQLConfig 描述驱动层连接设置。
// Addr 与 Host/Port 同时设置时 Addr 优先。
// 建议通过 Option 辅助函数设置，以保证 Addr/Host/Port 的优先级语义一致。
type MySQLConfig struct {
	User                 string            `json:"user"     yaml:"user"`
	Password             string            `json:"-"        yaml:"-"`
	Net                  string            `json:"net"      yaml:"net"`
	Host                 string            `json:"host"     yaml:"host"`
	Port                 string            `json:"port"     yaml:"port"`
	Addr                 string            `json:"addr"     yaml:"addr"`
	Database             string            `json:"database" yaml:"database"`
	Params               map[string]string `json:"params"   yaml:"params"`
	ConnectionAttributes string            `json:"connection_attributes" yaml:"connection_attributes"`
	Collation            string            `json:"collation" yaml:"collation"`
	Loc                  *time.Location    `json:"-"        yaml:"-"`
	TLSConfig            string            `json:"tls_config" yaml:"tls_config"`
	Timeout              time.Duration     `json:"timeout"  yaml:"timeout"`
	ReadTimeout          time.Duration     `json:"read_timeout" yaml:"read_timeout"`
	WriteTimeout         time.Duration     `json:"write_timeout" yaml:"write_timeout"`
	ParseTime            bool              `json:"parse_time" yaml:"parse_time"`
}

// PoolConfig 描述 *sql.DB 连接池参数。
// 字段均为指针：nil 表示不设置、保持 database/sql 的原有行为；
// 非 nil（含显式 0）表示应用该值到连接池。
// 可直接赋值、经 JSON/YAML 映射，或用 DefaultConfig 与对应 Option 设置，效果一致。
type PoolConfig struct {
	MaxOpenConns    *int           `json:"max_open_conns"     yaml:"max_open_conns"`
	MaxIdleConns    *int           `json:"max_idle_conns"     yaml:"max_idle_conns"`
	ConnMaxLifetime *time.Duration `json:"conn_max_lifetime"  yaml:"conn_max_lifetime"`
	ConnMaxIdleTime *time.Duration `json:"conn_max_idle_time" yaml:"conn_max_idle_time"`
}

// GORMConfig 描述透传给 gorm.Config 的行为配置，
// 字段与 gorm.Config 中的同名字段一一对应。
type GORMConfig struct {
	Logger                                   gormlogger.Interface
	NowFunc                                  func() time.Time
	NamingStrategy                           schema.NamingStrategy
	DefaultTransactionTimeout                time.Duration
	DefaultContextTimeout                    time.Duration
	PrepareStmt                              bool
	PrepareStmtMaxSize                       int
	PrepareStmtTTL                           time.Duration
	SkipDefaultTransaction                   bool
	DisableForeignKeyConstraintWhenMigrating bool
	IgnoreRelationshipsWhenMigrating         bool
	DisableNestedTransaction                 bool
	AllowGlobalUpdate                        bool
	QueryFields                              bool
	CreateBatchSize                          int
	TranslateError                           bool
	PropagateUnscoped                        bool
	DryRun                                   bool
}

// MySQLDialectConfig 描述透传给 GORM MySQL 方言（gorm.io/driver/mysql）的配置，
// 字段与其 Config 中的同名字段一一对应。
type MySQLDialectConfig struct {
	DriverName                    string
	ServerVersion                 string
	DefaultStringSize             uint
	DefaultDatetimePrecision      *int
	SkipInitializeWithVersion     bool
	DisableWithReturning          bool
	DisableDatetimePrecision      bool
	DontSupportRenameIndex        bool
	DontSupportRenameColumn       bool
	DontSupportForShareClause     bool
	DontSupportNullAsDefaultValue bool
	DontSupportRenameColumnUnique bool
	DontSupportDropConstraint     bool
}

// String 返回密码已脱敏的可读表示，
// 防止经 fmt.Print / 日志输出意外泄露凭据。
func (c Config) String() string {
	dsn, err := c.RedactedDSN()
	if err != nil {
		return fmt.Sprintf("ormx.Config{name=%s, err=%v}", c.Name, err)
	}
	return fmt.Sprintf("ormx.Config{name=%s, dsn=%s}", c.Name, dsn)
}

// GoString 实现 fmt.GoStringer，使 %#v 输出同样脱敏密码。
func (c Config) GoString() string { return c.String() }

// String 返回敏感值脱敏后的 MySQLConfig 表示，使 fmt 的 %v/%+v/%s 打印
// 不泄露密码、连接参数值与连接属性；用于安全日志输出。
// 注意：脱敏仅覆盖 fmt/Stringer 路径，结构化日志器（如 slog.Any）会反射字段、绕过本方法，
// 因此请勿将原始 Config/MySQLConfig 直接传给结构化日志，改用 String() 或 RedactedDSN()。
func (c MySQLConfig) String() string { return c.redactedString(false) }

// GoString 实现 fmt.GoStringer，使 %#v 输出同样脱敏。
func (c MySQLConfig) GoString() string { return c.redactedString(true) }

// redacted 返回把密码、连接参数值与连接属性替换为占位符的副本，
// 是所有脱敏路径（String/GoString、RedactedDSN、Client.Config）的唯一敏感字段清单来源。
// 值接收者保证不改原值，Params map 先克隆再脱敏，避免污染调用方。
func (c MySQLConfig) redacted() MySQLConfig {
	if c.Password != "" {
		c.Password = redactedMask
	}
	if len(c.Params) > 0 {
		c.Params = maps.Clone(c.Params)
		for key := range c.Params {
			c.Params[key] = redactedMask
		}
	}
	if c.ConnectionAttributes != "" {
		c.ConnectionAttributes = redactedMask
	}
	return c
}

// redactedString 通过 plain 别名（无 String/GoString 方法）避免格式化递归。
func (c MySQLConfig) redactedString(goSyntax bool) string {
	type plain MySQLConfig
	rc := plain(c.redacted())
	if goSyntax {
		return fmt.Sprintf("%#v", rc)
	}
	return fmt.Sprintf("%+v", rc)
}

// DefaultConfig 返回带合理默认值的 Config：
// MySQL 默认通过 tcp 连接 127.0.0.1:3306，时区为 time.Local，启用 ParseTime，
// 并设置拨号/读/写超时；连接池四项参数均设为包内默认值；
// GORM 使用默认命名策略；StartupPing 默认开启，重试基础等待 1 秒、上限 5 秒、
// 默认不重试（StartupPingMaxRetries 为 0）。
func DefaultConfig() Config {
	return Config{
		MySQL: MySQLConfig{
			Net:          "tcp",
			Host:         "127.0.0.1",
			Port:         "3306",
			Loc:          time.Local,
			Timeout:      defaultDialTimeout,
			ReadTimeout:  defaultReadTimeout,
			WriteTimeout: defaultWriteTimeout,
			ParseTime:    true,
		},
		Pool: PoolConfig{
			MaxOpenConns:    new(defaultMaxOpenConns),
			MaxIdleConns:    new(defaultMaxIdleConns),
			ConnMaxLifetime: new(defaultConnMaxLifetime),
			ConnMaxIdleTime: new(defaultConnMaxIdleTime),
		},
		GORM: GORMConfig{
			NamingStrategy: defaultNamingStrategy(),
		},
		StartupPing:              true,
		StartupPingMaxRetries:    0,
		StartupPingRetryBaseWait: time.Second,
		StartupPingRetryMaxWait:  defaultStartupPingRetryMax,
	}
}

// NewConfig 在 DefaultConfig 的基础上依次应用 opts 并返回结果。
func NewConfig(opts ...Option) Config {
	return DefaultConfig().With(opts...)
}

// With 返回应用 opts 后的 Config 副本，原 Config 不受影响；nil Option 会被跳过。
func (c Config) With(opts ...Option) Config {
	clone := c.Clone()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&clone)
	}
	return clone
}

// Clone 返回 Config 的深拷贝：复制 MySQL.Params 映射，以及连接池与方言中的可选指针字段，
// 避免副本与原值共享同一底层 map 或指针。
func (c Config) Clone() Config {
	clone := c
	clone.MySQL.Params = maps.Clone(c.MySQL.Params)
	clone.Pool.MaxOpenConns = clonePtr(c.Pool.MaxOpenConns)
	clone.Pool.MaxIdleConns = clonePtr(c.Pool.MaxIdleConns)
	clone.Pool.ConnMaxLifetime = clonePtr(c.Pool.ConnMaxLifetime)
	clone.Pool.ConnMaxIdleTime = clonePtr(c.Pool.ConnMaxIdleTime)
	clone.Dialect.DefaultDatetimePrecision = clonePtr(c.Dialect.DefaultDatetimePrecision)
	return clone
}

// clonePtr 返回 p 所指值的副本指针；p 为 nil 时返回 nil。
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Open 按当前配置构建 MySQL 连接器并打开 *sql.DB，应用连接池配置后初始化 GORM，
// 返回拥有该 *sql.DB 所有权的 Client（Close 时会一并关闭）。
// 若 StartupPing 开启，会先按重试策略 Ping 数据库；任一步骤失败时关闭已打开的连接并返回错误。
func (c Config) Open(ctx context.Context) (*Client, error) {
	driverCfg, err := c.DriverConfig()
	if err != nil {
		return nil, err
	}

	connector, err := mysqldriver.NewConnector(driverCfg)
	if err != nil {
		return nil, fmt.Errorf("ormx: create mysql connector: %w", err)
	}

	sqlDB := sql.OpenDB(connector)
	client, err := c.openWithSQLDB(ctx, sqlDB, true, driverCfg)
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return client, nil
}

// MustOpen 与 Open 行为一致，但在失败时直接 panic，适用于初始化阶段必须成功的场景。
func (c Config) MustOpen(ctx context.Context) *Client {
	client, err := c.Open(ctx)
	if err != nil {
		panic(err)
	}
	return client
}

// OpenWithDB 包装既有的 *sql.DB：GORM 初始化前会把 Config.Pool 的连接池设置应用到 sqlDB。
// 无论成败，sqlDB 的所有权始终归调用方（Client.Close 不会关闭它）。
func (c Config) OpenWithDB(ctx context.Context, sqlDB *sql.DB) (*Client, error) {
	if sqlDB == nil {
		return nil, ErrNilSQLDB
	}
	return c.openWithSQLDB(ctx, sqlDB, false, nil)
}

// Open 以 NewConfig(opts...) 构建配置并调用 Config.Open，是最常用的入口函数。
func Open(ctx context.Context, opts ...Option) (*Client, error) {
	return NewConfig(opts...).Open(ctx)
}

// MustOpen 以 NewConfig(opts...) 构建配置并调用 Config.MustOpen，失败时 panic。
func MustOpen(ctx context.Context, opts ...Option) *Client {
	return NewConfig(opts...).MustOpen(ctx)
}

// OpenWithDB 包装既有的 *sql.DB：GORM 初始化前会把 opts 中的连接池设置应用到 sqlDB。
// 无论成败，sqlDB 的所有权始终归调用方（Client.Close 不会关闭它）。
func OpenWithDB(ctx context.Context, sqlDB *sql.DB, opts ...Option) (*Client, error) {
	return NewConfig(opts...).OpenWithDB(ctx, sqlDB)
}

// DriverConfig 根据 MySQL 连接配置生成 go-sql-driver/mysql 的 *mysqldriver.Config，
// 配置非法（如缺少必填项或参数校验失败）时返回错误。
func (c Config) DriverConfig() (*mysqldriver.Config, error) {
	return c.MySQL.driverConfig()
}

// RedactedDSN 返回敏感值脱敏后的 DSN 字符串，可安全用于日志输出；
// 底层 DriverConfig 构建失败时返回错误。为避免泄露，密码、全部连接参数（Params）值
// 与连接属性（ConnectionAttributes）在非空时统一替换为 "******"，仅保留参数名等结构信息。
func (c Config) RedactedDSN() (string, error) {
	driverCfg, err := c.MySQL.redacted().driverConfig()
	if err != nil {
		return "", err
	}
	return driverCfg.FormatDSN(), nil
}

func (c Config) openWithSQLDB(
	ctx context.Context,
	sqlDB *sql.DB,
	ownsSQLDB bool,
	driverCfg *mysqldriver.Config,
) (*Client, error) {
	clone := c.Clone()
	applyPoolConfig(sqlDB, clone.Pool)

	if clone.StartupPing {
		if err := pingWithRetry(normalizeContext(ctx), sqlDB, clone); err != nil {
			return nil, fmt.Errorf("ormx: startup ping: %w", err)
		}
	}

	gdb, err := gorm.Open(gormmysql.New(clone.dialectorConfig(sqlDB, driverCfg)), clone.gormConfig())
	if err != nil {
		return nil, fmt.Errorf("ormx: initialize gorm: %w", err)
	}

	return &Client{
		db:        gdb,
		sqlDB:     sqlDB,
		config:    clone,
		ownsSQLDB: ownsSQLDB,
	}, nil
}

func pingWithRetry(ctx context.Context, sqlDB *sql.DB, cfg Config) error {
	// 先 Ping、再按 attempt 判断是否重试；用 attempt >= maxRetries 退出，
	// 避免 maxRetries+1 在极大值时整数溢出导致循环零次、静默跳过 Ping。
	var lastErr error
	maxRetries := max(cfg.StartupPingMaxRetries, 0)
	for attempt := 0; ; attempt++ {
		lastErr = sqlDB.PingContext(ctx)
		if lastErr == nil {
			return nil
		}
		if attempt >= maxRetries {
			return lastErr
		}

		sleep := retryBackoff(attempt, cfg.StartupPingRetryBaseWait, cfg.StartupPingRetryMaxWait)
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func (c Config) gormConfig() *gorm.Config {
	naming := c.GORM.NamingStrategy
	if naming.IdentifierMaxLength == 0 {
		naming.IdentifierMaxLength = defaultNamingStrategy().IdentifierMaxLength
	}

	return &gorm.Config{
		SkipDefaultTransaction:                   c.GORM.SkipDefaultTransaction,
		DefaultTransactionTimeout:                c.GORM.DefaultTransactionTimeout,
		DefaultContextTimeout:                    c.GORM.DefaultContextTimeout,
		NamingStrategy:                           naming,
		Logger:                                   c.GORM.Logger,
		NowFunc:                                  c.GORM.NowFunc,
		DryRun:                                   c.GORM.DryRun,
		PrepareStmt:                              c.GORM.PrepareStmt,
		PrepareStmtMaxSize:                       c.GORM.PrepareStmtMaxSize,
		PrepareStmtTTL:                           c.GORM.PrepareStmtTTL,
		DisableAutomaticPing:                     true,
		DisableForeignKeyConstraintWhenMigrating: c.GORM.DisableForeignKeyConstraintWhenMigrating,
		IgnoreRelationshipsWhenMigrating:         c.GORM.IgnoreRelationshipsWhenMigrating,
		DisableNestedTransaction:                 c.GORM.DisableNestedTransaction,
		AllowGlobalUpdate:                        c.GORM.AllowGlobalUpdate,
		QueryFields:                              c.GORM.QueryFields,
		CreateBatchSize:                          c.GORM.CreateBatchSize,
		TranslateError:                           c.GORM.TranslateError,
		PropagateUnscoped:                        c.GORM.PropagateUnscoped,
	}
}

func (c Config) dialectorConfig(sqlDB *sql.DB, driverCfg *mysqldriver.Config) gormmysql.Config {
	cfg := gormmysql.Config{
		DriverName:                    c.Dialect.DriverName,
		ServerVersion:                 c.Dialect.ServerVersion,
		Conn:                          sqlDB,
		SkipInitializeWithVersion:     c.Dialect.SkipInitializeWithVersion,
		DefaultStringSize:             c.Dialect.DefaultStringSize,
		DefaultDatetimePrecision:      c.Dialect.DefaultDatetimePrecision,
		DisableWithReturning:          c.Dialect.DisableWithReturning,
		DisableDatetimePrecision:      c.Dialect.DisableDatetimePrecision,
		DontSupportRenameIndex:        c.Dialect.DontSupportRenameIndex,
		DontSupportRenameColumn:       c.Dialect.DontSupportRenameColumn,
		DontSupportForShareClause:     c.Dialect.DontSupportForShareClause,
		DontSupportNullAsDefaultValue: c.Dialect.DontSupportNullAsDefaultValue,
		DontSupportRenameColumnUnique: c.Dialect.DontSupportRenameColumnUnique,
		DontSupportDropConstraint:     c.Dialect.DontSupportDropConstraint,
	}

	if driverCfg != nil {
		cfg.DSNConfig = driverCfg.Clone()
	}

	return cfg
}

func applyPoolConfig(sqlDB *sql.DB, pool PoolConfig) {
	if pool.MaxOpenConns != nil {
		sqlDB.SetMaxOpenConns(*pool.MaxOpenConns)
	}
	if pool.MaxIdleConns != nil {
		sqlDB.SetMaxIdleConns(*pool.MaxIdleConns)
	}
	if pool.ConnMaxLifetime != nil {
		sqlDB.SetConnMaxLifetime(*pool.ConnMaxLifetime)
	}
	if pool.ConnMaxIdleTime != nil {
		sqlDB.SetConnMaxIdleTime(*pool.ConnMaxIdleTime)
	}
}

func defaultNamingStrategy() schema.NamingStrategy {
	return schema.NamingStrategy{
		IdentifierMaxLength: defaultIdentifierMaxLength,
	}
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
