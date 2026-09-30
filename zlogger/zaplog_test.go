package zlogger_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	ormzap "github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type paramsFilteringLogger interface {
	ParamsFilter(ctx context.Context, sql string, params ...any) (string, []any)
}

type testContextKey string

func TestLogModeSilentSuppressesSlowQueryLogs(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)

	logger := ormzap.New(ormzap.WithLogger(zap.New(core))).LogMode(gormlogger.Silent)
	logger.Trace(
		context.Background(),
		time.Now().Add(-time.Second),
		func() (string, int64) { return "SELECT 1", 1 },
		nil,
	)

	if entries := logs.All(); len(entries) != 0 {
		t.Fatalf("expected no log entries in silent mode, got %d", len(entries))
	}
}

func TestLogModeInfoLogsRegularQueries(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)

	logger := ormzap.New(ormzap.WithLogger(zap.New(core))).LogMode(gormlogger.Info)
	logger.Trace(
		context.Background(),
		time.Now().Add(-50*time.Millisecond),
		func() (string, int64) { return "SELECT 1", 1 },
		nil,
	)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if entries[0].Level != zap.InfoLevel {
		t.Fatalf("expected info level, got %s", entries[0].Level)
	}
}

func TestTraceCallsSQLCallbackOnlyWhenLogging(t *testing.T) {
	tests := []struct {
		name        string
		options     []ormzap.Option
		elapsed     time.Duration
		err         error
		wantCalls   int
		wantEntries int
	}{
		{
			name: "warn fast query",
			options: []ormzap.Option{
				ormzap.WithLogLevel(gormlogger.Warn),
				ormzap.WithSlowThreshold(time.Hour),
			},
			wantCalls:   0,
			wantEntries: 0,
		},
		{
			name: "ignored record not found",
			options: []ormzap.Option{
				ormzap.WithLogLevel(gormlogger.Warn),
				ormzap.WithIgnoreRecordNotFoundError(true),
			},
			err:         gorm.ErrRecordNotFound,
			wantCalls:   0,
			wantEntries: 0,
		},
		{
			name: "error query",
			options: []ormzap.Option{
				ormzap.WithLogLevel(gormlogger.Error),
			},
			err:         errors.New("boom"),
			wantCalls:   1,
			wantEntries: 1,
		},
		{
			name: "slow query",
			options: []ormzap.Option{
				ormzap.WithLogLevel(gormlogger.Warn),
				ormzap.WithSlowThreshold(time.Millisecond),
			},
			elapsed:     time.Second,
			wantCalls:   1,
			wantEntries: 1,
		},
		{
			name: "info query",
			options: []ormzap.Option{
				ormzap.WithLogLevel(gormlogger.Info),
				ormzap.WithSlowThreshold(time.Hour),
			},
			wantCalls:   1,
			wantEntries: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			options := append([]ormzap.Option{ormzap.WithLogger(zap.New(core))}, tt.options...)
			logger := ormzap.New(options...)
			calls := 0

			logger.Trace(
				context.Background(),
				time.Now().Add(-tt.elapsed),
				func() (string, int64) {
					calls++
					return "SELECT 1", 1
				},
				tt.err,
			)

			if calls != tt.wantCalls {
				t.Fatalf("SQL callback calls = %d, want %d", calls, tt.wantCalls)
			}
			if entries := logs.All(); len(entries) != tt.wantEntries {
				t.Fatalf("log entries = %d, want %d", len(entries), tt.wantEntries)
			}
		})
	}
}

func TestIgnoreRecordNotFoundErrorSuppressesTrace(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)

	logger := ormzap.New(
		ormzap.WithLogger(zap.New(core)),
		ormzap.WithIgnoreRecordNotFoundError(true),
	)
	logger.Trace(
		context.Background(),
		time.Now().Add(-50*time.Millisecond),
		func() (string, int64) { return "SELECT 1", 0 },
		gorm.ErrRecordNotFound,
	)

	if entries := logs.All(); len(entries) != 0 {
		t.Fatalf("expected record-not-found trace to be suppressed, got %d entries", len(entries))
	}
}

func TestParameterizedQueriesHideParameters(t *testing.T) {
	filtering, ok := ormzap.New(
		ormzap.WithLogger(zap.NewNop()),
		ormzap.WithParameterizedQueries(true),
	).(paramsFilteringLogger)
	if !ok {
		t.Fatalf("expected logger to implement ParamsFilter")
	}

	sql, params := filtering.ParamsFilter(context.Background(), "SELECT * FROM users WHERE id = ?", 42)
	if sql != "SELECT * FROM users WHERE id = ?" {
		t.Fatalf("unexpected sql %q", sql)
	}
	if len(params) != 0 {
		t.Fatalf("expected params to be hidden, got %#v", params)
	}
}

