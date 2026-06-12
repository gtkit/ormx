package ormx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/gtkit/ormx/internal/dsn"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const (
	defaultDialTimeout         = dsn.DefaultDialTimeout
	defaultReadTimeout         = dsn.DefaultReadTimeout
	defaultWriteTimeout        = dsn.DefaultWriteTimeout
	defaultIdentifierMaxLength = 64
	defaultMaxOpenConns        = dsn.DefaultMaxOpenConns
	defaultMaxIdleConns        = dsn.DefaultMaxIdleConns
	defaultConnMaxLifetime     = dsn.DefaultConnMaxLifetime
	defaultConnMaxIdleTime     = dsn.DefaultConnMaxIdleTime
	defaultHealthCheckTimeout  = 5 * time.Second
	defaultStartupPingRetryMax = 5 * time.Second
)

var errNilSQLDB = errors.New("ormx: nil *sql.DB")

// Config 汇总建立 MySQL 连接所需的全部配置：驱动连接参数（MySQL）、
// 连接池（Pool）、GORM 行为（GORM）、方言（Dialect）以及启动期 Ping 重试策略。
// Config 为值语义，可安全复制；通过 With 应用 Option 会返回新副本，不修改原值。
// 字段全部导出以便从配置文件直接映射，但直接修改字段会绕过 Option 的防御逻辑，
// 合法性由调用方自行保证；优先使用 Option 构建配置。
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

// MySQLConfig describes driver-level connection settings.
// Addr takes precedence over Host and Port when both are set.
// Prefer the Option helpers so Addr/Host/Port precedence stays consistent.
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
// 每个字段仅在通过 DefaultConfig 或对应 Option 显式设置后才会应用到连接池，
// 未设置的字段保持 database/sql 的原有行为。
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	hasMaxOpenConns    bool
	hasMaxIdleConns    bool
	hasConnMaxLifetime bool
	hasConnMaxIdleTime bool
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

// String returns a human-readable representation with the password redacted.
// This prevents accidental credential leakage via fmt.Print / log output.
func (c Config) String() string {
	dsn, err := c.RedactedDSN()
	if err != nil {
		return fmt.Sprintf("ormx.Config{name=%s, err=%v}", c.Name, err)
	}
	return fmt.Sprintf("ormx.Config{name=%s, dsn=%s}", c.Name, dsn)
}

// GoString implements fmt.GoStringer so %#v also redacts the password.
func (c Config) GoString() string { return c.String() }

// DefaultConfig 返回带合理默认值的 Config：
// MySQL 默认通过 tcp 连接 127.0.0.1:3306，时区为 time.Local，启用 ParseTime，
// 并设置拨号/读/写超时；连接池四项参数均使用包内默认值并标记为已设置；
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
			MaxOpenConns:       defaultMaxOpenConns,
			MaxIdleConns:       defaultMaxIdleConns,
			ConnMaxLifetime:    defaultConnMaxLifetime,
			ConnMaxIdleTime:    defaultConnMaxIdleTime,
			hasMaxOpenConns:    true,
			hasMaxIdleConns:    true,
			hasConnMaxLifetime: true,
			hasConnMaxIdleTime: true,
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

// Clone 返回 Config 的深拷贝，其中 MySQL.Params 映射会被复制，
// 避免副本与原值共享同一底层 map。
func (c Config) Clone() Config {
	clone := c
	clone.MySQL.Params = cloneStringMap(c.MySQL.Params)
	return clone
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
		return nil, err
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

// OpenWithDB wraps an existing *sql.DB.
// Pool settings from Config.Pool are applied to sqlDB before GORM initialization.
// The caller retains ownership of sqlDB regardless of success or failure.
func (c Config) OpenWithDB(ctx context.Context, sqlDB *sql.DB) (*Client, error) {
	if sqlDB == nil {
		return nil, errNilSQLDB
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

// OpenWithDB wraps an existing *sql.DB.
// Pool settings from the supplied options are applied to sqlDB before GORM initialization.
// The caller retains ownership of sqlDB regardless of success or failure.
func OpenWithDB(ctx context.Context, sqlDB *sql.DB, opts ...Option) (*Client, error) {
	return NewConfig(opts...).OpenWithDB(ctx, sqlDB)
}

// DriverConfig 根据 MySQL 连接配置生成 go-sql-driver/mysql 的 *mysqldriver.Config，
// 配置非法（如缺少必填项或参数校验失败）时返回错误。
func (c Config) DriverConfig() (*mysqldriver.Config, error) {
	return c.MySQL.params().DriverConfig()
}

func (c MySQLConfig) params() dsn.Params {
	return dsn.Params{
		User:                 c.User,
		Password:             c.Password,
		Net:                  c.Net,
		Host:                 c.Host,
		Port:                 c.Port,
		Addr:                 c.Addr,
		Database:             c.Database,
		Params:               c.Params,
		ConnectionAttributes: c.ConnectionAttributes,
		Collation:            c.Collation,
		Loc:                  c.Loc,
		TLSConfig:            c.TLSConfig,
		Timeout:              c.Timeout,
		ReadTimeout:          c.ReadTimeout,
		WriteTimeout:         c.WriteTimeout,
		ParseTime:            c.ParseTime,
	}
}

// RedactedDSN 返回密码脱敏后的 DSN 字符串：密码非空时替换为 "******"，
// 可安全用于日志输出；底层 DriverConfig 构建失败时返回错误。
func (c Config) RedactedDSN() (string, error) {
	driverCfg, err := c.DriverConfig()
	if err != nil {
		return "", err
	}
	if driverCfg.Passwd != "" {
		driverCfg.Passwd = "******"
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
			return nil, err
		}
	}

	gdb, err := gorm.Open(gormmysql.New(clone.dialectorConfig(sqlDB, driverCfg)), clone.gormConfig())
	if err != nil {
		return nil, err
	}

	return &Client{
		db:        gdb,
		sqlDB:     sqlDB,
		config:    clone,
		ownsSQLDB: ownsSQLDB,
	}, nil
}

func pingWithRetry(ctx context.Context, sqlDB *sql.DB, cfg Config) error {
	var lastErr error
	maxRetries := max(cfg.StartupPingMaxRetries, 0)
	for attempt := range maxRetries + 1 {
		lastErr = sqlDB.PingContext(ctx)
		if lastErr == nil {
			return nil
		}
		if attempt >= maxRetries {
			return lastErr
		}

		sleep := dsn.RetryBackoff(attempt, cfg.StartupPingRetryBaseWait, cfg.StartupPingRetryMaxWait)
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		case <-timer.C:
		}
	}
	return lastErr
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
	if pool.hasMaxOpenConns {
		sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	}
	if pool.hasMaxIdleConns {
		sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	}
	if pool.hasConnMaxLifetime {
		sqlDB.SetConnMaxLifetime(pool.ConnMaxLifetime)
	}
	if pool.hasConnMaxIdleTime {
		sqlDB.SetConnMaxIdleTime(pool.ConnMaxIdleTime)
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

func cloneStringMap(src map[string]string) map[string]string {
	return maps.Clone(src)
}
