// Package jetorm 提供面向 go-jet 的 MySQL 连接管理、事务包装与执行辅助。
// 它不重新抽象 go-jet 的查询 DSL，只负责连接、连接池、事务与超时治理。
package jetorm

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"time"

	"github.com/gtkit/ormx/internal/dsn"
)

const defaultDriver = "mysql"

var (
	// ErrNilDB 表示 OpenWithDB 收到 nil 的 *sql.DB。
	ErrNilDB = errors.New("jetorm: db is required")
	// ErrNilTxFunc 表示 WithTx 收到 nil 的事务函数。
	ErrNilTxFunc = errors.New("jetorm: tx func is required")
	openDBFn     = openDB
)

// Config 描述 jetorm 客户端的全部可配置项。
// QueryTimeout 仅作用于单条语句执行；事务整体时长由 TxTimeout 控制。
type Config struct {
	Driver          string
	Host            string
	Port            string
	Database        string
	User            string
	Password        string `json:"-"`
	Params          map[string]string
	Loc             *time.Location
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	QueryTimeout    time.Duration
	// TxTimeout 限制 WithTx 中单次事务（含提交）的总时长，0 表示不限制。
	TxTimeout time.Duration
	// TxMaxRetries 为死锁（1213）/锁等待超时（1205）的自动重试次数，默认 0（不重试）。
	TxMaxRetries    int
	TxRetryBaseWait time.Duration
	TxRetryMaxWait  time.Duration
}

// Option 用于在 NewConfig / Open 中按 Functional Options 方式定制 Config。
type Option interface {
	apply(*Config)
}

type optionFunc func(*Config)

func (f optionFunc) apply(cfg *Config) {
	f(cfg)
}

// NewConfig 以内置默认值（mysql 驱动、127.0.0.1:3306、本地时区及默认连接池参数）
// 构建 Config，再依次应用 opts；nil Option 会被跳过。
func NewConfig(opts ...Option) Config {
	cfg := Config{
		Driver:          defaultDriver,
		Host:            "127.0.0.1",
		Port:            "3306",
		Loc:             time.Local,
		DialTimeout:     dsn.DefaultDialTimeout,
		ReadTimeout:     dsn.DefaultReadTimeout,
		WriteTimeout:    dsn.DefaultWriteTimeout,
		MaxOpenConns:    dsn.DefaultMaxOpenConns,
		MaxIdleConns:    dsn.DefaultMaxIdleConns,
		ConnMaxLifetime: dsn.DefaultConnMaxLifetime,
		ConnMaxIdleTime: dsn.DefaultConnMaxIdleTime,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.apply(&cfg)
	}
	return cfg
}

// Clone 返回 Config 的深拷贝（Params map 一并复制），修改副本不影响原值。
func (c Config) Clone() Config {
	clone := c
	clone.Params = maps.Clone(c.Params)
	return clone
}

// DSN 按当前配置生成 MySQL 连接串（含明文密码），配置非法时返回空字符串。
// 日志场景请改用 RedactedDSN。
func (c Config) DSN() string {
	cfg, err := c.params().DriverConfig()
	if err != nil {
		return ""
	}
	return cfg.FormatDSN()
}

// RedactedDSN 与 DSN 相同，但把非空密码脱敏为 ******，可安全用于日志输出。
func (c Config) RedactedDSN() string {
	cfg, err := c.params().DriverConfig()
	if err != nil {
		return ""
	}
	if cfg.Passwd != "" {
		cfg.Passwd = "******"
	}
	return cfg.FormatDSN()
}

func (c Config) params() dsn.Params {
	return dsn.Params{
		User:         c.User,
		Password:     c.Password,
		Host:         c.Host,
		Port:         c.Port,
		Database:     c.Database,
		Params:       c.Params,
		Loc:          c.Loc,
		Timeout:      c.DialTimeout,
		ReadTimeout:  c.ReadTimeout,
		WriteTimeout: c.WriteTimeout,
		ParseTime:    true,
	}
}