func TestParameterizedQueriesDefaultHidesParameters(t *testing.T) {
	// 不传 WithParameterizedQueries 时应默认隐藏参数（安全默认）。
	filtering, ok := ormzap.New(ormzap.WithLogger(zap.NewNop())).(paramsFilteringLogger)
	if !ok {
		t.Fatalf("expected logger to implement ParamsFilter")
	}

	_, params := filtering.ParamsFilter(context.Background(), "SELECT * FROM users WHERE id = ?", 42)
	if len(params) != 0 {
		t.Fatalf("expected default to hide bind params, got %#v", params)
	}
}

func TestWithSlowThresholdIgnoresNegative(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(
		ormzap.WithLogger(zap.New(core)),
		ormzap.WithLogLevel(gormlogger.Warn),
		ormzap.WithSlowThreshold(-1), // 负值应被忽略，保留默认 200ms
	)

	// 50ms 的查询在默认 200ms 阈值下不应被判为慢查询。
	logger.Trace(context.Background(), time.Now().Add(-50*time.Millisecond),
		func() (string, int64) { return "SELECT 1", 1 }, nil)

	if entries := logs.All(); len(entries) != 0 {
		t.Fatalf("negative slow threshold should be ignored (keep 200ms default), got %d entries", len(entries))
	}
}

func TestParameterizedQueriesReturnParamsWhenDisabled(t *testing.T) {
	filtering, ok := ormzap.New(
		ormzap.WithLogger(zap.NewNop()),
		ormzap.WithParameterizedQueries(false),
	).(paramsFilteringLogger)
	if !ok {
		t.Fatalf("expected logger to implement ParamsFilter")
	}

	sql, params := filtering.ParamsFilter(context.Background(), "SELECT * FROM users WHERE id = ?", 42)
	if sql != "SELECT * FROM users WHERE id = ?" {
		t.Fatalf("unexpected sql %q", sql)
	}
	if len(params) != 1 || params[0] != 42 {
		t.Fatalf("expected params to be preserved, got %#v", params)
	}
}

func TestTraceLogsErrorAndTraceID(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	const traceIDKey testContextKey = "trace_id"
	logger := ormzap.New(
		ormzap.WithLogger(zap.New(core)),
		ormzap.WithLogLevel(gormlogger.Error),
		ormzap.WithTraceIDExtractor(func(ctx context.Context) string {
			value, _ := ctx.Value(traceIDKey).(string)
			return value
		}),
	)

	ctx := context.WithValue(context.Background(), traceIDKey, "req-1")
	logger.Trace(
		ctx,
		time.Now().Add(-50*time.Millisecond),
		func() (string, int64) { return "SELECT 1", 3 },
		errors.New("boom"),
	)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if entries[0].Level != zap.ErrorLevel {
		t.Fatalf("expected error level, got %s", entries[0].Level)
	}
	if got := entries[0].ContextMap()["trace_id"]; got != "req-1" {
		t.Fatalf("expected trace_id req-1, got %#v", got)
	}
	if got := entries[0].ContextMap()["rows"]; got != int64(3) {
		t.Fatalf("expected rows 3, got %#v", got)
	}
}

func TestTraceTraceIDNotDuplicated(t *testing.T) {
	const traceIDKey testContextKey = "trace_id"
	extractor := ormzap.WithTraceIDExtractor(func(ctx context.Context) string {
		value, _ := ctx.Value(traceIDKey).(string)
		return value
	})

	tests := []struct {
		name    string
		options []ormzap.Option
		elapsed time.Duration
		err     error
	}{
		{
			name:    "error query",
			options: []ormzap.Option{ormzap.WithLogLevel(gormlogger.Error)},
			elapsed: 50 * time.Millisecond,
			err:     errors.New("boom"),
		},
		{
			name:    "slow query",
			options: []ormzap.Option{ormzap.WithLogLevel(gormlogger.Warn), ormzap.WithSlowThreshold(10 * time.Millisecond)},
			elapsed: time.Second,
		},
		{
			name:    "info query",
			options: []ormzap.Option{ormzap.WithLogLevel(gormlogger.Info), ormzap.WithSlowThreshold(time.Hour)},
			elapsed: 50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			options := append([]ormzap.Option{ormzap.WithLogger(zap.New(core)), extractor}, tt.options...)
			logger := ormzap.New(options...)

			ctx := context.WithValue(context.Background(), traceIDKey, "req-1")
			logger.Trace(
				ctx,
				time.Now().Add(-tt.elapsed),
				func() (string, int64) { return "SELECT 1", 1 },
				tt.err,
			)

			entries := logs.All()
			if len(entries) != 1 {
				t.Fatalf("expected one log entry, got %d", len(entries))
			}
			count := 0
			for _, field := range entries[0].Context {
				if field.Key == "trace_id" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("expected exactly one trace_id field, got %d", count)
			}
			if got := entries[0].ContextMap()["trace_id"]; got != "req-1" {
				t.Fatalf("expected trace_id req-1, got %#v", got)
			}
		})
	}
}

