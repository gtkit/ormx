package zlogger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ormzap "github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type paramsFilteringLogger interface {
	ParamsFilter(ctx context.Context, sql string, params ...interface{}) (string, []interface{})
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
