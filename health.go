package ormx

import (
	"context"
	"database/sql"
	"time"
)

// HealthStatus 表示健康检查结果的状态。
type HealthStatus string

// 健康状态（HealthStatus）的预定义枚举值。
const (
	HealthStatusUp   HealthStatus = "up"
	HealthStatusDown HealthStatus = "down"
)

// DBStatsSnapshot 是 sql.DBStats 的快照，并附带连接利用率 Utilization
// （InUse / MaxOpenConnections，MaxOpenConnections 为 0 时取 0）。
type DBStatsSnapshot struct {
	MaxOpenConnections int
	OpenConnections    int
	InUse              int
	Idle               int
	WaitCount          int64
	WaitDuration       time.Duration
	MaxIdleClosed      int64
	MaxIdleTimeClosed  int64
	MaxLifetimeClosed  int64
	Utilization        float64
}

// HealthProbeFunc 是自定义健康探测函数，在 Ping 成功后执行额外检查，返回非 nil 错误表示连接不健康。
type HealthProbeFunc func(ctx context.Context, client *Client) error

// HealthReport 描述一次健康检查的结果。
type HealthReport struct {
	Name      string
	Status    HealthStatus
	CheckedAt time.Time
	Duration  time.Duration
	Error     error
	Stats     DBStatsSnapshot
}

// Healthy 报告本次检查状态是否为 HealthStatusUp。
func (r HealthReport) Healthy() bool {
	return r.Status == HealthStatusUp
}

// Name 返回客户端名称，未在 Config 中配置时返回 "default"。
func (c *Client) Name() string {
	return c.effectiveName()
}

// HealthCheck 执行一次健康检查（Ping 加可选的 HealthProbe）并返回报告。
// 当 ctx 未设置 deadline 时使用内置默认超时，避免无限阻塞。
func (c *Client) HealthCheck(ctx context.Context) HealthReport {
	ctx = normalizeContext(ctx)

	// Apply a default timeout if the caller did not set a deadline,
	// preventing health checks from blocking indefinitely.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultHealthCheckTimeout)
		defer cancel()
	}

	start := time.Now()
	report := HealthReport{
		Name:      c.effectiveName(),
		CheckedAt: start,
		Status:    HealthStatusUp,
	}

	if err := c.PingContext(ctx); err != nil {
		report.Status = HealthStatusDown
		report.Error = err
	} else if c.config.HealthProbe != nil {
		if probeErr := c.config.HealthProbe(ctx, c); probeErr != nil {
			report.Status = HealthStatusDown
			report.Error = probeErr
		}
	}

	report.Duration = time.Since(start)
	report.Stats = c.StatsSnapshot()
	return report
}

// StatsSnapshot 返回当前连接池统计信息的快照。
func (c *Client) StatsSnapshot() DBStatsSnapshot {
	return newDBStatsSnapshot(c.sqlDB.Stats())
}

func (c *Client) effectiveName() string {
	if c.config.Name != "" {
		return c.config.Name
	}
	return "default"
}

func newDBStatsSnapshot(stats sql.DBStats) DBStatsSnapshot {
	snapshot := DBStatsSnapshot{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUse:              stats.InUse,
		Idle:               stats.Idle,
		WaitCount:          stats.WaitCount,
		WaitDuration:       stats.WaitDuration,
		MaxIdleClosed:      stats.MaxIdleClosed,
		MaxIdleTimeClosed:  stats.MaxIdleTimeClosed,
		MaxLifetimeClosed:  stats.MaxLifetimeClosed,
	}

	if stats.MaxOpenConnections > 0 {
		snapshot.Utilization = float64(stats.InUse) / float64(stats.MaxOpenConnections)
	}

	return snapshot
}
