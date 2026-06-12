package ormx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
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

// ClusterOption 用于在构建 Cluster 时定制集群行为的函数式选项。
type ClusterOption func(*clusterOptions)

type clusterOptions struct {
	readFallbackToPrimary bool
	autoRecoverReplicas   bool
	healthCheckTimeout    time.Duration
}

// Cluster 管理一主多副本的数据库节点拓扑，提供读写分离路由、
// 健康检查、副本摘除/恢复与主库切换能力。
// 所有方法并发安全（内部由 RWMutex 保护）。
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

// Node 是集群节点在某一时刻的不可变快照，
// 包含节点名称、角色、客户端、状态、最近错误及状态更新时间。
type Node struct {
	name      string
	role      NodeRole
	client    *Client
	state     NodeState
	lastError error
	updatedAt time.Time
}

// ClusterHealthReport 描述一次集群健康检查的整体结果，
// 包含集群级状态、检查时间和各节点的健康报告。
type ClusterHealthReport struct {
	Status    HealthStatus
	CheckedAt time.Time
	Nodes     []HealthReport
}

const metricSampleCountPerNode = 10

// Healthy 报告集群整体状态是否为 HealthStatusUp。
func (r ClusterHealthReport) Healthy() bool {
	return r.Status == HealthStatusUp
}

// OpenCluster 按给定配置打开主库与各副本连接，并构建 Cluster。
// 等价于使用默认选项调用 OpenClusterWithOptions。
func OpenCluster(
	ctx context.Context,
	primary Config,
	replicas ...Config,
) (*Cluster, error) {
	return OpenClusterWithOptions(ctx, primary, replicas)
}

// OpenClusterWithOptions 按给定配置打开主库连接，并行打开所有副本连接，
// 再按选项构建 Cluster。任一连接打开失败时会关闭已打开的连接并返回错误。
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

// NewCluster 用已有的主库与副本客户端构建 Cluster。
// 等价于使用默认选项调用 NewClusterWithOptions。
func NewCluster(primary *Client, replicas ...*Client) (*Cluster, error) {
	return NewClusterWithOptions(primary, replicas)
}

// NewClusterWithOptions 用已有客户端与选项构建 Cluster。
// primary 不能为 nil；副本不能为 nil 且节点名不能重复，否则返回错误。
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

// WithReadFallbackToPrimary 设置当没有可用副本时读请求是否回退到主库。
// 默认开启。
func WithReadFallbackToPrimary(enabled bool) ClusterOption {
	return func(options *clusterOptions) {
		options.readFallbackToPrimary = enabled
	}
}

// WithAutoRecoverReplicas 设置 Refresh 探活成功时是否自动把 down 状态的副本恢复为 ready。
// 默认开启。
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

// Primary 返回当前主库客户端；主库不存在时返回 nil。
func (c *Cluster) Primary() *Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == nil {
		return nil
	}
	return c.primary.client
}

// PrimaryNode 返回当前主库节点的快照；主库不存在时返回零值 Node。
func (c *Cluster) PrimaryNode() Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == nil {
		return Node{}
	}
	return c.primary.snapshot()
}

// ReplicaNodes 返回所有副本节点的快照。
func (c *Cluster) ReplicaNodes() []Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return snapshots(c.replicas)
}

// Nodes 返回主库与所有副本节点的快照，主库（若存在）排在首位。
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

// Close 关闭集群并释放所有节点的底层连接（同一客户端只关闭一次），
// 返回各节点关闭错误的合并结果；重复调用返回 errClusterClosed。
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

// Name 返回节点名称。
func (n Node) Name() string {
	return n.name
}

// Role 返回节点角色（主库或副本）。
func (n Node) Role() NodeRole {
	return n.role
}

// Client 返回节点对应的数据库客户端。
func (n Node) Client() *Client {
	return n.client
}

// State 返回快照时刻的节点状态。
func (n Node) State() NodeState {
	return n.state
}

// Healthy 报告快照时刻节点状态是否为 NodeStateReady。
func (n Node) Healthy() bool {
	return n.state == NodeStateReady
}

// LastError 返回节点最近一次记录的错误；无错误时为 nil。
func (n Node) LastError() error {
	return n.lastError
}

// UpdatedAt 返回节点状态最近一次更新的时间。
func (n Node) UpdatedAt() time.Time {
	return n.updatedAt
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