func TestTraceSlowQueryWithoutRowsField(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(
		ormzap.WithLogger(zap.New(core)),
		ormzap.WithLogLevel(gormlogger.Warn),
		ormzap.WithSlowThreshold(10*time.Millisecond),
	)

	logger.Trace(
		context.Background(),
		time.Now().Add(-50*time.Millisecond),
		func() (string, int64) { return "SELECT 1", -1 },
		nil,
	)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if entries[0].Level != zap.WarnLevel {
		t.Fatalf("expected warn level, got %s", entries[0].Level)
	}
	if _, ok := entries[0].ContextMap()["rows"]; ok {
		t.Fatal("expected rows field to be omitted when rows < 0")
	}
}

func TestInfoWarnErrorMethodsRespectLogLevel(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(
		ormzap.WithLogger(zap.New(core)),
		ormzap.WithLogLevel(gormlogger.Warn),
	)

	logger.Info(context.Background(), "info %s", "skip")
	logger.Warn(context.Background(), "warn %s", "keep")
	logger.Error(context.Background(), "error %s", "keep")

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("expected two log entries, got %d", len(entries))
	}
	if entries[0].Level != zap.WarnLevel {
		t.Fatalf("expected first entry warn, got %s", entries[0].Level)
	}
	if entries[1].Level != zap.ErrorLevel {
		t.Fatalf("expected second entry error, got %s", entries[1].Level)
	}
}

func TestWithLoggerNilFallsBackToNop(t *testing.T) {
	logger := ormzap.New(ormzap.WithLogger(nil))
	if logger == nil {
		t.Fatal("expected logger")
	}

	logger.Trace(
		context.Background(),
		time.Now().Add(-time.Second),
		func() (string, int64) { return "SELECT 1", 1 },
		nil,
	)
}

// Trace 的调用位置必须是 GORM 与 ormx 之外的第一个调用方：这里直接从 _test.go 调用，source 应指向本文件的调用行。
// 若跳帧判定失效（如把 zlogger 自身的帧放行），source 会退化成 zaplog.go 自身，本测试即失败。
func TestTraceSourcePointsToCaller(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(ormzap.WithLogger(zap.New(core)), ormzap.WithLogLevel(gormlogger.Info))

	_, file, line, _ := runtime.Caller(0)
	logger.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	want := fmt.Sprintf("%s:%d", file, line+1)
	if got := entries[0].ContextMap()["source"]; got != want {
		t.Fatalf("source = %v, want %s", got, want)
	}
}

// 注入开启了 AddCaller 的 zap logger 时，输出不得携带 zap 自带的 caller（它只会指向 zaplog.go）。
func TestWithLoggerDisablesZapCaller(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(ormzap.WithLogger(zap.New(core, zap.AddCaller())), ormzap.WithLogLevel(gormlogger.Info))

	logger.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if entries[0].Caller.Defined {
		t.Fatalf("expected zap caller disabled, got %s", entries[0].Caller)
	}
}

// Info/Warn/Error 与 Trace 同样要求 source 指向 GORM 与 ormx 之外的第一个调用方：
// 这里直接从 _test.go 调用，source 应为本文件的调用行；跳帧判定失效时本测试即失败。
func TestInfoWarnErrorSourcePointsToCaller(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(ormzap.WithLogger(zap.New(core, zap.AddCaller())), ormzap.WithLogLevel(gormlogger.Info))

	_, file, line, _ := runtime.Caller(0)
	logger.Info(context.Background(), "info %s", "x")
	logger.Warn(context.Background(), "warn %s", "x")
	logger.Error(context.Background(), "error %s", "x")

	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("expected three log entries, got %d", len(entries))
	}
	for i, entry := range entries {
		want := fmt.Sprintf("%s:%d", file, line+1+i)
		if got := entry.ContextMap()["source"]; got != want {
			t.Fatalf("entry %d source = %v, want %s", i, got, want)
		}
		if entry.Caller.Defined {
			t.Fatalf("entry %d must not carry zap caller, got %s", i, entry.Caller)
		}
	}
}
