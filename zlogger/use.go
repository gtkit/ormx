package zlogger

import (
	"go.uber.org/zap"

	"github.com/gtkit/ormx"
)

// Use 返回把 zap logger 接入 ormx 的 Option，等价于
// ormx.WithGormLogger(New(WithLogger(zl), opts...))：zl 为 nil 时回退为 no-op（静默丢弃），
// opts 在 logger 注入之后按序应用。需要注入自定义 gormlogger.Interface 实现时改用 ormx.WithGormLogger。
// 本子包依赖根包而非相反，只引用根包的项目因此不携带 zap。
func Use(zl *zap.Logger, opts ...Option) ormx.Option {
	return ormx.WithGormLogger(New(append([]Option{WithLogger(zl)}, opts...)...))
}
