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
// Config 通过 With / Clone 返回隔离副本（仅复制包内可变字段），不修改原值。注意普通赋值（cfg2 := cfg）
// 只是浅拷贝，仍与原值共享 SystemVariables map 与连接池等指针字段；需要独立副本时用 Clone 或 With。
// Config 是运行期配置，推荐用 Option（Open / NewConfig / With）构建；直接改字段会绕过 Option 的防御逻辑，
// 合法性由调用方自行保证。它不承诺可整体序列化——仅 MySQL、Pool 带 JSON/YAML 标签，
// 而 Logger、HealthProbe、TxRetryObserver、NowFunc、NamingStrategy 等运行时字段无法从配置文件映射；
// 配置文件驱动的场景建议由业务侧维护自己的 DTO，再转换成 ormx.Option。
// 注意：零值 Config 不携带任何默认值——需要默认值时以 DefaultConfig()（或 NewConfig）的返回值为基底再覆盖字段。
// Pool 各字段为指针，nil 表示不设置、保持 database/sql 默认。
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
	User                 string            `json:"user"                  yaml:"user"`
	Password             string            `json:"-"                     yaml:"-"`
	Net                  string            `json:"net"                   yaml:"net"`
	Host                 string            `json:"host"                  yaml:"host"`
	Port                 string            `json:"port"                  yaml:"port"`
	Addr                 string            `json:"addr"                  yaml:"addr"`
	Database             string            `json:"database"              yaml:"database"`
	SystemVariables      map[string]string `json:"system_variables"      yaml:"system_variables"`
	ConnectionAttributes string            `json:"connection_attributes" yaml:"connection_attributes"`
	Collation            string            `json:"collation"             yaml:"collation"`
	Loc                  *time.Location    `json:"-"                     yaml:"-"`
	TLSConfig            string            `json:"tls_config"            yaml:"tls_config"`
	Timeout              time.Duration     `json:"timeout"               yaml:"timeout"`
	ReadTimeout          time.Duration     `json:"read_timeout"          yaml:"read_timeout"`
	WriteTimeout         time.Duration     `json:"write_timeout"         yaml:"write_timeout"`
	ParseTime            bool              `json:"parse_time"            yaml:"parse_time"`
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
	ServerVersion             string
	DefaultStringSize         uint
	DefaultDatetimePrecision  *int
	SkipInitializeWithVersion bool
	DisableWithReturning      bool
	DisableDatetimePrecision  bool
}

