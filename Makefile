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
## 发版: make tag
## 读取 version.go 中的版本号，自动 bump patch，打 tag 并推送。
## Tag 格式: vX.Y.Z（单模块，无子目录前缀）
## 发版前提：工作区必须干净（git status 无未提交文件）。
## ────────────────────────────────────────────────────────
tag:
	@if [ -n "$$(git status --porcelain)" ]; then echo "❌ 工作区不干净，先提交或还原后再发版"; exit 1; fi; \
	current=$$(grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' version.go | head -n1 | tr -d 'v'); \
	if [ -z "$$current" ]; then echo "❌ version not found in version.go"; exit 1; fi; \
	maj=$$(echo $$current | cut -d. -f1); \
	min=$$(echo $$current | cut -d. -f2); \
	patch=$$(echo $$current | cut -d. -f3); \
	newpatch=$$(expr $$patch + 1); \
	new="v$$maj.$$min.$$newpatch"; \
	printf "Bump: v%s → %s\n" "$$current" "$$new"; \
	if ! grep -q "^## \\[$$new\\]" CHANGELOG.md; then echo "❌ CHANGELOG.md 缺少 $$new 条目"; exit 1; fi; \
	go vet ./...; \
	golangci-lint run ./...; \
	go test -race -count=1 -timeout=5m ./...; \
	go test -coverprofile=coverage.out ./...; \
	total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub(/%/, "", $$3); print $$3}'); \
	rm -f coverage.out; \
	awk -v t="$$total" 'BEGIN { exit (t + 0 >= 80) ? 0 : 1 }' || { echo "❌ coverage $$total% 低于 80%"; exit 1; }; \
	govulncheck ./...; \
	sed -E -i.bak 's/(const Version = ")([^"]+)(")/\1'"$$new"'\3/' version.go && rm -f version.go.bak; \
	git add version.go; \
	git commit -m "chore(release): $$new"; \
	git tag -a "$$new" -m "$$(printf '版本 %s\n\n主要变更：\n- chore: 发布 %s\n\n破坏性变更（如有）：\n- 无\n' "$$new" "$$new")"; \
	git push gtkit HEAD; \
	git push gtkit "$$new"; \
	printf "✅ released: %s\n" "$$new"

## 查看最新 tag
gittag:
	@git tag --sort=-v:refname | head -n1
