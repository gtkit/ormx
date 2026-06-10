package zlogger

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

const slowTime = 200 * time.Millisecond

// nopLogger is a package-level singleton to avoid repeated allocations.
var nopLogger = zap.NewNop()

// TraceIDExtractor extracts a trace/request ID from a context for log correlation.
// Return an empty string if no trace ID is present.
type TraceIDExtractor func(ctx context.Context) string

type GormLogger struct {
	zapLogger        *zap.Logger
	sugar            *zap.SugaredLogger // cached to avoid per-call allocation
	slowThreshold    time.Duration
	logLevel         gormlogger.LogLevel
	traceIDExtractor TraceIDExtractor

	ignoreRecordNotFoundError bool
	parameterizedQueries      bool
}

func _() {
	var _ gormlogger.Interface = (*GormLogger)(nil)
}

func New(options ...Option) gormlogger.Interface {
	logger := &GormLogger{
		zapLogger:     nopLogger,
		slowThreshold: slowTime,
		logLevel:      gormlogger.Warn,
	}
	for _, option := range options {
		if option != nil {
			option(logger)
		}
	}
	// Cache the sugared logger after all options are applied.
	logger.sugar = logger.base().Sugar()
	return logger
}

func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.logLevel = level
	// sugar and base are inherited from the original — no re-allocation needed.
	return &clone
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Info {
		return
	}
	l.getSugar(ctx).Infof(msg, data...)
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Warn {
		return
	}
	l.getSugar(ctx).Warnf(msg, data...)
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Error {
		return
	}
	l.getSugar(ctx).Errorf(msg, data...)
}

func (l *GormLogger) Trace(
	ctx context.Context, begin time.Time,
	fc func() (sql string, rowsAffected int64), err error,
) {
	if l.logLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	fields := l.traceFields(ctx, elapsed, sql, rows)

	recordNotFoundIgnored := errors.Is(err, gorm.ErrRecordNotFound) && l.ignoreRecordNotFoundError

	switch {
	case err != nil && l.logLevel >= gormlogger.Error && !recordNotFoundIgnored:
		l.getBase(ctx).Error("gorm query error", append(fields, zap.Error(err))...)
	case l.slowThreshold != 0 && elapsed > l.slowThreshold && l.logLevel >= gormlogger.Warn:
		l.getBase(ctx).Warn("gorm slow query", append(fields, zap.Duration("slow_threshold", l.slowThreshold))...)
	case l.logLevel == gormlogger.Info:
		l.getBase(ctx).Info("gorm query", fields...)
	}
}

func (l *GormLogger) ParamsFilter(_ context.Context, sql string, params ...interface{}) (string, []interface{}) {
	if l.parameterizedQueries {
		return sql, nil
	}
	return sql, params
}

// traceFields builds the common zap fields for a Trace call.
func (l *GormLogger) traceFields(ctx context.Context, elapsed time.Duration, sql string, rows int64) []zap.Field {
	const maxTraceFields = 6
	fields := make([]zap.Field, 0, maxTraceFields)
	fields = append(fields,
		zap.String("source", utils.FileWithLineNum()),
		zap.Duration("elapsed", elapsed),
		zap.String("sql", sql),
	)
	if rows >= 0 {
		fields = append(fields, zap.Int64("rows", rows))
	}
	if traceID := l.extractTraceID(ctx); traceID != "" {
		fields = append(fields, zap.String("trace_id", traceID))
	}
	return fields
}

func (l *GormLogger) base() *zap.Logger {
	if l == nil || l.zapLogger == nil {
		return nopLogger
	}
	return l.zapLogger
}

// getBase returns the base logger, enriched with the trace ID from ctx if available.
func (l *GormLogger) getBase(ctx context.Context) *zap.Logger {
	logger := l.base()
	if traceID := l.extractTraceID(ctx); traceID != "" {
		return logger.With(zap.String("trace_id", traceID))
	}
	return logger
}

// getSugar returns the cached sugared logger, enriched with the trace ID from ctx if available.
func (l *GormLogger) getSugar(ctx context.Context) *zap.SugaredLogger {
	if traceID := l.extractTraceID(ctx); traceID != "" {
		return l.base().With(zap.String("trace_id", traceID)).Sugar()
	}
	if l.sugar != nil {
		return l.sugar
	}
	return l.base().Sugar()
}

func (l *GormLogger) extractTraceID(ctx context.Context) string {
	if l.traceIDExtractor == nil || ctx == nil {
		return ""
	}
	return l.traceIDExtractor(ctx)
}
