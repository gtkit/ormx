package ormx

import (
	"context"
	"sync"
	"time"
)

// HealthCheck 并行探活所有节点并返回集群健康报告；
// 该方法只读，不修改任何节点状态。
func (c *Cluster) HealthCheck(ctx context.Context) ClusterHealthReport {
	checkedAt := time.Now()
	ctx, cancel := c.healthCtx(ctx)
	defer cancel()
	nodes := c.Nodes()
	reports := probeNodes(ctx, nodes)
	return buildClusterReport(checkedAt, reports)
}

// Refresh 并行探活所有节点并据此更新节点状态：
// 探活失败的节点置为 down；主库探活成功时恢复为 ready；
// 开启 autoRecoverReplicas 时，down 状态的副本探活成功后自动恢复为 ready。
// 返回更新后的集群健康报告。
func (c *Cluster) Refresh(ctx context.Context) ClusterHealthReport {
	checkedAt := time.Now()
	ctx, cancel := c.healthCtx(ctx)
	defer cancel()
	nodes := c.Nodes()
	probed := probeNodesByName(ctx, nodes)

	c.mu.Lock()
	for _, node := range c.allManagedNodesLocked() {
		report, ok := probed[node.name]
		if !ok {
			continue
		}

		switch {
		case report.Error != nil:
			node.setState(NodeStateDown, report.Error)
		case node.role == RolePrimary:
			node.setState(NodeStateReady, nil)
		case node.state == NodeStateDown && c.options.autoRecoverReplicas:
			node.setState(NodeStateReady, nil)
		}
	}

	finalReports := c.currentReportsLocked(probed)
	c.mu.Unlock()

	return buildClusterReport(checkedAt, finalReports)
}

// RunHealthLoop periodically refreshes cluster health until ctx is canceled.
// Callers should start it in their own goroutine to control shutdown semantics.
func (c *Cluster) RunHealthLoop(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errHealthLoopInterval
	}

	ctx = normalizeContext(ctx)
	if ctx.Err() != nil {
		return nil
	}

	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return errClusterClosed
	}

	c.Refresh(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.mu.RLock()
			closed = c.closed
			c.mu.RUnlock()
			if closed {
				return errClusterClosed
			}
			c.Refresh(ctx)
		}
	}
}

// Metrics 汇总并返回所有节点的连接池指标采样。
func (c *Cluster) Metrics() []MetricSample {
	nodes := c.Nodes()
	metrics := make([]MetricSample, 0, len(nodes)*metricSampleCountPerNode)
	for _, node := range nodes {
		metrics = append(metrics, node.Metrics()...)
	}
	return metrics
}

// HealthCheck 对该节点执行一次探活，并结合快照中的节点状态修饰健康报告后返回。
func (n Node) HealthCheck(ctx context.Context) HealthReport {
	return n.decorateHealthReport(n.client.healthCheck(ctx, n.name, n.role))
}

// Metrics 返回该节点的连接池指标采样。
func (n Node) Metrics() []MetricSample {
	return n.client.metrics(n.name, n.role)
}

func (c *Cluster) currentReportsLocked(probed map[string]HealthReport) []HealthReport {
	nodes := make([]Node, 0, 1+len(c.replicas))
	if c.primary != nil {
		nodes = append(nodes, c.primary.snapshot())
	}
	nodes = append(nodes, snapshots(c.replicas)...)

	reports := make([]HealthReport, 0, len(nodes))
	for _, node := range nodes {
		report, ok := probed[node.name]
		if !ok {
			report = HealthReport{
				Name:      node.name,
				Role:      node.role,
				State:     node.state,
				Status:    stateToHealthStatus(node.state), // 按 state 推断初始 Status,
				CheckedAt: time.Now(),
				Error:     node.lastError,
			}
		}
		reports = append(reports, node.decorateHealthReport(report))
	}
	return reports
}

func (n Node) decorateHealthReport(report HealthReport) HealthReport {
	report.Name = n.name
	report.Role = n.role
	report.State = n.state

	switch n.state {
	case NodeStateReady:
	case NodeStateDraining:
		if report.Status == HealthStatusUp {
			report.Status = HealthStatusDegraded
		}
		if report.Error == nil {
			report.Error = n.lastError
		}
	case NodeStateDown:
		report.Status = HealthStatusDown
		if report.Error == nil {
			report.Error = n.lastError
		}
	}

	return report
}

func probeNodes(ctx context.Context, nodes []Node) []HealthReport {
	probed := make([]HealthReport, len(nodes))
	var wg sync.WaitGroup
	for i := range nodes {
		wg.Go(func() {
			probed[i] = nodes[i].HealthCheck(ctx)
		})
	}
	wg.Wait()
	return probed
}

func probeNodesByName(ctx context.Context, nodes []Node) map[string]HealthReport {
	reports := probeNodes(ctx, nodes)
	indexed := make(map[string]HealthReport, len(reports))
	for _, report := range reports {
		indexed[report.Name] = report
	}
	return indexed
}

func buildClusterReport(checkedAt time.Time, reports []HealthReport) ClusterHealthReport {
	report := ClusterHealthReport{
		Status:    HealthStatusUp,
		CheckedAt: checkedAt,
		Nodes:     reports,
	}

	for _, node := range reports {
		if node.Role == RolePrimary && node.Status != HealthStatusUp {
			report.Status = HealthStatusDown
			return report
		}
		if node.Status != HealthStatusUp {
			report.Status = HealthStatusDegraded
		}
	}

	return report
}

// healthCtx applies the cluster's configured health check timeout if the
// caller's context does not already carry a shorter deadline.
// The returned cancel function must be called when the context is no longer needed.
func (c *Cluster) healthCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	c.mu.RLock()
	timeout := c.options.healthCheckTimeout
	c.mu.RUnlock()

	ctx = normalizeContext(ctx)

	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {} // caller already set a deadline
	}
	return context.WithTimeout(ctx, timeout)
}

func stateToHealthStatus(state NodeState) HealthStatus {
	switch state {
	case NodeStateReady:
		return HealthStatusUp
	case NodeStateDraining:
		return HealthStatusDegraded
	case NodeStateDown:
		return HealthStatusDown
	default:
		return HealthStatusDown
	}
}
