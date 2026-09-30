package zlogger_test

import (
	"fmt"
	"time"

	"github.com/gtkit/ormx"
	"github.com/gtkit/ormx/zlogger"

	"go.uber.org/zap"
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

// Use 直传 *zap.Logger 一步接入 ormx，
// 等价于 ormx.WithGormLogger(zlogger.New(zlogger.WithLogger(zlog), opts...))。
func ExampleUse() {
	cfg := ormx.NewConfig(
		zlogger.Use(zap.NewNop(), zlogger.WithSlowThreshold(300*time.Millisecond)),
	)

	fmt.Println(cfg.GORM.Logger != nil)
	// Output: true
}
