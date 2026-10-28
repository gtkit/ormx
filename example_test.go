package ormx_test

import (
	"fmt"
	"time"

	"github.com/gtkit/ormx"
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

// WithCharset 一行设置连接字符集，连接建立后驱动执行 SET NAMES <charset>
// （配 WithCollation 时执行 SET NAMES <charset> COLLATE <collation>）。
func ExampleWithCharset() {
	cfg := ormx.NewConfig(
		ormx.WithUser("alice"),
		ormx.WithPassword("secret"),
		ormx.WithDatabase("app"),
		ormx.WithCharset("utf8mb4"),
	)

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(dsn)
	// Output: alice:******@tcp(127.0.0.1:3306)/app?charset=utf8mb4&loc=Local&parseTime=true&readTimeout=30s&timeout=10s&writeTimeout=30s
}

// WithDSN 直接以完整 DSN 初始化连接配置（整体替换 MySQL 子配置，
// DSN 未写的参数按驱动默认），后续 Option 仍可覆盖单个字段。
func ExampleWithDSN() {
	cfg := ormx.NewConfig(
		ormx.WithDSN("alice:secret@tcp(db.internal:3307)/app?charset=utf8mb4&parseTime=true"),
		ormx.WithName("orders"),
	)

	dsn, err := cfg.RedactedDSN()
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(dsn)
	// Output: alice:******@tcp(db.internal:3307)/app?charset=utf8mb4&parseTime=true
}

// With 返回应用新 Option 后的隔离副本，原配置不受影响
// （普通赋值 cfg2 := cfg 只是浅拷贝、仍共享 map 与指针字段，隔离请用 With/Clone）。
func ExampleConfig_With() {
	base := ormx.NewConfig(ormx.WithName("base"))
	derived := base.With(ormx.WithName("derived"))

	fmt.Println(base.Name, derived.Name)
	// Output: base derived
}

// 数值仅用于展示配置；容量与超时应按部署环境确认。本例不连接数据库。
func ExampleNewConfig_production() {
	cfg := ormx.NewConfig(
		ormx.WithName("orders"),
		ormx.WithHost("mysql.internal"),
		ormx.WithDatabase("orders"),
		ormx.WithUser("orders_app"),
		ormx.WithPassword("<injected-password>"),
		ormx.WithTLSConfig("true"),
		ormx.WithLocation(time.UTC),
		ormx.WithSystemVariable("time_zone", "'+00:00'"),
		ormx.WithTimeout(3*time.Second),
		ormx.WithReadTimeout(5*time.Second),
		ormx.WithWriteTimeout(5*time.Second),
		ormx.WithDefaultContextTimeout(2*time.Second),
		ormx.WithDefaultTransactionTimeout(5*time.Second),
		ormx.WithMaxOpenConns(20),
		ormx.WithMaxIdleConns(10),
		ormx.WithConnMaxLifetime(3*time.Minute),
		ormx.WithConnMaxIdleTime(time.Minute),
	)
	fmt.Println(cfg.MySQL.TLSConfig, cfg.MySQL.Loc)
	fmt.Println(cfg.GORM.DefaultContextTimeout, cfg.GORM.DefaultTransactionTimeout)
	fmt.Println(*cfg.Pool.MaxOpenConns, *cfg.Pool.MaxIdleConns)
	// Output:
	// true UTC
	// 2s 5s
	// 20 10
}
