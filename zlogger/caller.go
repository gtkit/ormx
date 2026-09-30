package zlogger

import (
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// 需要跳过的调用方前缀由反射得到，不硬编码模块路径：
// ormxModule 形如 "github.com/gtkit/ormx"（根包函数名为 ormxModule+"."，子包为 ormxModule+"/"），
// gormHost 形如 "gorm.io/"（覆盖 gorm、方言与插件）。
var (
	ormxModule = func() string {
		pkg := reflect.TypeFor[gormLogger]().PkgPath()
		return pkg[:strings.LastIndexByte(pkg, '/')]
	}()
	gormHost = func() string {
		pkg := reflect.TypeFor[gorm.DB]().PkgPath()
		return pkg[:strings.IndexByte(pkg, '/')+1]
	}()
)

// callerSource 返回 GORM、ormx、gorm/gen 生成代码与 Go runtime 之外第一个调用方的 "file:line"，
// 全部为内部帧时返回空串。
// 模块归属按函数全名前缀判定而非文件路径，-trimpath、模块缓存与 replace 到本地目录下行为一致；
// *.gen.go 帧跳过、_test.go 帧视为调用方，与 GORM 的 FileWithLineNum 规则一致；
// 跳过 runtime 帧是为了在 panic 展开中执行的回滚（如嵌套事务的 ROLLBACK TO SAVEPOINT）定位到业务的 panic 处。
func callerSource() string {
	const (
		maxDepth = 32
		// 跳过 runtime.Callers 自身与本函数两帧；zlogger 的方法帧由前缀判定过滤，不依赖具体深度。
		skipSelf = 2
	)
	var pcs [maxDepth]uintptr
	n := runtime.Callers(skipSelf, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.PC != 0 && !internalFrame(frame) {
			return frame.File + ":" + strconv.Itoa(frame.Line)
		}
		if !more {
			return ""
		}
	}
}

func internalFrame(frame runtime.Frame) bool {
	switch {
	case strings.HasSuffix(frame.File, ".gen.go"):
		return true
	case strings.HasSuffix(frame.File, "_test.go"):
		return false
	}
	fn := frame.Function
	return strings.HasPrefix(fn, gormHost) ||
		strings.HasPrefix(fn, ormxModule+".") ||
		strings.HasPrefix(fn, ormxModule+"/") ||
		strings.HasPrefix(fn, "runtime.")
}
