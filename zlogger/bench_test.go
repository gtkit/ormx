package zlogger_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	ormlogger "gorm.io/gorm/logger"
)

func BenchmarkTraceWarnFastPath(b *testing.B) {
	logger := zlogger.New(
		zlogger.WithLogger(zap.NewNop()),
		zlogger.WithLogLevel(ormlogger.Warn),
		zlogger.WithSlowThreshold(time.Hour),
	)
	ctx := context.Background()
	var calls atomic.Int64
	fc := func() (string, int64) {
		calls.Add(1)
		return "SELECT 1", 1
	}

	b.ReportAllocs()
	for b.Loop() {
		logger.Trace(ctx, time.Now(), fc, nil)
	}
	if got := calls.Load(); got != 0 {
		b.Fatalf("SQL callback calls = %d, want 0", got)
	}
}

func BenchmarkTraceWarnFastPathParallel(b *testing.B) {
	logger := zlogger.New(
		zlogger.WithLogger(zap.NewNop()),
		zlogger.WithLogLevel(ormlogger.Warn),
		zlogger.WithSlowThreshold(time.Hour),
	)
	ctx := context.Background()
	var calls atomic.Int64
	fc := func() (string, int64) {
		calls.Add(1)
		return "SELECT 1", 1
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Trace(ctx, time.Now(), fc, nil)
		}
	})
	if got := calls.Load(); got != 0 {
		b.Fatalf("SQL callback calls = %d, want 0", got)
	}
}
