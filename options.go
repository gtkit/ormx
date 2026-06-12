package ormx

import (
	"time"

	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Option 是修改 Config 的函数式配置项，配合 NewConfig、Open 等入口使用。
type Option func(*Config)

// WithName 设置数据库实例名称，用于健康检查报告与日志等可观测标识。
func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

// WithNetwork 设置连接 MySQL 使用的网络类型（如 "tcp"、"unix"）。默认 "tcp"。
func WithNetwork(network string) Option {
	return func(c *Config) {
		c.MySQL.Net = network
	}
}

// WithAddress 设置完整连接地址（如 "127.0.0.1:3306"）；
// Addr 非空时优先于 Host/Port 生效。
func WithAddress(addr string) Option {
	return func(c *Config) {
		c.MySQL.Addr = addr
	}
}

// WithHost 设置主机地址，并同时清空 Addr 以保证 Host/Port 生效。默认 "127.0.0.1"。
func WithHost(host string) Option {
	return func(c *Config) {
		c.MySQL.Host = host
		c.MySQL.Addr = ""
	}
}

// WithPort 设置端口，并同时清空 Addr 以保证 Host/Port 生效。默认 "3306"。
func WithPort(port string) Option {
	return func(c *Config) {
		c.MySQL.Port = port
		c.MySQL.Addr = ""
	}
}

// WithDatabase 设置要连接的数据库名。
func WithDatabase(name string) Option {
	return func(c *Config) {
		c.MySQL.Database = name
	}
}

// WithUser 设置连接用户名。
func WithUser(user string) Option {
	return func(c *Config) {
		c.MySQL.User = user
	}
}

// WithPassword 设置连接密码。
func WithPassword(password string) Option {
	return func(c *Config) {
		c.MySQL.Password = password
	}
}

// WithParseTime 设置是否将 DATE/DATETIME 列解析为 time.Time。默认开启。
func WithParseTime(enabled bool) Option {
	return func(c *Config) {
		c.MySQL.ParseTime = enabled
	}
}

// WithLocation 设置解析时间值使用的时区。默认 time.Local。
func WithLocation(loc *time.Location) Option {
	return func(c *Config) {
		c.MySQL.Loc = loc
	}
}

// WithTimeout 设置建立连接（拨号）超时时间。默认 10s。
func WithTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.Timeout = timeout
	}
}

// WithReadTimeout 设置 I/O 读超时时间。默认 30s。
func WithReadTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.ReadTimeout = timeout
	}
}

// WithWriteTimeout 设置 I/O 写超时时间。默认 30s。
func WithWriteTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.WriteTimeout = timeout
	}
}

// WithTLSConfig 设置 MySQL 驱动使用的 TLS 配置名称。
func WithTLSConfig(name string) Option {
	return func(c *Config) {
		c.MySQL.TLSConfig = name
	}
}

// WithCollation 设置连接使用的字符集校对规则。
func WithCollation(collation string) Option {
	return func(c *Config) {
		c.MySQL.Collation = collation
	}
}

// WithConnectionAttributes 设置 MySQL 连接属性（connection attributes）字符串。
func WithConnectionAttributes(attrs string) Option {
	return func(c *Config) {
		c.MySQL.ConnectionAttributes = attrs
	}
}

// WithDSNParam 设置单个额外的 DSN 连接参数；
// Params 为 nil 时自动初始化，同名 key 会被覆盖。
func WithDSNParam(key, value string) Option {
	return func(c *Config) {
		if c.MySQL.Params == nil {
			c.MySQL.Params = make(map[string]string)
		}
		c.MySQL.Params[key] = value
	}
}

// WithDSNParams 批量合并额外的 DSN 连接参数，同名 key 会被覆盖；
// 传入 nil 或空 map 时不做任何修改。
func WithDSNParams(params map[string]string) Option {
	return func(c *Config) {
		if len(params) == 0 {
			return
		}
		if c.MySQL.Params == nil {
			c.MySQL.Params = make(map[string]string, len(params))
		}
		for key, value := range params {
			c.MySQL.Params[key] = value
		}
	}
}

// WithMaxOpenConns 设置连接池最大打开连接数。默认 50。
// 取值透传给 [sql.DB.SetMaxOpenConns]：size ≤ 0 表示不限制。
func WithMaxOpenConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxOpenConns = size
		c.Pool.hasMaxOpenConns = true
	}
}

// WithMaxIdleConns 设置连接池最大空闲连接数。默认 10。
// 取值透传给 [sql.DB.SetMaxIdleConns]：size ≤ 0 表示不保留空闲连接。
func WithMaxIdleConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxIdleConns = size
		c.Pool.hasMaxIdleConns = true
	}
}

// WithConnMaxLifetime 设置连接可被复用的最长时间。默认 30 分钟。
// 取值透传给 [sql.DB.SetConnMaxLifetime]：duration ≤ 0 表示连接不过期。
func WithConnMaxLifetime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxLifetime = duration
		c.Pool.hasConnMaxLifetime = true
	}
}

// WithConnMaxIdleTime 设置连接最长空闲时间。默认 10 分钟。
// 取值透传给 [sql.DB.SetConnMaxIdleTime]：duration ≤ 0 表示空闲连接不因闲置被关闭。
func WithConnMaxIdleTime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxIdleTime = duration
		c.Pool.hasConnMaxIdleTime = true
	}
}

// WithPrepareStmt 设置 GORM 是否缓存预编译语句以提升后续执行性能。
func WithPrepareStmt(enabled bool) Option {
	return func(c *Config) {
		c.GORM.PrepareStmt = enabled
	}
}

