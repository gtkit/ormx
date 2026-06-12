package jetorm_test

import (
	"fmt"

	"github.com/gtkit/ormx/jetorm"
)

// 用 Functional Options 构建配置，并通过 RedactedDSN 输出密码脱敏后的
// DSN（可安全打印到日志）。实际连库使用 jetorm.Open(ctx, opts...)。
func ExampleNewConfig() {
	cfg := jetorm.NewConfig(
		jetorm.WithUser("alice"),
		jetorm.WithPassword("secret"),
		jetorm.WithDatabase("app"),
	)

	fmt.Println(cfg.RedactedDSN())
	// Output: alice:******@tcp(127.0.0.1:3306)/app?loc=Local&parseTime=true&readTimeout=30s&timeout=10s&writeTimeout=30s
}
