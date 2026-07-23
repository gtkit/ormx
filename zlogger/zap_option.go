package zlogger

import (
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
)

// Option 用于在构造 gormLogger 时定制其配置。
type Option func(l *gormLogger)

// WithLogger 设置底层使用的 zap.Logger；传入 nil 时回退为 no-op logger。
func WithLogger(log *zap.Logger) Option {
	return func(l *gormLogger) {
		if log == nil {
			l.zapLogger = nopLogger
			return
		}
		l.zapLogger = log
	}
}

// WithSlowThreshold 设置慢查询阈值；Trace 中执行耗时超过该阈值时按慢查询以 Warn 级别记录，设为 0 表示关闭慢查询日志。
func WithSlowThreshold(t time.Duration) Option {
	return func(l *gormLogger) {
		l.slowThreshold = t
	}
}

// WithLogLevel 设置 GORM 日志级别，低于该级别的日志不会输出。
func WithLogLevel(level gormlogger.LogLevel) Option {
	return func(l *gormLogger) {
		l.logLevel = level
	}
}

// WithIgnoreRecordNotFoundError 设置是否忽略 gorm.ErrRecordNotFound：开启后 Trace 不再把该错误作为错误日志输出。
func WithIgnoreRecordNotFoundError(enabled bool) Option {
	return func(l *gormLogger) {
		l.ignoreRecordNotFoundError = enabled
	}
}

// WithParameterizedQueries 设置是否以参数化形式记录 SQL：开启后 ParamsFilter 会丢弃绑定参数，日志中不出现真实参数值。
// 默认开启（安全优先，避免密码、Token 等敏感绑定值进入日志）；仅在调试需要看真实参数值时显式传 false 关闭。
func WithParameterizedQueries(enabled bool) Option {
	return func(l *gormLogger) {
		l.parameterizedQueries = enabled
	}
}

// WithTraceIDExtractor 设置从 context 提取 trace/request ID 的函数。
// 提取到的 ID 会作为 "trace_id" 字段附加到每条日志，用于串联 SQL 日志与请求链路。
//
// 示例：
//
//	WithTraceIDExtractor(func(ctx context.Context) string {
//	    if id, ok := ctx.Value("X-Request-ID").(string); ok {
//	        return id
//	    }
//	    return ""
//	})
func WithTraceIDExtractor(fn TraceIDExtractor) Option {
	return func(l *gormLogger) {
		l.traceIDExtractor = fn
	}
}