// WithPrepareStmtCache 设置预编译语句缓存的最大条数 maxSize 与存活时间 ttl。
func WithPrepareStmtCache(maxSize int, ttl time.Duration) Option {
	return func(c *Config) {
		c.GORM.PrepareStmtMaxSize = maxSize
		c.GORM.PrepareStmtTTL = ttl
	}
}

// WithSkipDefaultTransaction 设置是否跳过 GORM 对单条写操作的默认事务包装。
func WithSkipDefaultTransaction(skip bool) Option {
	return func(c *Config) {
		c.GORM.SkipDefaultTransaction = skip
	}
}

// WithGormLogger 设置 GORM 使用的日志实现。
func WithGormLogger(log gormlogger.Interface) Option {
	return func(c *Config) {
		c.GORM.Logger = log
	}
}

// WithHealthProbe 设置自定义健康探针；健康检查在 Ping 成功后调用该探针，
// 探针返回错误则判定为不健康。
func WithHealthProbe(probe HealthProbeFunc) Option {
	return func(c *Config) {
		c.HealthProbe = probe
	}
}

// WithNowFunc 设置 GORM 生成时间戳时使用的当前时间函数。
func WithNowFunc(now func() time.Time) Option {
	return func(c *Config) {
		c.GORM.NowFunc = now
	}
}

// WithNamingStrategy 整体替换 GORM 的命名策略，会覆盖之前设置的表前缀等字段。
func WithNamingStrategy(strategy schema.NamingStrategy) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy = strategy
	}
}

// WithTablePrefix 设置命名策略中的表名前缀，仅修改该字段，不影响策略的其他配置。
func WithTablePrefix(prefix string) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy.TablePrefix = prefix
	}
}

// WithSingularTable 设置是否使用单数表名（如 User 对应表 user 而非 users）。
func WithSingularTable(enabled bool) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy.SingularTable = enabled
	}
}

// WithDefaultContextTimeout 设置 GORM 操作的默认 context 超时时间。
func WithDefaultContextTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.GORM.DefaultContextTimeout = timeout
	}
}

// WithDefaultTransactionTimeout 设置 GORM 事务的默认超时时间。
func WithDefaultTransactionTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.GORM.DefaultTransactionTimeout = timeout
	}
}

// WithDryRun 设置是否启用 DryRun 模式：只生成 SQL 而不真正执行。
func WithDryRun(enabled bool) Option {
	return func(c *Config) {
		c.GORM.DryRun = enabled
	}
}

// WithQueryFields 设置查询时是否按模型字段名逐列展开 SELECT，而非 SELECT *。
func WithQueryFields(enabled bool) Option {
	return func(c *Config) {
		c.GORM.QueryFields = enabled
	}
}

// WithCreateBatchSize 设置批量插入时的默认分批大小。
func WithCreateBatchSize(size int) Option {
	return func(c *Config) {
		c.GORM.CreateBatchSize = size
	}
}

// WithTranslateError 设置是否将驱动错误翻译为 GORM 统一错误类型（如 gorm.ErrDuplicatedKey）。
func WithTranslateError(enabled bool) Option {
	return func(c *Config) {
		c.GORM.TranslateError = enabled
	}
}

// WithStartupPing 设置打开连接时是否先执行 Ping 验证连通性。默认开启。
func WithStartupPing(enabled bool) Option {
	return func(c *Config) {
		c.StartupPing = enabled
	}
}

// WithStartupPingRetry 配置启动 Ping 的重试策略：maxRetries 为最大重试次数，
// baseWait、maxWait 为退避等待的基准值与上限。maxRetries 为负、baseWait 或
// maxWait 非正时，对应项被忽略并保留原值。默认不重试，基准 1s，上限 5s。
func WithStartupPingRetry(maxRetries int, baseWait, maxWait time.Duration) Option {
	return func(c *Config) {
		if maxRetries >= 0 {
			c.StartupPingMaxRetries = maxRetries
		}
		if baseWait > 0 {
			c.StartupPingRetryBaseWait = baseWait
		}
		if maxWait > 0 {
			c.StartupPingRetryMaxWait = maxWait
		}
	}
}

// WithDriverName 设置 GORM MySQL 方言使用的底层 SQL 驱动名。
func WithDriverName(name string) Option {
	return func(c *Config) {
		c.Dialect.DriverName = name
	}
}

// WithTxRetryObserver 设置事务重试观察者，事务发生重试时回调通知重试事件。
func WithTxRetryObserver(observer TxRetryObserver) Option {
	return func(c *Config) {
		c.TxRetryObserver = observer
	}
}

// WithServerVersion 手动指定 MySQL 服务端版本号，供方言据此调整行为。
func WithServerVersion(version string) Option {
	return func(c *Config) {
		c.Dialect.ServerVersion = version
	}
}

// WithSkipInitializeWithVersion 设置是否跳过初始化时根据服务端版本自动配置方言。
func WithSkipInitializeWithVersion(skip bool) Option {
	return func(c *Config) {
		c.Dialect.SkipInitializeWithVersion = skip
	}
}

// WithDefaultStringSize 设置 string 类型字段建表时的默认长度。
func WithDefaultStringSize(size uint) Option {
	return func(c *Config) {
		c.Dialect.DefaultStringSize = size
	}
}

// WithDisableDatetimePrecision 设置是否禁用 datetime 字段的精度支持。
func WithDisableDatetimePrecision(disable bool) Option {
	return func(c *Config) {
		c.Dialect.DisableDatetimePrecision = disable
	}
}

// WithDisableWithReturning 设置是否禁用方言的 RETURNING 子句支持。
func WithDisableWithReturning(disable bool) Option {
	return func(c *Config) {
		c.Dialect.DisableWithReturning = disable
	}
}
