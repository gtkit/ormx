package zlogger

import (
	"runtime"
	"testing"
)

// 反射推导的前缀必须恰为模块根路径与 GORM 的域名段；推导逻辑出错会让 internalFrame 整体失准。
func TestDerivedFramePrefixes(t *testing.T) {
	t.Parallel()
	if ormxModule != "github.com/gtkit/ormx" {
		t.Fatalf("ormxModule = %q", ormxModule)
	}
	if gormHost != "gorm.io/" {
		t.Fatalf("gormHost = %q", gormHost)
	}
}

// 每条判定规则至少有一个用例在该规则被删除或放宽时失败。
func TestInternalFrame(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		frame runtime.Frame
		want  bool
	}{
		{
			name:  "gorm core",
			frame: runtime.Frame{Function: "gorm.io/gorm.(*DB).Find", File: "/mod/gorm.io/gorm@v1.31.2/finisher_api.go"},
			want:  true,
		},
		{
			name:  "gorm dialect",
			frame: runtime.Frame{Function: "gorm.io/driver/mysql.Dialector.Explain", File: "/mod/gorm.io/driver/mysql@v1.6.0/mysql.go"},
			want:  true,
		},
		{
			name:  "gorm plugin",
			frame: runtime.Frame{Function: "gorm.io/plugin/dbresolver.(*DBResolver).resolve", File: "dbresolver.go"},
			want:  true,
		},
		{
			name:  "ormx root package closure",
			frame: runtime.Frame{Function: "github.com/gtkit/ormx.(*Client).WithTx.func1", File: "/src/ormx/tx.go"},
			want:  true,
		},
		{
			name:  "ormx subpackage generic",
			frame: runtime.Frame{Function: "github.com/gtkit/ormx/paginator.Paginate[go.shape.struct {}]", File: "/src/ormx/paginator/paginator.go"},
			want:  true,
		},
		{
			name:  "zlogger itself",
			frame: runtime.Frame{Function: "github.com/gtkit/ormx/zlogger.(*gormLogger).Trace", File: "/src/ormx/zlogger/zaplog.go"},
			want:  true,
		},
		{
			name:  "go runtime during panic",
			frame: runtime.Frame{Function: "runtime.gopanic", File: "/go/src/runtime/panic.go"},
			want:  true,
		},
		{
			name:  "go runtime goroutine entry",
			frame: runtime.Frame{Function: "runtime.goexit", File: "/go/src/runtime/asm_arm64.s"},
			want:  true,
		},
		{
			name:  "gorm gen generated file in business module",
			frame: runtime.Frame{Function: "example.com/app/dal/query.userDo.Find", File: "/app/dal/query/users.gen.go"},
			want:  true,
		},
		{
			name:  "test file inside ormx package",
			frame: runtime.Frame{Function: "github.com/gtkit/ormx/paginator.TestPaginate", File: "/src/ormx/paginator/paginator_test.go"},
			want:  false,
		},
		{
			name:  "business module sharing ormx path prefix",
			frame: runtime.Frame{Function: "github.com/gtkit/ormxtra/biz.List", File: "/src/ormxtra/biz/biz.go"},
			want:  false,
		},
		{
			name:  "business package named like runtime",
			frame: runtime.Frame{Function: "runtimeutil.Probe", File: "/src/runtimeutil/probe.go"},
			want:  false,
		},
		{
			name:  "business closure",
			frame: runtime.Frame{Function: "example.com/app/dao.(*Repo).List.func1", File: "/app/dao/repo.go"},
			want:  false,
		},
		{
			name:  "main package",
			frame: runtime.Frame{Function: "main.main", File: "/app/main.go"},
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := internalFrame(tt.frame); got != tt.want {
				t.Fatalf("internalFrame(%q, %q) = %v, want %v", tt.frame.Function, tt.frame.File, got, tt.want)
			}
		})
	}
}
