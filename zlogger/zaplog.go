// Package zlogger 提供基于 zap 的 GORM 日志适配器，
// 实现 gorm.io/gorm/logger 的 Interface，支持慢查询阈值、
// 日志级别、忽略 ErrRecordNotFound、参数化 SQL 以及 trace ID 关联等配置。
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

// TraceIDExtractor 从 context 提取 trace/request ID 用于日志关联；
// 无可用 ID 时应返回空字符串。
type TraceIDExtractor func(ctx context.Context) string

// gormLogger 是基于 zap 的 GORM 日志器，实现 gorm.io/gorm/logger 的 Interface。
// 内部类型，请通过 New 配合 Option 构造；零值可安全使用但会静默丢弃所有日志
// （未注入 zap.Logger 时回退为 no-op）。
type gormLogger struct {
	zapLogger        *zap.Logger
	sugar            *zap.SugaredLogger // cached to avoid per-call allocation
	slowThreshold    time.Duration
	logLevel         gormlogger.LogLevel
	traceIDExtractor TraceIDExtractor

	ignoreRecordNotFoundError bool
	parameterizedQueries      bool
}

var _ gormlogger.Interface = (*gormLogger)(nil)

// New 按给定 Option 构造一个 gormLogger 并以 gormlogger.Interface 返回。
// 默认使用 no-op logger、慢查询阈值 200ms、日志级别 Warn、参数化查询开启（不记录绑定参数值）；
// nil Option 会被跳过。调试需要看真实参数值时用 WithParameterizedQueries(false) 显式关闭。
func New(options ...Option) gormlogger.Interface {
	logger := &gormLogger{
		zapLogger:            nopLogger,
		slowThreshold:        slowTime,
		logLevel:             gormlogger.Warn,
		parameterizedQueries: true,
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

// LogMode 返回一个使用指定日志级别的副本，原 logger 不受影响。
func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.logLevel = level
	// sugar and base are inherited from the original — no re-allocation needed.
	return &clone
}

// Info 在日志级别不低于 Info 时按 Printf 风格输出 Info 日志。
func (l *gormLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Info {
		return
	}
	l.getSugar(ctx).Infof(msg, data...)
}

// Warn 在日志级别不低于 Warn 时按 Printf 风格输出 Warn 日志。
func (l *gormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Warn {
		return
	}
	l.getSugar(ctx).Warnf(msg, data...)
}

// Error 在日志级别不低于 Error 时按 Printf 风格输出 Error 日志。
func (l *gormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.logLevel < gormlogger.Error {
		return
	}
	l.getSugar(ctx).Errorf(msg, data...)
}

// Trace 记录一次 SQL 执行：出错时（除被忽略的 gorm.ErrRecordNotFound 外）输出 Error 日志；
// 慢查询阈值非 0 且耗时超过阈值时输出 Warn 慢查询日志；级别为 Info 时输出普通查询日志；
// 级别为 Silent 时不输出。日志字段包含调用位置、耗时、SQL、影响行数及可选的 trace_id。
// 无需输出日志时不会调用 fc，避免正常快查询产生 SQL 格式化开销。
func (l *gormLogger) Trace(
	ctx context.Context, begin time.Time,
	fc func() (sql string, rowsAffected int64), err error,
) {
	if l.logLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	recordNotFoundIgnored := errors.Is(err, gorm.ErrRecordNotFound) && l.ignoreRecordNotFoundError

	switch {
	case err != nil && l.logLevel >= gormlogger.Error && !recordNotFoundIgnored:
		sql, rows := fc()
		fields := l.traceFields(ctx, elapsed, sql, rows, utils.FileWithLineNum())
		l.base().Error("gorm query error", append(fields, zap.Error(err))...)
	case l.slowThreshold != 0 && elapsed > l.slowThreshold && l.logLevel >= gormlogger.Warn:
		sql, rows := fc()
		fields := l.traceFields(ctx, elapsed, sql, rows, utils.FileWithLineNum())
		l.base().Warn("gorm slow query", append(fields, zap.Duration("slow_threshold", l.slowThreshold))...)
	case l.logLevel == gormlogger.Info:
		sql, rows := fc()
		fields := l.traceFields(ctx, elapsed, sql, rows, utils.FileWithLineNum())
		l.base().Info("gorm query", fields...)
	}
}

// ParamsFilter 实现 GORM 的参数过滤钩子：启用参数化查询（WithParameterizedQueries）时
// 返回原始 SQL 并丢弃绑定参数，使日志中不出现真实参数值；否则原样返回 SQL 与参数。
func (l *gormLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if l.parameterizedQueries {
		return sql, nil
	}
	return sql, params
}

// traceFields 组装 Trace 日志的公共字段。source 必须由 Trace 在自己的栈帧内取得后传入：
// GORM 的 FileWithLineNum 按固定跳帧数开始扫描，在这里调用会多一层帧，
// 扫描在 Trace 处即停、返回 zaplog.go 自身而非业务调用方。
func (l *gormLogger) traceFields(
	ctx context.Context, elapsed time.Duration, sql string, rows int64, source string,
) []zap.Field {
	const maxTraceFields = 6
	fields := make([]zap.Field, 0, maxTraceFields)
	fields = append(fields,
		zap.String("source", source),
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

func (l *gormLogger) base() *zap.Logger {
	if l == nil || l.zapLogger == nil {
		return nopLogger
	}
	return l.zapLogger
}

// getSugar returns the cached sugared logger, enriched with the trace ID from ctx if available.
func (l *gormLogger) getSugar(ctx context.Context) *zap.SugaredLogger {
	if traceID := l.extractTraceID(ctx); traceID != "" {
		return l.base().With(zap.String("trace_id", traceID)).Sugar()
	}
	if l.sugar != nil {
		return l.sugar
	}
	return l.base().Sugar()
}

func (l *gormLogger) extractTraceID(ctx context.Context) string {
	if l.traceIDExtractor == nil || ctx == nil {
		return ""
	}
	return l.traceIDExtractor(ctx)
}