func openDB(cfg Config) (*sql.DB, error) {
	return sql.Open(cfg.Driver, cfg.DSN())
}

func applyPoolOptions(db *sql.DB, cfg Config) {
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
}

// normalizeContext 为单条语句执行附加 QueryTimeout；
// 已有 deadline 的 context 不再叠加。
func normalizeContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// WithHost 设置数据库主机地址（默认 127.0.0.1）。
func WithHost(host string) Option {
	return optionFunc(func(cfg *Config) { cfg.Host = host })
}

// WithPort 设置数据库端口（默认 3306）。
func WithPort(port string) Option {
	return optionFunc(func(cfg *Config) { cfg.Port = port })
}

// WithDatabase 设置目标数据库名。
func WithDatabase(name string) Option {
	return optionFunc(func(cfg *Config) { cfg.Database = name })
}

// WithUser 设置连接用户名。
func WithUser(user string) Option {
	return optionFunc(func(cfg *Config) { cfg.User = user })
}

// WithPassword 设置连接密码。
func WithPassword(password string) Option {
	return optionFunc(func(cfg *Config) { cfg.Password = password })
}

// WithDSNParam 追加自定义 DSN 参数（如 charset）。key 为空时忽略。
func WithDSNParam(key, value string) Option {
	return optionFunc(func(cfg *Config) {
		if key == "" {
			return
		}
		if cfg.Params == nil {
			cfg.Params = make(map[string]string)
		}
		cfg.Params[key] = value
	})
}

// WithLoc 设置 DSN 的时区（默认 time.Local）。nil 时忽略。
func WithLoc(loc *time.Location) Option {
	return optionFunc(func(cfg *Config) {
		if loc != nil {
			cfg.Loc = loc
		}
	})
}

// WithDialTimeout 设置建立连接的超时时间。
func WithDialTimeout(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.DialTimeout = d })
}

// WithReadTimeout 设置 I/O 读超时时间。
func WithReadTimeout(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.ReadTimeout = d })
}

// WithWriteTimeout 设置 I/O 写超时时间。
func WithWriteTimeout(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.WriteTimeout = d })
}

// WithMaxOpenConns 设置连接池最大打开连接数。
func WithMaxOpenConns(n int) Option {
	return optionFunc(func(cfg *Config) { cfg.MaxOpenConns = n })
}

// WithMaxIdleConns 设置连接池最大空闲连接数。
func WithMaxIdleConns(n int) Option {
	return optionFunc(func(cfg *Config) { cfg.MaxIdleConns = n })
}

// WithConnMaxLifetime 设置连接的最长存活时间。
func WithConnMaxLifetime(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.ConnMaxLifetime = d })
}

// WithConnMaxIdleTime 设置连接的最长空闲时间。
func WithConnMaxIdleTime(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.ConnMaxIdleTime = d })
}

// WithQueryTimeout 设置单条语句的执行超时；只作用于单条语句，
// 不限制事务整体时长（事务用 WithTxTimeout）。
func WithQueryTimeout(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.QueryTimeout = d })
}

// WithTxTimeout 限制 WithTx 中单次事务（含提交）的总时长。
func WithTxTimeout(d time.Duration) Option {
	return optionFunc(func(cfg *Config) { cfg.TxTimeout = d })
}

// WithTxRetry 开启事务死锁自动重试：检测 MySQL 1213/1205，按带抖动的
// 指数退避最多重试 maxRetries 次。开启后事务函数可能被执行多次，
// 必须保证幂等。baseWait/maxWait 传 0 时取默认值（5ms/50ms）。
func WithTxRetry(maxRetries int, baseWait, maxWait time.Duration) Option {
	return optionFunc(func(cfg *Config) {
		if maxRetries < 0 {
			maxRetries = 0
		}
		cfg.TxMaxRetries = maxRetries
		cfg.TxRetryBaseWait = baseWait
		cfg.TxRetryMaxWait = maxWait
	})
}