// String 返回敏感值（密码、连接参数值、连接属性）已脱敏的可读表示，
// 底层复用 RedactedDSN，防止经 fmt.Print / 日志输出意外泄露。
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
// 值接收者保证不改原值，SystemVariables map 先克隆再脱敏，避免污染调用方。
func (c MySQLConfig) redacted() MySQLConfig {
	if c.Password != "" {
		c.Password = redactedMask
	}
	if len(c.SystemVariables) > 0 {
		c.SystemVariables = maps.Clone(c.SystemVariables)
		for key := range c.SystemVariables {
			c.SystemVariables[key] = redactedMask
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
			// 默认静默：库不隐式向 stdout 输出 SQL 或泄露绑定参数，
			// 需要日志时用 WithZlogger / WithGormLogger 显式开启。
			Logger:         gormlogger.Discard,
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

// Clone 隔离复制包内可变的配置字段：MySQL.SystemVariables 映射、连接池与方言中的可选指针字段，
// 使副本与原值互不影响。注意它不深拷贝调用方注入的引用型字段——
// GORM.Logger、HealthProbe、TxRetryObserver、NamingStrategy.NameReplacer 以及
// MySQL.Loc（*time.Location，按不可变共享）仍与原值共享同一实例。
func (c Config) Clone() Config {
	clone := c
	clone.MySQL.SystemVariables = maps.Clone(c.MySQL.SystemVariables)
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
	return new(*p)
}

// Open 按当前配置构建 MySQL 连接器并打开 *sql.DB，应用连接池配置后初始化 GORM，
// 返回拥有该 *sql.DB 所有权的 Client（Close 时会一并关闭）。
// 若 StartupPing 开启，会先按重试策略 Ping 数据库；任一步骤失败时关闭已打开的连接并返回错误。
// 注意：ctx 仅约束启动 Ping；GORM 初始化时的 SELECT VERSION() 探测由驱动以内部
// context.Background() 执行，受连接读超时（WithReadTimeout）约束而非 ctx。若需严格超时，
// 设置合理的 ReadTimeout，或用 WithSkipInitializeWithVersion + WithServerVersion 跳过该探测。
func (c Config) Open(ctx context.Context) (*Client, error) {
	driverCfg, err := c.MySQL.driverConfig()
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
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("ormx: close sql db: %w", closeErr))
		}
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

// OpenWithDB 包装既有的 *sql.DB：GORM 初始化前会把 Config.Pool 中已设置的连接池参数应用到 sqlDB。
// 打开成功后 Client.Close 不会关闭该外部 *sql.DB（所有权归调用方）。
// 但注意：若 GORM 初始化（方言初始化/版本探测 SELECT VERSION()）失败，GORM 会调用 sqlDB.Close() 清理，
// 因此打开失败后不应再复用传入的 *sql.DB——这是 GORM 的行为，本库无法在不引入包装层的前提下规避。
// 另注意：本方法不修改 Config.MySQL，而外部 *sql.DB 的真实连接地址由调用方决定，
// 因此返回 Client 的 Config().MySQL 未必反映其真实 DSN。
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

// OpenWithDB 包装既有的 *sql.DB：只把 opts 中显式传入的连接池参数应用到 sqlDB，
// 不强加包内默认（借用的外部连接池由调用方自行配置，未显式覆盖的项保持不变）。
// 成功打开后 Client.Close 不会关闭该外部 *sql.DB；但注意：若 GORM 初始化失败，
// GORM 的清理逻辑可能关闭传入的 *sql.DB，失败后请勿再复用它（见 Config.OpenWithDB）。
func OpenWithDB(ctx context.Context, sqlDB *sql.DB, opts ...Option) (*Client, error) {
	// 从无连接池默认的基底出发，仅由 opts 设置池指针，避免覆盖外部 DB 的既有池配置。
	base := DefaultConfig()
	base.Pool = PoolConfig{}
	return base.With(opts...).OpenWithDB(ctx, sqlDB)
}

// RedactedDSN 返回敏感值脱敏后的 DSN 字符串，可安全用于日志输出；
// 底层驱动配置构建失败时返回错误。为避免泄露，密码、全部连接系统变量（SystemVariables）值
// 与连接属性（ConnectionAttributes）在非空时统一替换为 "******"，仅保留变量名等结构信息。
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

	// nil Logger 会让 GORM 恢复自己的默认日志器（stdout、不隐藏参数），
	// 在此统一兜底为 Discard，保证"默认静默"契约不被绕过。
	logger := c.GORM.Logger
	if logger == nil {
		logger = gormlogger.Discard
	}

	return &gorm.Config{
		SkipDefaultTransaction:                   c.GORM.SkipDefaultTransaction,
		DefaultTransactionTimeout:                c.GORM.DefaultTransactionTimeout,
		DefaultContextTimeout:                    c.GORM.DefaultContextTimeout,
		NamingStrategy:                           naming,
		Logger:                                   logger,
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
		ServerVersion:             c.Dialect.ServerVersion,
		Conn:                      sqlDB,
		SkipInitializeWithVersion: c.Dialect.SkipInitializeWithVersion,
		DefaultStringSize:         c.Dialect.DefaultStringSize,
		DefaultDatetimePrecision:  c.Dialect.DefaultDatetimePrecision,
		DisableWithReturning:      c.Dialect.DisableWithReturning,
		DisableDatetimePrecision:  c.Dialect.DisableDatetimePrecision,
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
