package ormx

import (
	"context"
	"errors"
)

// DrainReplica 把指定名称的副本置为 draining 状态，将其从读流量中摘除；
// cause 记录为该节点的最近错误。副本不存在或集群已关闭时返回错误。
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

// RecoverReplica 对指定名称的副本执行 Ping 探活：
// 成功则恢复为 ready 重新接收读流量，失败则置为 down 并返回探活错误。
// 副本不存在或集群已关闭时返回错误。
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
