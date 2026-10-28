package paginator

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestGroupedCountPreservesAliasesAndArguments(t *testing.T) {
	t.Parallel()
	state := &scriptState{total: 2, rows: []widget{{ID: 1, Name: "first"}}}
	db := newScriptedDB(t, state)
	calls := 0
	query := db.Model(&widget{}).
		Select("LOWER(name) AS normalized, COUNT(*) AS n").
		Where("name <> ?", "excluded").Group("normalized").Having("n > ?", 2).
		Scopes(func(tx *gorm.DB) *gorm.DB { calls++; return tx })
	page, err := Paginate[widget](query, Params{PageSize: 1}, WithDefaultSort("normalized"))
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 2 || calls != 2 {
		t.Fatalf("total=%d scope calls=%d, want 2/2", page.TotalCount, calls)
	}
	queries, args := state.recorded()
	if len(queries) != 2 || !strings.Contains(queries[0], "FROM (SELECT LOWER(name) AS normalized, COUNT(*) AS n") {
		t.Fatalf("count must preserve grouped projection inside a derived table: %v", queries)
	}
	if !strings.Contains(queries[0], "HAVING n > ?") || len(args[0]) != 2 ||
		args[0][0] != driver.Value("excluded") || args[0][1] != driver.Value(int64(2)) {
		t.Fatalf("group filters or arguments lost: sql=%s args=%v", queries[0], args[0])
	}
	if query.Statement.SQL.Len() != 0 || query.Statement.Dest != nil {
		t.Fatal("input query was mutated")
	}
}

func TestGroupedCountRetainsScopeGuard(t *testing.T) {
	t.Parallel()
	state := &scriptState{total: 2}
	db := newScriptedDB(t, state)
	query := db.Model(&widget{}).Select("name, COUNT(*) AS n").Group("name").
		Scopes(func(tx *gorm.DB) *gorm.DB {
			return tx.Scopes(func(inner *gorm.DB) *gorm.DB { return inner.Select("name") })
		})
	_, err := Paginate[widget](query, Params{Sort: "name"})
	if !errors.Is(err, ErrDeferredPaginationClause) {
		t.Fatalf("projection guard not applied: %v", err)
	}
	if queries, _ := state.recorded(); len(queries) != 0 {
		t.Fatalf("unsafe grouped query reached database: %v", queries)
	}
}

func TestGroupedCountHonorsContext(t *testing.T) {
	t.Parallel()
	state := &scriptState{total: 2}
	db := newScriptedDB(t, state)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Paginate[widget](db.WithContext(ctx).Model(&widget{}).Group("name"),
		Params{Sort: "name"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("group count lost cancellation: %v", err)
	}
}

func TestGroupedCountDoesNotUseModelAsDestination(t *testing.T) {
	t.Parallel()
	state := &scriptState{total: 2, rows: []widget{{ID: 1, Name: "first"}}}
	db := newScriptedDB(t, state)
	model := &widget{ID: 999}
	_, err := Paginate[widget](db.Model(model).Select("name, COUNT(*) AS n").Group("name"), Params{Sort: "name"})
	if err != nil {
		t.Fatal(err)
	}
	_, args := state.recorded()
	if len(args) != 2 || len(args[0]) != 0 || model.ID != 999 {
		t.Fatalf("model was treated as a query destination or mutated: args=%v model=%+v", args, model)
	}
}
