package ormx

import (
	"context"

	"gorm.io/gorm"
)

// Reader 返回一个读客户端，是 ReaderClient 忽略错误的便捷形式；
// 没有可读节点时返回 nil。
func (c *Cluster) Reader() *Client {
	client, _ := c.ReaderClient()
	return client
}

// ReaderClient 按轮询（round-robin）从 ready 状态的副本中选出一个读客户端；
// 没有可用副本且开启 readFallbackToPrimary 时回退到主库，
// 否则返回 errNoReadableNode。
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

// WriteClient 返回用于写操作的主库客户端；
// 集群已关闭或主库不可用（不存在或处于 down 状态）时返回错误。
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

// WithTx 在主库上执行事务函数 fn；主库不可用时返回错误。
func (c *Cluster) WithTx(ctx context.Context, fn func(tx *gorm.DB) error, txOpts ...TxOption) error {
	client, err := c.WriteClient()
	if err != nil {
		return err
	}
	return client.WithTx(ctx, nil, fn, txOpts...)
}

// WithReadTx 在按 ReaderClientCtx 路由选出的读节点上执行只读事务函数 fn；
// ctx 携带写标记时会路由到主库，没有可读节点时返回错误。
func (c *Cluster) WithReadTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	client, err := c.ReaderClientCtx(ctx)
	if err != nil {
		return err
	}
	return client.WithReadTx(ctx, fn)
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
