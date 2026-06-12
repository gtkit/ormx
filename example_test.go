package ormx_test

import (
	"fmt"

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

// Config 是值语义：With 返回应用新 Option 后的副本，原配置不受影响。
func ExampleConfig_With() {
	base := ormx.NewConfig(ormx.WithName("base"))
	derived := base.With(ormx.WithName("derived"))

	fmt.Println(base.Name, derived.Name)
	// Output: base derived
}
