package ormx

import (
	"maps"
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/gtkit/ormx/zlogger"
)

// Option 是修改 Config 的函数式配置项，配合 NewConfig、Open 等入口使用。
type Option func(*Config)

// WithName 设置数据库实例名称，用于 Client.Name、健康报告与事务重试事件。
func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

// WithNetwork 设置连接 MySQL 使用的网络类型（如 "tcp"、"unix"）。默认 "tcp"。
// 使用 "unix" 时必须配合 WithAddress 指定 socket 路径，否则 Open 返回 ErrAddressRequired。
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

// WithDSN 以完整 MySQL DSN（如 "user:pass@tcp(host:3306)/db?parseTime=true"）初始化
// 连接配置，适合配置里已有现成 DSN 的场景。本库未单独建模的驱动参数（multiStatements、
// maxAllowedPacket、charset 回退列表等）会原样保留并透传给驱动，连接行为与直接使用该
// DSN 一致。注意语义：
//
//   - 整体替换：MySQL 连接子配置以该 DSN 为准，DSN 未写的参数按驱动默认生效
//     （如 parseTime=false、时区 UTC、无超时），本包 DefaultConfig 的 MySQL 默认不再叠加；
//     连接池、GORM 行为等非 DSN 配置不受影响。
//   - 可继续覆盖：建议把 WithDSN 放在其它连接 Option 之前——之后的 Option 仍可按序
//     覆盖单个字段（TCP 地址已同步拆出 Host/Port，WithHost/WithPort 覆盖可用）。
//   - 失败不 panic：DSN 解析失败（含驱动已移除的 strict 等参数，此类可用
//     errors.Is(err, ErrDSNUnsupported) 判定）会保留到 Open/RedactedDSN 时报错，
//     即便后续 Option 覆盖了字段。
//
// 安全边界：DSN 仅应来自可信静态配置，不得直接接收用户输入，也不要记录包含凭据的
// 原始 DSN（日志用 RedactedDSN）；未建模的驱动参数会被原样透传，包括可能影响安全
// 边界的驱动开关（如 allowCleartextPasswords、tls=skip-verify）。
func WithDSN(dsn string) Option {
	return func(c *Config) {
		m, state, err := parseDSNConfig(dsn)
		if err != nil {
			c.dsn = &dsnState{err: err}
			return
		}
		c.MySQL = m
		c.dsn = state
	}
}

// WithParseTime 设置是否将 DATE/DATETIME 列解析为 time.Time。默认开启。
func WithParseTime(enabled bool) Option {
	return func(c *Config) {
		c.MySQL.ParseTime = enabled
	}
}

// WithLocation 设置解析时间值使用的时区。默认 time.Local。
// 传入 nil 会被忽略并保留原值，避免覆盖默认后在时间解析时因 nil Location 触发 panic。
func WithLocation(loc *time.Location) Option {
	return func(c *Config) {
		if loc == nil {
			return
		}
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

// WithTLSConfig 设置 MySQL 驱动使用的 TLS 配置名称。支持驱动内置值
// "true"、"false"、"skip-verify"、"preferred"，也支持经 mysql.RegisterTLSConfig
// 注册的名称。生产环境推荐 "true" 或启用证书验证的自定义配置；
// "preferred" 在服务端不支持 TLS 时会回退为明文连接，
// "skip-verify" 加密但不验证服务端证书，两者仅适合受控环境。
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

// WithCharset 设置连接字符集（如 "utf8mb4"）：连接建立后驱动执行 `SET NAMES <charset>`，
// 与 WithCollation 同时设置时执行 `SET NAMES <charset> COLLATE <collation>`。
// 驱动默认连接字符集已是 utf8mb4（默认 collation utf8mb4_general_ci），并非必须设置，
// 仅在需要显式指定 charset 或与 WithCollation 联用时使用；显式设置会让每条新连接
// 多执行一次 SET NAMES。
//
// 仅支持单一字符集，且标识符只允许字母、数字与下划线（charset 拼入原始 SQL，库层校验
// 以降低误配置与注入风险）。逗号分隔的回退列表（如 "utf8mb4,utf8"）无法经本 Option
// 设置（需要时用 WithDSN 的 charset 参数），传入会在 Open 时返回包装 ErrDSNUnsupported
// 的错误；同理，来自 WithDSN 的 charset 无法用 WithCharset("") 清除。
func WithCharset(charset string) Option {
	return func(c *Config) {
		c.MySQL.Charset = charset
	}
}

// WithConnectionAttributes 设置 MySQL 连接属性（connection attributes）字符串。
func WithConnectionAttributes(attrs string) Option {
	return func(c *Config) {
		c.MySQL.ConnectionAttributes = attrs
	}
}

// WithSystemVariable 追加一个连接系统变量：连接建立后驱动会执行 `SET key = value`，
// 因此 value 必须是合法的 SQL 表达式（如字符串需自带引号）。它不是 DSN 内置参数——
// loc、parseTime、timeout、charset 等 DSN 内置参数由专用 Option
// （WithLocation/WithParseTime/WithTimeout/WithCharset）处理，请勿经此设置。
// SystemVariables 为 nil 时自动初始化，同名 key 会被覆盖；空 key 会在 Open 时返回错误。
//
// 安全边界：key/value 作为原始 SQL 直接拼接为 `SET` 语句执行，仅接受可信的静态配置；
// 切勿传入 HTTP 参数、用户配置等不可信输入，否则存在会话级 SQL 注入风险。
func WithSystemVariable(key, value string) Option {
	return func(c *Config) {
		if c.MySQL.SystemVariables == nil {
			c.MySQL.SystemVariables = make(map[string]string)
		}
		c.MySQL.SystemVariables[key] = value
	}
}

// WithSystemVariables 批量追加连接系统变量（语义同 WithSystemVariable：连接后 `SET key = value`），同名 key 会被覆盖；
// 传入 nil 或空 map 时不做任何修改。
func WithSystemVariables(params map[string]string) Option {
	return func(c *Config) {
		if len(params) == 0 {
			return
		}
		if c.MySQL.SystemVariables == nil {
			c.MySQL.SystemVariables = make(map[string]string, len(params))
		}
		maps.Copy(c.MySQL.SystemVariables, params)
	}
}

// WithMaxOpenConns 设置连接池最大打开连接数。默认 50。
// 取值透传给 [sql.DB.SetMaxOpenConns]：size ≤ 0 表示不限制。
func WithMaxOpenConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxOpenConns = new(size)
	}
}

