package zlogger

import (
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
)

type Option func(l *GormLogger)

func WithLogger(log *zap.Logger) Option {
	return func(l *GormLogger) {
		if log == nil {
			l.zapLogger = nopLogger
			return
		}
		l.zapLogger = log
	}
}

func WithSlowThreshold(t time.Duration) Option {
	return func(l *GormLogger) {
		l.slowThreshold = t
	}
}

func WithLogLevel(level gormlogger.LogLevel) Option {
	return func(l *GormLogger) {
		l.logLevel = level
	}
}

func WithIgnoreRecordNotFoundError(enabled bool) Option {
	return func(l *GormLogger) {
		l.ignoreRecordNotFoundError = enabled
	}
}

func WithParameterizedQueries(enabled bool) Option {
	return func(l *GormLogger) {
		l.parameterizedQueries = enabled
	}
}

// WithTraceIDExtractor sets a function that extracts a trace/request ID from context.
// The extracted ID is attached to every log entry as the "trace_id" field,
// enabling correlation between SQL logs and the originating request.
//
// Example:
//
//	WithTraceIDExtractor(func(ctx context.Context) string {
//	    if id, ok := ctx.Value("X-Request-ID").(string); ok {
//	        return id
//	    }
//	    return ""
//	})
func WithTraceIDExtractor(fn TraceIDExtractor) Option {
	return func(l *GormLogger) {
		l.traceIDExtractor = fn
	}
}
