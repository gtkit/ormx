.PHONY: tool check tag release-patch release-minor gittag delcommit


LINT_TARGETS ?= ./...

# 发版语义级别：patch（默认）/ minor。major 被本项目策略拒绝。
BUMP ?= patch
tool: ## Lint Go code with the installed golangci-lint
	@ echo "▶️ golangci-lint run"
	golangci-lint run $(LINT_TARGETS)
	gofumpt -l -w .
	@ echo "✅ golangci-lint run"

## govulncheck 检查漏洞 go install golang.org/x/vuln/cmd/govulncheck@latest
check:
	govulncheck ./...
	gosec ./...
tag:
	@set -e; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "✗ 工作区不干净，发版前请先提交或清理："; git status --short; exit 1; \
	fi; \
	echo "▶️ go vet"; go vet ./...; \
	echo "▶️ golangci-lint"; golangci-lint run $(LINT_TARGETS); \
	echo "▶️ 测试 (race)"; go test -race -count=1 -timeout=5m ./...; \
	echo "▶️ 覆盖率 ≥ 80%"; \
	go test -coverprofile=coverage.out ./... >/dev/null; \
	cov=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	rm -f coverage.out; \
	awk -v c="$$cov" 'BEGIN { if (c+0 < 80) { printf "✗ 覆盖率 %.1f%% < 80%%\n", c+0; exit 1 } printf "✓ 覆盖率 %.1f%%\n", c+0 }'; \
	echo "▶️ benchmark"; go test -bench=. -benchmem -count=3 -run='^$$' ./... >/dev/null; \
	echo "▶️ govulncheck"; govulncheck ./...; \
	current=$$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' version.go | head -n1 | tr -d 'v'); \
	if [ -z "$$current" ]; then echo "version not found in version.go"; exit 1; fi; \
	maj=$$(echo $$current | cut -d. -f1); \
	min=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	case "$(BUMP)" in \
	  patch) new="v$$maj.$$min.$$((patch+1))" ;; \
	  minor) new="v$$maj.$$((min+1)).0" ;; \
	  major) echo "✗ 本项目只做 v1、不发 v2；且 MAJOR 还需 /v2 module path 重构（仅 bump tag 是错误发布），已拒绝"; exit 1 ;; \
	  *) echo "✗ BUMP 必须为 patch 或 minor（当前: $(BUMP)）"; exit 1 ;; \
	esac; \
	if ! grep -qE "^## \[$$new\] - [0-9]{4}-[0-9]{2}-[0-9]{2}" CHANGELOG.md; then \
		echo "✗ CHANGELOG.md 缺少版本条目：## [$$new] - YYYY-MM-DD，发版前请先补齐"; exit 1; \
	fi; \
	notes=$$(awk -v h="## [$$new] -" 'index($$0, h) == 1 { f = 1; print; next } f && /^## / { exit } f { print }' CHANGELOG.md); \
	printf "Bump (%s): v%s -> %s\n" "$(BUMP)" "$$current" "$$new"; \
	sed -E -i.bak 's/(const Version = ")([^"]+)(")/\1'"$$new"'\3/' version.go; \
	rm -f version.go.bak; \
	git add version.go; \
	git commit -m "chore(release): $$new"; \
	git tag -a "$$new" -m "$$notes"; \
	git push gtkit HEAD; \
	git push gtkit "$$new"; \
	printf "Done: %s\n" "$$new"

release-patch: ## 发布 PATCH 版本（bug 修复 / 文档 / 内部重构）
	@$(MAKE) tag BUMP=patch

release-minor: ## 发布 MINOR 版本（新增功能，或按本项目策略承载破坏性变更）
	@$(MAKE) tag BUMP=minor

gittag:
	git tag --sort=-version:refname | head -1

## 删除最近一次提交，但保留修改内容
delcommit:
	git reset --soft HEAD~1
