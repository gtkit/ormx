package ormx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

var (
	errNilPrimaryClient   = errors.New("ormx: nil primary client")
	errNilReplicaClient   = errors.New("ormx: nil replica client")
	errReplicaNotFound    = errors.New("ormx: replica not found")
	errNoReadableNode     = errors.New("ormx: no readable node available")
	errPrimaryUnavailable = errors.New("ormx: primary unavailable")
	errDuplicateNodeName  = errors.New("ormx: duplicate node name")
	errClusterClosed      = errors.New("ormx: cluster is closed")
	errTopologyChanged    = errors.New("ormx: topology changed during operation, retry")
	errHealthLoopInterval = errors.New("ormx: health loop interval must be greater than zero")
)

type ClusterOption func(*clusterOptions)

type clusterOptions struct {
	readFallbackToPrimary bool
	autoRecoverReplicas   bool
	healthCheckTimeout    time.Duration
}

type Cluster struct {
	mu          sync.RWMutex
	primary     *managedNode
	replicas    []*managedNode
	readerIndex atomic.Uint64
	options     clusterOptions
	epoch       uint64 // incremented on every topology change; used to detect TOCTOU
	closed      bool
}

type managedNode struct {
	name      string
	role      NodeRole
	client    *Client
	state     NodeState
	lastError error
	updatedAt time.Time
}

type Node struct {
	name      string
	role      NodeRole
	client    *Client
	state     NodeState
	lastError error
	updatedAt time.Time
}

type ClusterHealthReport struct {
	Status    HealthStatus
	CheckedAt time.Time
	Nodes     []HealthReport
}

const metricSampleCountPerNode = 10

func (r ClusterHealthReport) Healthy() bool {
	return r.Status == HealthStatusUp
}

func OpenCluster(
	ctx context.Context,
	primary Config,
	replicas ...Config,
) (*Cluster, error) {
	return OpenClusterWithOptions(ctx, primary, replicas)
}

func OpenClusterWithOptions(
	ctx context.Context,
	primary Config,
	replicas []Config,
	opts ...ClusterOption,
) (_ *Cluster, err error) {
	primaryClient, err := primary.Open(ctx)
	if err != nil {
		return nil, err
	}

	opened := []*Client{primaryClient}
	defer func() {
		if err == nil {
			return
		}
		for _, client := range opened {
			_ = client.Close()
		}
	}()

	// Open replicas in parallel to reduce total startup latency.
	type result struct {
		index  int
		client *Client
		err    error
	}
	replicaClients := make([]*Client, len(replicas))
	results := make([]result, len(replicas))

	var wg sync.WaitGroup
	for i := range replicas {
		wg.Go(func() {
			client, openErr := replicas[i].Open(ctx)
			results[i] = result{index: i, client: client, err: openErr}
		})
	}
	wg.Wait()

	for _, r := range results {
		if r.client != nil {
			opened = append(opened, r.client)
		}
		if r.err != nil {
			err = r.err
			return nil, err
		}
		replicaClients[r.index] = r.client
	}

	return NewClusterWithOptions(primaryClient, replicaClients, opts...)
}

func NewCluster(primary *Client, replicas ...*Client) (*Cluster, error) {
	return NewClusterWithOptions(primary, replicas)
}

func NewClusterWithOptions(primary *Client, replicas []*Client, opts ...ClusterOption) (*Cluster, error) {
	if primary == nil {
		return nil, errNilPrimaryClient
	}

	options := defaultClusterOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	cluster := &Cluster{
		primary: newManagedNode(primary.effectiveName("primary"), RolePrimary, primary),
		options: options,
	}

	usedNames := map[string]struct{}{
		cluster.primary.name: {},
	}

	cluster.replicas = make([]*managedNode, 0, len(replicas))
	for i, replica := range replicas {
		if replica == nil {
			return nil, errNilReplicaClient
		}

		name := replica.effectiveName(replicaName(i))
		if _, exists := usedNames[name]; exists {
			return nil, fmt.Errorf("%w: %s", errDuplicateNodeName, name)
		}
		usedNames[name] = struct{}{}
		cluster.replicas = append(cluster.replicas, newManagedNode(name, RoleReplica, replica))
	}

	return cluster, nil
}

