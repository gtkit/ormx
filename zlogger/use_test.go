package zlogger_test

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/gtkit/ormx"
	"github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	gormlogger "gorm.io/gorm/logger"
)

func traceOnce(l gormlogger.Interface) {
	l.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)
}

// Use 必须把日志送到传入的 zap logger，且行为与 WithGormLogger(New(WithLogger(zl), opts...)) 一致：
// 同级别、同消息、同字段集合，且两条路径都不携带 zap 自带的 caller。
func TestUseEquivalentToWithGormLoggerNew(t *testing.T) {
	coreA, logsA := observer.New(zap.DebugLevel)
	coreB, logsB := observer.New(zap.DebugLevel)
	opts := []zlogger.Option{zlogger.WithLogLevel(gormlogger.Info)}

	viaUse := ormx.NewConfig(zlogger.Use(zap.New(coreA, zap.AddCaller()), opts...)).GORM.Logger
	viaNew := ormx.NewConfig(ormx.WithGormLogger(zlogger.New(
		append([]zlogger.Option{zlogger.WithLogger(zap.New(coreB, zap.AddCaller()))}, opts...)...,
	))).GORM.Logger
	traceOnce(viaUse)
	traceOnce(viaNew)

	a, b := logsA.All(), logsB.All()
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("entries via Use = %d, via New = %d, want 1 and 1", len(a), len(b))
	}
	if a[0].Level != b[0].Level || a[0].Message != b[0].Message {
		t.Fatalf("Use logged %s %q, New logged %s %q", a[0].Level, a[0].Message, b[0].Level, b[0].Message)
	}
	keysA := slices.Sorted(maps.Keys(a[0].ContextMap()))
	keysB := slices.Sorted(maps.Keys(b[0].ContextMap()))
	if !slices.Equal(keysA, keysB) {
		t.Fatalf("field keys via Use = %v, via New = %v", keysA, keysB)
	}
	if a[0].Caller.Defined || b[0].Caller.Defined {
		t.Fatal("zap caller must be disabled on both paths")
	}
}

// opts 在 WithLogger(zl) 之后应用：opts 里再传 WithLogger 以后者为准。
// 若 Use 把 opts 放到 WithLogger 之前，日志会落到第一个 logger，本测试即失败。
func TestUseOptionsApplyAfterLogger(t *testing.T) {
	coreA, logsA := observer.New(zap.DebugLevel)
	coreB, logsB := observer.New(zap.DebugLevel)

	l := ormx.NewConfig(zlogger.Use(zap.New(coreA),
		zlogger.WithLogger(zap.New(coreB)),
		zlogger.WithLogLevel(gormlogger.Info),
	)).GORM.Logger
	traceOnce(l)

	if logsA.Len() != 0 || logsB.Len() != 1 {
		t.Fatalf("entries: first logger = %d, overriding logger = %d; want 0 and 1", logsA.Len(), logsB.Len())
	}
}

// nil zap logger 回退 no-op：日志器非 nil、不 panic、不输出。
func TestUseNilLoggerFallsBackToNop(t *testing.T) {
	l := ormx.NewConfig(zlogger.Use(nil, zlogger.WithLogLevel(gormlogger.Info))).GORM.Logger
	if l == nil {
		t.Fatal("expected non-nil GORM logger for nil zap logger")
	}
	traceOnce(l)
	l.Error(context.Background(), "ignored %s", "x")
}
