.PHONY: build test lint check-lines web-e2e

# 构建单二进制（前端嵌入在 Task 23 接入）。
build:
	go build -o bin/mtx ./cmd/mtx

# 全量测试。docker 标签的集成测试需要本机 Docker。
test:
	go test ./...

# lint = vet + 单文件行数红线（spec：>800 行 fail）。
lint: check-lines
	go vet ./...

check-lines:
	@viol=$$(find . -name '*.go' -not -path './web/*' | xargs wc -l | awk '$$1 > 800 && $$2 != "total" {print $$2" ("$$1" 行)"}'); \
	if [ -n "$$viol" ]; then echo "超过 800 行红线："; echo "$$viol"; exit 1; fi

web-e2e:
	@echo "Task 23 接入 Playwright 后启用"