func WithReadFallbackToPrimary(enabled bool) ClusterOption {
	return func(options *clusterOptions) {
		options.readFallbackToPrimary = enabled
	}
}

func WithAutoRecoverReplicas(enabled bool) ClusterOption {
	return func(options *clusterOptions) {
		options.autoRecoverReplicas = enabled
	}
}

// WithHealthCheckTimeout sets the default timeout for health check pings.
// If the caller's context already has a shorter deadline, that takes precedence.
// Default: 5s.
func WithHealthCheckTimeout(timeout time.Duration) ClusterOption {
	return func(options *clusterOptions) {
		if timeout > 0 {
			options.healthCheckTimeout = timeout
		}
	}
}

func (c *Cluster) Primary() *Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == nil {
		return nil
	}
	return c.primary.client
}

func (c *Cluster) PrimaryNode() Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == nil {
		return Node{}
	}
	return c.primary.snapshot()
}

func (c *Cluster) ReplicaNodes() []Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshots(c.replicas)
}

func (c *Cluster) Nodes() []Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	nodes := make([]Node, 0, 1+len(c.replicas))
	if c.primary != nil {
		nodes = append(nodes, c.primary.snapshot())
	}
	nodes = append(nodes, snapshots(c.replicas)...)
	return nodes
}

func (c *Cluster) Reader() *Client {
	client, _ := c.ReaderClient()
	return client
}

func (c *Cluster) ReaderClient() (*Client, error) {
	return c.readerClient(false)
}

// ReaderClientCtx returns a read client, respecting the write flag in ctx.
// If [ContextWithWriteFlag] was called on ctx, reads are routed to the
// primary to guarantee read-after-write consistency.
func (c *Cluster) ReaderClientCtx(ctx context.Context) (*Client, error) {
	return c.readerClient(HasWriteFlag(ctx))
}

func (c *Cluster) readerClient(forcePrimary bool) (*Client, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, errClusterClosed
	}

	// Fast path: write flag set — route to primary for read-after-write consistency.
	if forcePrimary {
		if c.primary == nil || c.primary.state != NodeStateReady {
			c.mu.RUnlock()
			return nil, errPrimaryUnavailable
		}
		client := c.primary.client
		c.mu.RUnlock()
		return client, nil
	}

	candidates := c.readyReplicasLocked()
	readFallback := c.options.readFallbackToPrimary
	// Capture primary state inside lock to avoid data race.
	var primaryClient *Client
	if readFallback && c.primary != nil && c.primary.state == NodeStateReady {
		primaryClient = c.primary.client
	}
	c.mu.RUnlock()

	if len(candidates) > 0 {
		idx := c.readerIndex.Add(1) - 1
		return candidates[idx%uint64(len(candidates))].client, nil
	}
	if primaryClient != nil {
		return primaryClient, nil
	}
	return nil, errNoReadableNode
}

func (c *Cluster) WriteClient() (*Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, errClusterClosed
	}
	if c.primary == nil || c.primary.state == NodeStateDown {
		return nil, errPrimaryUnavailable
	}
	return c.primary.client, nil
}

// WriteDB returns the primary *gorm.DB for write operations.
//
// Deprecated: Use WriteClient() instead to handle errors explicitly.
// This method returns nil when the primary is unavailable, which may
// cause nil-pointer panics if the caller does not check.
func (c *Cluster) WriteDB() *gorm.DB {
	client, _ := c.WriteClient()
	if client == nil {
		return nil
	}
	return client.DB()
}

