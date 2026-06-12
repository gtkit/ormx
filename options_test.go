package ormx

import (
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestOptionsApply(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	logger := gormlogger.Default
	nowFunc := func() time.Time { return time.Time{} }

	tests := []struct {
		name string
		opt  Option
		got  func(c Config) any
		want any
	}{
		{"WithNetwork", WithNetwork("unix"), func(c Config) any { return c.MySQL.Net }, "unix"},
		{"WithAddress", WithAddress("db:3307"), func(c Config) any { return c.MySQL.Addr }, "db:3307"},
		{"WithParseTime", WithParseTime(true), func(c Config) any { return c.MySQL.ParseTime }, true},
		{"WithLocation", WithLocation(loc), func(c Config) any { return c.MySQL.Loc }, loc},
		{"WithTLSConfig", WithTLSConfig("custom"), func(c Config) any { return c.MySQL.TLSConfig }, "custom"},
		{"WithCollation", WithCollation("utf8mb4_general_ci"), func(c Config) any { return c.MySQL.Collation }, "utf8mb4_general_ci"},
		{"WithConnectionAttributes", WithConnectionAttributes("program_name:demo"), func(c Config) any { return c.MySQL.ConnectionAttributes }, "program_name:demo"},
		{"WithDSNParams", WithDSNParams(map[string]string{"charset": "utf8mb4"}), func(c Config) any { return c.MySQL.Params["charset"] }, "utf8mb4"},
		{"WithDSNParams 空 map 不生效", WithDSNParams(nil), func(c Config) any { return c.MySQL.Params == nil }, true},
		{"WithPrepareStmt", WithPrepareStmt(true), func(c Config) any { return c.GORM.PrepareStmt }, true},
		{"WithPrepareStmtCache", WithPrepareStmtCache(64, time.Minute), func(c Config) any {
			return [2]any{c.GORM.PrepareStmtMaxSize, c.GORM.PrepareStmtTTL}
		}, [2]any{64, time.Minute}},
		{"WithSkipDefaultTransaction", WithSkipDefaultTransaction(true), func(c Config) any { return c.GORM.SkipDefaultTransaction }, true},
		{"WithGormLogger", WithGormLogger(logger), func(c Config) any { return c.GORM.Logger == logger }, true},
		{"WithNowFunc", WithNowFunc(nowFunc), func(c Config) any { return c.GORM.NowFunc != nil }, true},
		{"WithNamingStrategy", WithNamingStrategy(schema.NamingStrategy{TablePrefix: "t_"}), func(c Config) any { return c.GORM.NamingStrategy.TablePrefix }, "t_"},
		{"WithTablePrefix", WithTablePrefix("app_"), func(c Config) any { return c.GORM.NamingStrategy.TablePrefix }, "app_"},
		{"WithSingularTable", WithSingularTable(true), func(c Config) any { return c.GORM.NamingStrategy.SingularTable }, true},
		{"WithDefaultContextTimeout", WithDefaultContextTimeout(3 * time.Second), func(c Config) any { return c.GORM.DefaultContextTimeout }, 3 * time.Second},
		{"WithDefaultTransactionTimeout", WithDefaultTransactionTimeout(5 * time.Second), func(c Config) any { return c.GORM.DefaultTransactionTimeout }, 5 * time.Second},
		{"WithDryRun", WithDryRun(true), func(c Config) any { return c.GORM.DryRun }, true},
		{"WithQueryFields", WithQueryFields(true), func(c Config) any { return c.GORM.QueryFields }, true},
		{"WithCreateBatchSize", WithCreateBatchSize(500), func(c Config) any { return c.GORM.CreateBatchSize }, 500},
		{"WithTranslateError", WithTranslateError(true), func(c Config) any { return c.GORM.TranslateError }, true},
		{"WithDriverName", WithDriverName("mysql-custom"), func(c Config) any { return c.Dialect.DriverName }, "mysql-custom"},
		{"WithServerVersion", WithServerVersion("8.4.0"), func(c Config) any { return c.Dialect.ServerVersion }, "8.4.0"},
		{"WithDefaultStringSize", WithDefaultStringSize(191), func(c Config) any { return c.Dialect.DefaultStringSize }, uint(191)},
		{"WithDisableDatetimePrecision", WithDisableDatetimePrecision(true), func(c Config) any { return c.Dialect.DisableDatetimePrecision }, true},
		{"WithDisableWithReturning", WithDisableWithReturning(true), func(c Config) any { return c.Dialect.DisableWithReturning }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(Config{}.With(tt.opt)); got != tt.want {
				t.Fatalf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