// WithMaxIdleConns 设置连接池最大空闲连接数。默认 10。
// 取值透传给 [sql.DB.SetMaxIdleConns]：size ≤ 0 表示不保留空闲连接。
func WithMaxIdleConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxIdleConns = new(size)
	}
}

// WithConnMaxLifetime 设置连接可被复用的最长时间。默认 30 分钟。
// 取值透传给 [sql.DB.SetConnMaxLifetime]：duration ≤ 0 表示连接不过期。
func WithConnMaxLifetime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxLifetime = new(duration)
	}
}

// WithConnMaxIdleTime 设置连接最长空闲时间。默认 10 分钟。
// 取值透传给 [sql.DB.SetConnMaxIdleTime]：duration ≤ 0 表示空闲连接不因闲置被关闭。
func WithConnMaxIdleTime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxIdleTime = new(duration)
	}
}

// WithPrepareStmt 设置 GORM 是否缓存预编译语句以提升后续执行性能。
func WithPrepareStmt(enabled bool) Option {
	return func(c *Config) {
		c.GORM.PrepareStmt = enabled
	}
}

// WithPrepareStmtCache 设置预编译语句缓存的最大条数 maxSize 与存活时间 ttl。
// 仅在 WithPrepareStmt(true) 时生效；未设置时沿用 GORM 的缓存默认。
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

// WithZlogger 用给定的 zlogger.Option 构造 GORM 日志器并注入，
// 等价于 WithGormLogger(zlogger.New(opts...))，省去调用方显式调用 zlogger.New。
// 不传任何 Option 时使用 zlogger 默认配置（no-op logger、慢查询 200ms、级别 Warn）。
// 需要注入自定义 gormlogger.Interface 实现时改用 WithGormLogger。
func WithZlogger(opts ...zlogger.Option) Option {
	return func(c *Config) {
		c.GORM.Logger = zlogger.New(opts...)
	}
}

// WithZapLogger 用给定的 *zap.Logger 构造 GORM 日志器并注入，是接入 zap 的最短路径，
// 等价于 WithZlogger(zlogger.WithLogger(zlog), opts...)。zlog 为 nil 时回退为 no-op（静默丢弃）。
// opts 在 logger 注入之后按序应用；默认级别 Warn、慢查询 200ms、参数化查询开启（不记录绑定参数值）。
func WithZapLogger(zlog *zap.Logger, opts ...zlogger.Option) Option {
	return func(c *Config) {
		c.GORM.Logger = zlogger.New(append([]zlogger.Option{zlogger.WithLogger(zlog)}, opts...)...)
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

// WithTxRetryObserver 设置事务重试观察者，事务发生重试时回调通知重试事件。
func WithTxRetryObserver(observer TxRetryObserver) Option {
	return func(c *Config) {
		c.TxRetryObserver = observer
	}
}

// WithServerVersion 手动指定 MySQL 服务端版本号，供方言据此调整行为。
// 仅在 WithSkipInitializeWithVersion(true) 时生效——否则会被 GORM 的 SELECT VERSION() 结果覆盖；
// 且跳过版本探测后，GORM 不再据版本自动推导兼容标志，需要时由调用方自行处理。
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