// ReadDB returns a replica *gorm.DB for read operations.
//
// Deprecated: Use ReaderClient() instead to handle errors explicitly.
// This method returns nil when no readable node is available, which may
// cause nil-pointer panics if the caller does not check.
func (c *Cluster) ReadDB() *gorm.DB {
	client, _ := c.ReaderClient()
	if client == nil {
		return nil
	}
	return client.DB()
}

// ReadDBCtx returns a *gorm.DB for reads, routing to primary when ctx
// carries a write flag set by [ContextWithWriteFlag].
//
// Deprecated: Use ReaderClientCtx() instead to handle errors explicitly.
func (c *Cluster) ReadDBCtx(ctx context.Context) *gorm.DB {
	client, _ := c.ReaderClientCtx(ctx)
	if client == nil {
		return nil
	}
	return client.DB()
}

// MustWriteDB returns the primary *gorm.DB or panics if unavailable.
// Use only when a nil-pointer panic is acceptable (e.g. startup wiring).
func (c *Cluster) MustWriteDB() *gorm.DB {
	client, err := c.WriteClient()
	if err != nil {
		panic(err)
	}
	return client.DB()
}

// MustReadDB returns a replica *gorm.DB or panics if unavailable.
// Use only when a nil-pointer panic is acceptable (e.g. startup wiring).
func (c *Cluster) MustReadDB() *gorm.DB {
	client, err := c.ReaderClient()
	if err != nil {
		panic(err)
	}
	return client.DB()
}

func (c *Cluster) WithTx(ctx context.Context, fn func(tx *gorm.DB) error, txOpts ...TxOption) error {
	client, err := c.WriteClient()
	if err != nil {
		return err
	}
	return client.WithTx(ctx, nil, fn, txOpts...)
}

func (c *Cluster) WithReadTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	client, err := c.ReaderClientCtx(ctx)
	if err != nil {
		return err
	}
	return client.WithReadTx(ctx, fn)
}

func (c *Cluster) HealthCheck(ctx context.Context) ClusterHealthReport {
	checkedAt := time.Now()
	ctx, cancel := c.healthCtx(ctx)
	defer cancel()
	nodes := c.Nodes()
	reports := probeNodes(ctx, nodes)
	return buildClusterReport(checkedAt, reports)
}

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

func (c *Cluster) DrainReplica(name string, cause error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errClusterClosed
	}

	replica := c.findReplicaLocked(name)
	if replica == nil {
		return errReplicaNotFound
	}
	replica.setState(NodeStateDraining, cause)
	return nil
}

func (c *Cluster) RecoverReplica(ctx context.Context, name string) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return errClusterClosed
	}
	replica := c.findReplicaLocked(name)
	if replica == nil {
		c.mu.RUnlock()
		return errReplicaNotFound
	}
	client := replica.client
	epochBefore := c.epoch
	c.mu.RUnlock()

	// Ping outside lock.
	pingErr := client.PingContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errClusterClosed
	}

	// Topology changed while pinging — the node may have been promoted or removed.
	if c.epoch != epochBefore {
		replica = c.findReplicaLocked(name)
		if replica == nil {
			return errReplicaNotFound
		}
	}

	if pingErr != nil {
		replica.setState(NodeStateDown, pingErr)
		return pingErr
	}

	replica.setState(NodeStateReady, nil)
	return nil
}

// MarkPrimaryDown 将当前主库标记为 down。
// 注意：如果后续调用 Refresh() 且主库 Ping 恢复成功，状态会自动回到 Ready。
// 如果你的目标是长期隔离主库，请在运维侧同时停止健康循环或避免继续触发 Refresh()。
func (c *Cluster) MarkPrimaryDown(cause error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errClusterClosed
	}

	if c.primary == nil {
		return errPrimaryUnavailable
	}
	c.primary.setState(NodeStateDown, cause)
	return nil
}

