.PHONY: tool check tag gittag

LINT_TARGETS ?= ./...

tool: ## Lint Go code with the installed golangci-lint
	@ echo "▶️  golangci-lint run"
	golangci-lint run $(LINT_TARGETS)
	gofmt -l -w .
	@ echo "✅ golangci-lint done"

## govulncheck 检查漏洞 go install golang.org/x/vuln/cmd/govulncheck@latest
check:
	govulncheck ./...

## ────────────────────────────────────────────────────────
## 发版: make tag [BUMP=patch|minor|major]
## 读取 version.go 中的版本号，按 BUMP 递增（默认 patch），打 tag 并推送。
## Tag 格式: vX.Y.Z（单模块，无子目录前缀）
## 发版前提：工作区干净；CHANGELOG.md 已有 "## [vX.Y.Z] - YYYY-MM-DD" 条目。
## Tag message 自动携带该版本的 CHANGELOG 区段内容。
## ────────────────────────────────────────────────────────
BUMP ?= patch

tag:
	@set -e; \
	if [ -n "$$(git status --porcelain)" ]; then echo "❌ 工作区不干净，先提交或还原后再发版"; exit 1; fi; \
	current=$$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' version.go | head -n1 | tr -d 'v'); \
	if [ -z "$$current" ]; then echo "❌ version not found in version.go"; exit 1; fi; \
	maj=$$(echo $$current | cut -d. -f1); \
	min=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	case "$(BUMP)" in \
	  major) maj=$$((maj + 1)); min=0; patch=0 ;; \
	  minor) min=$$((min + 1)); patch=0 ;; \
	  patch) patch=$$((patch + 1)) ;; \
	  *) echo "❌ BUMP 只支持 patch|minor|major（当前: $(BUMP)）"; exit 1 ;; \
	esac; \
	new="v$$maj.$$min.$$patch"; \
	printf "Bump(%s): v%s → %s\n" "$(BUMP)" "$$current" "$$new"; \
	if ! grep -qE "^## \[$$new\] - [0-9]{4}-[0-9]{2}-[0-9]{2}" CHANGELOG.md; then echo "❌ CHANGELOG.md 缺少 '## [$$new] - YYYY-MM-DD' 条目"; exit 1; fi; \
	go vet ./...; \
	golangci-lint run ./...; \
	go test -race -count=1 -timeout=5m ./...; \
	go test -bench=. -benchmem -count=3 -run '^$$' ./...; \
	go test -coverprofile=coverage.out ./...; \
	total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub(/%/, "", $$3); print $$3}'); \
	rm -f coverage.out; \
	awk -v t="$$total" 'BEGIN { exit (t + 0 >= 80) ? 0 : 1 }' || { echo "❌ coverage $$total% 低于 80%"; exit 1; }; \
	govulncheck ./...; \
	sed -E -i.bak 's/(const Version = ")([^"]+)(")/\1'"$$new"'\3/' version.go && rm -f version.go.bak; \
	git add version.go; \
	git commit -m "chore(release): $$new"; \
	notes=$$(awk -v ver="$$new" '$$0 ~ "^## \\[" ver "\\]" {flag=1; next} /^## /{flag=0} flag' CHANGELOG.md); \
	git tag -a "$$new" -m "$$(printf '版本 %s\n\n主要变更：\n%s\n' "$$new" "$$notes")"; \
	git push gtkit HEAD; \
	git push gtkit "$$new"; \
	printf "✅ released: %s\n" "$$new"

## 查看最新 tag
gittag:
	@git tag --sort=-v:refname | head -n1
