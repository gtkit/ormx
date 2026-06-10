package ormx

import (
	"context"
	"database/sql"
	"maps"
	"time"
)

type HealthStatus string
type NodeState string

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

type NodeRole string

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

type MetricSample struct {
	Name   string
	Value  float64
	Labels map[string]string
}

type HealthProbeFunc func(ctx context.Context, client *Client, role NodeRole) error

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

func (r HealthReport) Healthy() bool {
	return r.Status == HealthStatusUp
}

func (c *Client) Name() string {
	return c.effectiveName("default")
}

func (c *Client) HealthCheck(ctx context.Context) HealthReport {
	return c.healthCheck(ctx, c.effectiveName("default"), RoleStandalone)
}

func (c *Client) StatsSnapshot() DBStatsSnapshot {
	return newDBStatsSnapshot(c.sqlDB.Stats())
}

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