// SwitchPrimary 切换主库节点。
// 注意：如果后续调用 Refresh() 且主库 Ping 恢复成功，状态会自动回到 Ready。
// 如果你的目标是长期隔离主库，请在运维侧同时停止健康循环或避免继续触发 Refresh()。
//
// 注意：如果目标节点已经是主节点，则只做连通性确认；
// 如果目标节点是副本节点，则把它提升为主节点。
func (c *Cluster) SwitchPrimary(ctx context.Context, name string) (Node, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return Node{}, errClusterClosed
	}

	// Fast path: requested node is already the primary.
	if c.primary != nil && c.primary.name == name {
		c.mu.RUnlock()
		return c.switchPrimaryPingExisting(ctx, name)
	}

	// Slow path: promote a replica to primary.
	return c.switchPrimaryPromote(ctx, name)
}

// switchPrimaryPingExisting 处理 SwitchPrimary 的快路径：
// 当目标节点已经是主节点时，只做连通性确认。
// 调用方进入此函数前不能持有任何锁。
func (c *Cluster) switchPrimaryPingExisting(ctx context.Context, name string) (Node, error) {
	c.mu.RLock()
	client := c.primary.client
	epochBefore := c.epoch
	c.mu.RUnlock()

	if err := client.PingContext(ctx); err != nil {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return Node{}, errClusterClosed
		}
		if c.epoch == epochBefore && c.primary != nil && c.primary.name == name {
			c.primary.setState(NodeStateDown, err)
		}
		c.mu.Unlock()
		return Node{}, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return Node{}, errClusterClosed
	}
	if c.primary != nil && c.primary.name == name {
		return c.primary.snapshot(), nil
	}
	return Node{}, errTopologyChanged
}

// switchPrimaryPromote 处理 SwitchPrimary 的慢路径：
// 当目标节点还是副本时，需要把它提升为主节点。
//
// 注意：调用方进入此函数时必须已经持有 RLock，
// 且本函数会负责释放这把 RLock。
// 这里特意保留这种锁交接方式，是为了在锁外执行 Ping，
// 避免持锁做 I/O；后续维护这段代码时不要忽略这一点。
func (c *Cluster) switchPrimaryPromote(ctx context.Context, name string) (Node, error) {
	replica := c.findReplicaLocked(name)
	if replica == nil {
		c.mu.RUnlock()
		return Node{}, errReplicaNotFound
	}
	client := replica.client
	epochBefore := c.epoch
	c.mu.RUnlock()

	// Ping outside lock to avoid holding the mutex during I/O.
	if err := client.PingContext(ctx); err != nil {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return Node{}, errClusterClosed
		}
		if c.epoch == epochBefore {
			if r := c.findReplicaLocked(name); r != nil {
				r.setState(NodeStateDown, err)
			}
		}
		c.mu.Unlock()
		return Node{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Node{}, errClusterClosed
	}

	// Re-check: topology may have changed during ping.
	if c.epoch != epochBefore {
		if c.primary != nil && c.primary.name == name {
			return c.primary.snapshot(), nil
		}
		return Node{}, errTopologyChanged
	}

	replica = c.findReplicaLocked(name)
	if replica == nil {
		if c.primary != nil && c.primary.name == name {
			return c.primary.snapshot(), nil
		}
		return Node{}, errReplicaNotFound
	}

	return c.switchPrimaryLocked(replica, errors.New("primary routing switched")), nil
}

func (c *Cluster) Metrics() []MetricSample {
	nodes := c.Nodes()
	metrics := make([]MetricSample, 0, len(nodes)*metricSampleCountPerNode)
	for _, node := range nodes {
		metrics = append(metrics, node.Metrics()...)
	}
	return metrics
}

