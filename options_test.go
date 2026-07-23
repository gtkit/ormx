package ormx

import (
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestDefaultLoggerIsDiscard(t *testing.T) {
	if got := NewConfig().GORM.Logger; got != gormlogger.Discard {
		t.Fatalf("expected default GORM logger to be gormlogger.Discard, got %#v", got)
	}
}

func TestNilLoggerFallsBackToDiscard(t *testing.T) {
	// WithGormLogger(nil) 或直接构造 nil Logger，最终映射到 GORM 时应兜底为 Discard，
	// 不能把 nil 交给 GORM（否则 GORM 恢复自己的默认日志器）。
	if got := NewConfig(WithGormLogger(nil)).gormConfig().Logger; got != gormlogger.Discard {
		t.Fatalf("expected nil logger to fall back to gormlogger.Discard, got %#v", got)
	}
	var raw Config
	if got := raw.gormConfig().Logger; got != gormlogger.Discard {
		t.Fatalf("expected zero-value Config logger to fall back to gormlogger.Discard, got %#v", got)
	}
}

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
		{"WithSystemVariables", WithSystemVariables(map[string]string{"time_zone": "'+00:00'"}), func(c Config) any { return c.MySQL.SystemVariables["time_zone"] }, "'+00:00'"},
		{"WithSystemVariables 空 map 不生效", WithSystemVariables(nil), func(c Config) any { return c.MySQL.SystemVariables == nil }, true},
		{"WithPrepareStmt", WithPrepareStmt(true), func(c Config) any { return c.GORM.PrepareStmt }, true},
		{"WithPrepareStmtCache", WithPrepareStmtCache(64, time.Minute), func(c Config) any {
			return [2]any{c.GORM.PrepareStmtMaxSize, c.GORM.PrepareStmtTTL}
		}, [2]any{64, time.Minute}},
		{"WithSkipDefaultTransaction", WithSkipDefaultTransaction(true), func(c Config) any { return c.GORM.SkipDefaultTransaction }, true},
		{"WithGormLogger", WithGormLogger(logger), func(c Config) any { return c.GORM.Logger == logger }, true},
		{"WithZlogger", WithZlogger(), func(c Config) any { return c.GORM.Logger != nil }, true},
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
