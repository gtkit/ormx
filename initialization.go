package ormx

import (
	"context"
	"database/sql"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// initializationPool 只在 gorm.Open 期间使用：版本查询继承启动 ctx，且不实现
// GetDBConnector，防止 GORM 初始化失败时取得并关闭借用的 *sql.DB。
type initializationPool struct {
	gorm.ConnPool
	ctx context.Context
}

//nolint:contextcheck // 此处特意将 GORM 的 Background 替换为调用方启动 ctx；取消与 deadline 有反证测试。
func (p *initializationPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if p.ctx != nil {
		ctx = p.ctx
	}
	return p.ConnPool.QueryRowContext(ctx, query, args...)
}

func openGORM(
	ctx context.Context, sqlDB *sql.DB, dialectConfig gormmysql.Config, config *gorm.Config,
) (*gorm.DB, error) {
	pool := &initializationPool{ConnPool: sqlDB, ctx: ctx}
	dialectConfig.Conn = pool
	dialector := gormmysql.New(dialectConfig)
	defer func() {
		pool.ctx = nil
		if mysqlDialector, ok := dialector.(*gormmysql.Dialector); ok {
			mysqlDialector.Conn = sqlDB
		}
	}()

	db, err := gorm.Open(dialector, config)
	if err != nil {
		return nil, err
	}

	// 在返回给调用方之前移除启动适配，保留运行期的事务、DB() 与预编译缓存语义。
	if prepared, ok := db.ConnPool.(*gorm.PreparedStmtDB); ok {
		prepared.ConnPool = sqlDB
	} else {
		db.ConnPool = sqlDB
	}
	db.Statement.ConnPool = db.ConnPool
	return db, nil
}
