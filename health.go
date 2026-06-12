package ormx

import (
	"context"
	"database/sql"
	"maps"
	"time"
)

// HealthStatus 表示健康检查结果的状态。
type HealthStatus string

// NodeState 表示节点的运行状态。
type NodeState string

// 健康状态（HealthStatus）、节点角色（NodeRole）与节点状态（NodeState）的预定义枚举值。
const (
	HealthStatusUp       HealthStatus = "up"
	HealthStatusDown     HealthStatus = "down"
	HealthStatusDegraded HealthStatus = "degraded"
	RoleStandalone       NodeRole     = "standalone"
	RolePrimary          NodeRole     = "primary"
	RoleReplica          NodeRole     = "replica"

	NodeStateReady    NodeState = "ready"
	NodeStateDraining NodeState = "draining"
	NodeStateDown     NodeState = "down"
)

// NodeRole 表示节点在集群中的角色。
type NodeRole string

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

// MetricSample 表示一条带标签的指标采样。
type MetricSample struct {
	Name   string
	Value  float64
	Labels map[string]string
}

// HealthProbeFunc 是自定义健康探测函数，在 Ping 成功后执行额外检查，返回非 nil 错误表示节点不健康。
type HealthProbeFunc func(ctx context.Context, client *Client, role NodeRole) error

// HealthReport 描述一次健康检查的结果。
type HealthReport struct {
	Name      string
	Role      NodeRole
	State     NodeState
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
	return c.effectiveName("default")
}

// HealthCheck 以 RoleStandalone 角色执行一次健康检查（Ping 加可选的 HealthProbe）并返回报告。
// 当 ctx 未设置 deadline 时使用内置默认超时，避免无限阻塞。
func (c *Client) HealthCheck(ctx context.Context) HealthReport {
	return c.healthCheck(ctx, c.effectiveName("default"), RoleStandalone)
}

// StatsSnapshot 返回当前连接池统计信息的快照。
func (c *Client) StatsSnapshot() DBStatsSnapshot {
	return newDBStatsSnapshot(c.sqlDB.Stats())
}

// Metrics 返回连接池的指标采样列表，标签含客户端名称与 RoleStandalone 角色。
func (c *Client) Metrics() []MetricSample {
	return c.metrics(c.effectiveName("default"), RoleStandalone)
}

func (c *Client) healthCheck(ctx context.Context, name string, role NodeRole) HealthReport {
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
		Name:      name,
		Role:      role,
		State:     NodeStateReady,
		CheckedAt: start,
		Stats:     c.StatsSnapshot(),
		Status:    HealthStatusUp,
	}

	if err := c.PingContext(ctx); err != nil {
		report.Status = HealthStatusDown
		report.State = NodeStateDown
		report.Error = err
	} else if c.config.HealthProbe != nil {
		if probeErr := c.config.HealthProbe(ctx, c, role); probeErr != nil {
			report.Status = HealthStatusDown
			report.State = NodeStateDown
			report.Error = probeErr
		}
	}

	report.Duration = time.Since(start)
	report.Stats = c.StatsSnapshot()
	return report
}

func (c *Client) metrics(name string, role NodeRole) []MetricSample {
	return c.StatsSnapshot().metrics(metricLabels(name, role))
}

func (c *Client) effectiveName(fallback string) string {
	if c == nil {
		return fallback
	}
	if c.config.Name != "" {
		return c.config.Name
	}
	return fallback
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

func (s DBStatsSnapshot) metrics(labels map[string]string) []MetricSample {
	return []MetricSample{
		{Name: "orm_db_max_open_connections", Value: float64(s.MaxOpenConnections), Labels: cloneLabels(labels)},
		{Name: "orm_db_open_connections", Value: float64(s.OpenConnections), Labels: cloneLabels(labels)},
		{Name: "orm_db_in_use_connections", Value: float64(s.InUse), Labels: cloneLabels(labels)},
		{Name: "orm_db_idle_connections", Value: float64(s.Idle), Labels: cloneLabels(labels)},
		{Name: "orm_db_wait_count_total", Value: float64(s.WaitCount), Labels: cloneLabels(labels)},
		{Name: "orm_db_wait_duration_seconds_total", Value: s.WaitDuration.Seconds(), Labels: cloneLabels(labels)},
		{Name: "orm_db_max_idle_closed_total", Value: float64(s.MaxIdleClosed), Labels: cloneLabels(labels)},
		{Name: "orm_db_max_idle_time_closed_total", Value: float64(s.MaxIdleTimeClosed), Labels: cloneLabels(labels)},
		{Name: "orm_db_max_lifetime_closed_total", Value: float64(s.MaxLifetimeClosed), Labels: cloneLabels(labels)},
		{Name: "orm_db_connection_utilization", Value: s.Utilization, Labels: cloneLabels(labels)},
	}
}

func metricLabels(name string, role NodeRole) map[string]string {
	labels := map[string]string{
		"role": string(role),
	}
	if name != "" {
		labels["name"] = name
	}
	return labels
}

func cloneLabels(src map[string]string) map[string]string {
	return maps.Clone(src)
}
