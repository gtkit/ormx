.PHONY: tool fmt check test-integration tag release-patch release-minor gittag delcommit


LINT_TARGETS ?= ./...

# 发版语义级别：patch（默认）/ minor。当前脚本仅管理 v1 的 PATCH/MINOR 发布；
# 破坏性变更需建立 /v2 模块，不由本脚本处理。
BUMP ?= patch

# 真实 MySQL 集成测试（发布前置条件）：fake driver 只能验 SQL 形状，
# DISTINCT / ONLY_FULL_GROUP_BY / 保留字 / Join 同名列必须由真实数据库判定。
# DSN 允许覆盖（不同机器不同实例）；运行开关在 tag 门禁里硬编码为 1，
# 防止 shell 里 export ORM_RUN_INTEGRATION=0 让集成测试被静默跳过却照常打 tag。
ORM_TEST_DSN ?= root:@tcp(127.0.0.1:3306)/

tool: ## 只读静态检查（不修改代码，格式化用 make fmt）
	@ echo "▶️ golangci-lint run"
	golangci-lint run $(LINT_TARGETS)
	@ unformatted=$$(gofumpt -l .); \
	if [ -n "$$unformatted" ]; then \
	  echo "✗ 以下文件未按 gofumpt 格式化（运行 make fmt 修复）:"; echo "$$unformatted"; exit 1; \
	fi
	@ echo "✅ golangci-lint run"

fmt: ## 按 gofumpt 格式化代码（唯一允许写文件的格式化入口）
	gofumpt -l -w .

test-integration: ## 跑真实 MySQL 集成测试（需可连接的 MySQL；缺条件即失败，不静默跳过）
	ORM_RUN_INTEGRATION=1 ORM_REQUIRE_INTEGRATION=1 ORM_TEST_DSN='$(ORM_TEST_DSN)' \
	  go test -tags integration -race -count=1 -timeout=5m ./...

## govulncheck 检查漏洞 go install golang.org/x/vuln/cmd/govulncheck@latest
check:
	govulncheck ./...
	gosec ./...
tag:
	@set -e; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "✗ 工作区不干净，发版前请先提交或清理："; git status --short; exit 1; \
	fi; \
	echo "▶️ go mod tidy -diff"; go mod tidy -diff; \
	echo "▶️ go vet"; go vet ./...; \
	echo "▶️ lint（含 gofumpt 只读检查）"; $(MAKE) tool; \
	echo "▶️ 测试 (race)"; go test -race -count=1 -timeout=5m ./...; \
	echo "▶️ 集成测试（真实 MySQL，发布前置条件；缺条件即失败）"; \
	ORM_RUN_INTEGRATION=1 ORM_REQUIRE_INTEGRATION=1 ORM_TEST_DSN='$(ORM_TEST_DSN)' \
	  go test -tags integration -race -count=1 -timeout=5m ./...; \
	echo "▶️ 覆盖率 ≥ 80%"; \
	go test -coverprofile=coverage.out ./... >/dev/null; \
	cov=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	rm -f coverage.out; \
	awk -v c="$$cov" 'BEGIN { if (c+0 < 80) { printf "✗ 覆盖率 %.1f%% < 80%%\n", c+0; exit 1 } printf "✓ 覆盖率 %.1f%%\n", c+0 }'; \
	echo "▶️ benchmark"; go test -bench=. -benchmem -count=3 -run='^$$' ./... >/dev/null; \
	echo "▶️ govulncheck"; govulncheck ./...; \
	echo "▶️ gosec"; gosec -quiet ./...; \
	current=$$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' version.go | head -n1 | tr -d 'v'); \
	if [ -z "$$current" ]; then echo "version not found in version.go"; exit 1; fi; \
	maj=$$(echo $$current | cut -d. -f1); \
	min=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	case "$(BUMP)" in \
	  patch) new="v$$maj.$$min.$$((patch+1))" ;; \
	  minor) new="v$$maj.$$((min+1)).0" ;; \
	  major) echo "✗ MAJOR 需建立 /v2 模块（module path 加 /v2），仅 bump tag 是错误发布，不由本脚本处理，已拒绝"; exit 1 ;; \
	  *) echo "✗ BUMP 必须为 patch 或 minor（当前: $(BUMP)）"; exit 1 ;; \
	esac; \
	if ! grep -qE "^## \[$$new\] - [0-9]{4}-[0-9]{2}-[0-9]{2}" CHANGELOG.md; then \
		echo "✗ CHANGELOG.md 缺少版本条目：## [$$new] - YYYY-MM-DD，发版前请先补齐"; exit 1; \
	fi; \
	notes=$$(awk -v h="## [$$new] -" 'index($$0, h) == 1 { f = 1; next } f && /^## / { exit } f && /^### / { next } f { print }' CHANGELOG.md | sed -e '/./,$$!d'); \
	printf "Bump (%s): v%s -> %s\n" "$(BUMP)" "$$current" "$$new"; \
	sed -E -i.bak 's/(const Version = ")([^"]+)(")/\1'"$$new"'\3/' version.go; \
	rm -f version.go.bak; \
	git add version.go; \
	git commit -m "chore(release): 发布 $$new"; \
	git tag -a "$$new" -m "$$(printf '版本 %s\n\n主要变更：\n%s\n' "$$new" "$$notes")"; \
	git push gtkit HEAD; \
	git push gtkit "$$new"; \
	printf "Done: %s\n" "$$new"

release-patch: ## 发布 PATCH 版本（bug 修复 / 文档 / 内部重构）
	@$(MAKE) tag BUMP=patch

release-minor: ## 发布 MINOR 版本（向后兼容的新功能）
	@$(MAKE) tag BUMP=minor

gittag:
	git tag --sort=-version:refname | head -1

## 删除最近一次提交，但保留修改内容
delcommit:
	git reset --soft HEAD~1
