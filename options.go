package ormx

import (
	"time"

	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type Option func(*Config)

func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

func WithNetwork(network string) Option {
	return func(c *Config) {
		c.MySQL.Net = network
	}
}

func WithAddress(addr string) Option {
	return func(c *Config) {
		c.MySQL.Addr = addr
	}
}

func WithHost(host string) Option {
	return func(c *Config) {
		c.MySQL.Host = host
		c.MySQL.Addr = ""
	}
}

func WithPort(port string) Option {
	return func(c *Config) {
		c.MySQL.Port = port
		c.MySQL.Addr = ""
	}
}

func WithDatabase(name string) Option {
	return func(c *Config) {
		c.MySQL.Database = name
	}
}

func WithUser(user string) Option {
	return func(c *Config) {
		c.MySQL.User = user
	}
}

func WithPassword(password string) Option {
	return func(c *Config) {
		c.MySQL.Password = password
	}
}

func WithParseTime(enabled bool) Option {
	return func(c *Config) {
		c.MySQL.ParseTime = enabled
	}
}

func WithLocation(loc *time.Location) Option {
	return func(c *Config) {
		c.MySQL.Loc = loc
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.Timeout = timeout
	}
}

func WithReadTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.ReadTimeout = timeout
	}
}

func WithWriteTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.MySQL.WriteTimeout = timeout
	}
}

func WithTLSConfig(name string) Option {
	return func(c *Config) {
		c.MySQL.TLSConfig = name
	}
}

func WithCollation(collation string) Option {
	return func(c *Config) {
		c.MySQL.Collation = collation
	}
}

func WithConnectionAttributes(attrs string) Option {
	return func(c *Config) {
		c.MySQL.ConnectionAttributes = attrs
	}
}

func WithDSNParam(key, value string) Option {
	return func(c *Config) {
		if c.MySQL.Params == nil {
			c.MySQL.Params = make(map[string]string)
		}
		c.MySQL.Params[key] = value
	}
}

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

func WithMaxOpenConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxOpenConns = size
		c.Pool.hasMaxOpenConns = true
	}
}

func WithMaxIdleConns(size int) Option {
	return func(c *Config) {
		c.Pool.MaxIdleConns = size
		c.Pool.hasMaxIdleConns = true
	}
}

func WithConnMaxLifetime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxLifetime = duration
		c.Pool.hasConnMaxLifetime = true
	}
}

func WithConnMaxIdleTime(duration time.Duration) Option {
	return func(c *Config) {
		c.Pool.ConnMaxIdleTime = duration
		c.Pool.hasConnMaxIdleTime = true
	}
}

func WithPrepareStmt(enabled bool) Option {
	return func(c *Config) {
		c.GORM.PrepareStmt = enabled
	}
}

func WithPrepareStmtCache(maxSize int, ttl time.Duration) Option {
	return func(c *Config) {
		c.GORM.PrepareStmtMaxSize = maxSize
		c.GORM.PrepareStmtTTL = ttl
	}
}

func WithSkipDefaultTransaction(skip bool) Option {
	return func(c *Config) {
		c.GORM.SkipDefaultTransaction = skip
	}
}

func WithGormLogger(log gormlogger.Interface) Option {
	return func(c *Config) {
		c.GORM.Logger = log
	}
}

func WithHealthProbe(probe HealthProbeFunc) Option {
	return func(c *Config) {
		c.HealthProbe = probe
	}
}

func WithNowFunc(now func() time.Time) Option {
	return func(c *Config) {
		c.GORM.NowFunc = now
	}
}

func WithNamingStrategy(strategy schema.NamingStrategy) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy = strategy
	}
}

func WithTablePrefix(prefix string) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy.TablePrefix = prefix
	}
}

func WithSingularTable(enabled bool) Option {
	return func(c *Config) {
		c.GORM.NamingStrategy.SingularTable = enabled
	}
}

func WithDefaultContextTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.GORM.DefaultContextTimeout = timeout
	}
}

func WithDefaultTransactionTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.GORM.DefaultTransactionTimeout = timeout
	}
}

func WithDryRun(enabled bool) Option {
	return func(c *Config) {
		c.GORM.DryRun = enabled
	}
}

func WithQueryFields(enabled bool) Option {
	return func(c *Config) {
		c.GORM.QueryFields = enabled
	}
}

func WithCreateBatchSize(size int) Option {
	return func(c *Config) {
		c.GORM.CreateBatchSize = size
	}
}

func WithTranslateError(enabled bool) Option {
	return func(c *Config) {
		c.GORM.TranslateError = enabled
	}
}

func WithStartupPing(enabled bool) Option {
	return func(c *Config) {
		c.StartupPing = enabled
	}
}

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

func WithDriverName(name string) Option {
	return func(c *Config) {
		c.Dialect.DriverName = name
	}
}

func WithTxRetryObserver(observer TxRetryObserver) Option {
	return func(c *Config) {
		c.TxRetryObserver = observer
	}
}

func WithServerVersion(version string) Option {
	return func(c *Config) {
		c.Dialect.ServerVersion = version
	}
}

func WithSkipInitializeWithVersion(skip bool) Option {
	return func(c *Config) {
		c.Dialect.SkipInitializeWithVersion = skip
	}
}

func WithDefaultStringSize(size uint) Option {
	return func(c *Config) {
		c.Dialect.DefaultStringSize = size
	}
}

func WithDisableDatetimePrecision(disable bool) Option {
	return func(c *Config) {
		c.Dialect.DisableDatetimePrecision = disable
	}
}

func WithDisableWithReturning(disable bool) Option {
	return func(c *Config) {
		c.Dialect.DisableWithReturning = disable
	}
}