func (c *Cluster) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClusterClosed
	}
	c.closed = true

	nodes := make([]Node, 0, 1+len(c.replicas))
	if c.primary != nil {
		nodes = append(nodes, c.primary.snapshot())
	}
	nodes = append(nodes, snapshots(c.replicas)...)
	c.mu.Unlock()

	seen := make(map[*Client]struct{}, len(nodes))
	var errs []error
	for _, node := range nodes {
		if _, ok := seen[node.client]; ok {
			continue
		}
		seen[node.client] = struct{}{}
		if err := node.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (n Node) Name() string {
	return n.name
}

func (n Node) Role() NodeRole {
	return n.role
}

func (n Node) Client() *Client {
	return n.client
}

func (n Node) State() NodeState {
	return n.state
}

func (n Node) Healthy() bool {
	return n.state == NodeStateReady
}

func (n Node) LastError() error {
	return n.lastError
}

func (n Node) UpdatedAt() time.Time {
	return n.updatedAt
}

func (n Node) HealthCheck(ctx context.Context) HealthReport {
	return n.decorateHealthReport(n.client.healthCheck(ctx, n.name, n.role))
}

func (n Node) Metrics() []MetricSample {
	return n.client.metrics(n.name, n.role)
}

func defaultClusterOptions() clusterOptions {
	return clusterOptions{
		readFallbackToPrimary: true,
		autoRecoverReplicas:   true,
		healthCheckTimeout:    defaultHealthCheckTimeout,
	}
}

func newManagedNode(name string, role NodeRole, client *Client) *managedNode {
	now := time.Now()
	return &managedNode{
		name:      name,
		role:      role,
		client:    client,
		state:     NodeStateReady,
		updatedAt: now,
	}
}

func (n *managedNode) snapshot() Node {
	return Node{
		name:      n.name,
		role:      n.role,
		client:    n.client,
		state:     n.state,
		lastError: n.lastError,
		updatedAt: n.updatedAt,
	}
}

func (n *managedNode) setState(state NodeState, err error) {
	n.state = state
	n.lastError = err
	n.updatedAt = time.Now()
}

func (c *Cluster) allManagedNodesLocked() []*managedNode {
	nodes := make([]*managedNode, 0, 1+len(c.replicas))
	if c.primary != nil {
		nodes = append(nodes, c.primary)
	}
	nodes = append(nodes, c.replicas...)
	return nodes
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

func (c *Cluster) readyReplicasLocked() []*managedNode {
	ready := make([]*managedNode, 0, len(c.replicas))
	for _, replica := range c.replicas {
		if replica.state == NodeStateReady {
			ready = append(ready, replica)
		}
	}
	return ready
}

func (c *Cluster) findReplicaLocked(name string) *managedNode {
	for _, replica := range c.replicas {
		if replica.name == name {
			return replica
		}
	}
	return nil
}

func (c *Cluster) switchPrimaryLocked(candidate *managedNode, cause error) Node {
	oldPrimary := c.primary
	if oldPrimary != nil {
		oldPrimary.role = RoleReplica
		if oldPrimary.state == NodeStateReady {
			oldPrimary.setState(NodeStateDraining, cause)
		} else if cause != nil {
			oldPrimary.setState(oldPrimary.state, errors.Join(oldPrimary.lastError, cause))
		}
	}

	candidate.role = RolePrimary
	candidate.setState(NodeStateReady, nil)
	c.removeReplicaLocked(candidate.name)
	c.primary = candidate

	if oldPrimary != nil {
		c.replicas = append(c.replicas, oldPrimary)
	}

	c.epoch++ // Topology changed — invalidate in-flight TOCTOU checks.
	return candidate.snapshot()
}

func (c *Cluster) removeReplicaLocked(name string) {
	for i, replica := range c.replicas {
		if replica.name != name {
			continue
		}
		c.replicas = append(c.replicas[:i], c.replicas[i+1:]...)
		return
	}
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

func snapshots(nodes []*managedNode) []Node {
	snapshotNodes := make([]Node, len(nodes))
	for i, node := range nodes {
		snapshotNodes[i] = node.snapshot()
	}
	return snapshotNodes
}

func replicaName(index int) string {
	return "replica-" + strconv.Itoa(index+1)
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
