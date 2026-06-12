package zlogger_test

import (
	"fmt"
	"time"

	"github.com/gtkit/ormx/zlogger"

	gormlogger "gorm.io/gorm/logger"
)

// 构建 GORM 日志器并交给 ormx.WithGormLogger 使用。
func ExampleNew() {
	log := zlogger.New(
		zlogger.WithSlowThreshold(300*time.Millisecond),
		zlogger.WithLogLevel(gormlogger.Warn),
	)

	fmt.Println(log != nil)
	// Output: true
}
