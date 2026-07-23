package ormx_test

import (
	"fmt"

	"github.com/gtkit/ormx"
	"github.com/gtkit/ormx/zlogger"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
)

// 用 Functional Options 构建配置，并通过 RedactedDSN 输出密码脱敏后的
// DSN（可安全打印到日志）。实际连库使用 cfg.Open(ctx) 或包级 ormx.Open。
func ExampleNewConfig() {
	cfg := ormx.NewConfig(
		ormx.WithUser("alice"),
		ormx.WithPassword("secret"),
		ormx.WithDatabase("app"),
	)

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(dsn)
	// Output: alice:******@tcp(127.0.0.1:3306)/app?loc=Local&parseTime=true&readTimeout=30s&timeout=10s&writeTimeout=30s
}

// WithZlogger 一步注入 GORM SQL 日志器，无需显式调用 zlogger.New，
// 等价于 WithGormLogger(zlogger.New(opts...))。
func ExampleWithZlogger() {
	cfg := ormx.NewConfig(
		ormx.WithZlogger(
			zlogger.WithLogger(zap.NewNop()),
			zlogger.WithLogLevel(gormlogger.Info),
			zlogger.WithIgnoreRecordNotFoundError(true),
		),
	)

	fmt.Println(cfg.GORM.Logger != nil)
	// Output: true
}

// With 返回应用新 Option 后的隔离副本，原配置不受影响
// （普通赋值 cfg2 := cfg 只是浅拷贝、仍共享 map 与指针字段，隔离请用 With/Clone）。
func ExampleConfig_With() {
	base := ormx.NewConfig(ormx.WithName("base"))
	derived := base.With(ormx.WithName("derived"))

	fmt.Println(base.Name, derived.Name)
	// Output: base derived
}
