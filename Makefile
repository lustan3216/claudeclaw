# goclaudeclaw Makefile
# 用法: make build | make install | make run | make test | make lint

MODULE   := github.com/lustan3216/goclaudeclaw
BINARY   := goclaudeclaw
CMD_PATH := ./cmd/goclaudeclaw

# 从 git tag 自动获取版本，回退到 "dev"
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION) -s -w"

# 构建输出目录
BUILD_DIR := ./dist

.PHONY: all build install run test lint clean tidy fmt vet help

all: lint test build

## build: 编译为本地平台二进制，输出到 ./dist/
build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)
	@echo "✓ 构建完成: $(BUILD_DIR)/$(BINARY) ($(VERSION))"

## build-linux: 交叉编译 Linux amd64
build-linux:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(CMD_PATH)
	@echo "✓ Linux 构建完成: $(BUILD_DIR)/$(BINARY)-linux-amd64"

## build-all: 编译所有常用平台
build-all: build-linux
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 $(CMD_PATH)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 $(CMD_PATH)
	@echo "✓ 全平台构建完成"

## install: 安装到 $GOPATH/bin（默认 ~/go/bin）
install:
	CGO_ENABLED=0 go install $(LDFLAGS) $(CMD_PATH)
	@echo "✓ 已安装 $(BINARY) 到 $$(go env GOPATH)/bin/"

## run: 直接运行（需要 config.yaml 存在）
run:
	go run $(CMD_PATH) --config config.yaml --debug

## run-validate: 校验配置文件
run-validate:
	go run $(CMD_PATH) validate --config config.yaml

## test: 运行所有测试（含竞态检测）
test:
	go test -v -race -timeout 60s ./...

## test-short: 快速测试（跳过慢测试）
test-short:
	go test -short -race ./...

## bench: 运行性能基准测试
bench:
	go test -bench=. -benchmem ./...

## lint: 运行 golangci-lint
lint:
	@which golangci-lint > /dev/null || (echo "请先安装 golangci-lint: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

## fmt: 格式化代码
fmt:
	gofmt -w .
	@echo "✓ 代码已格式化"

## vet: 运行 go vet
vet:
	go vet ./...

## tidy: 整理 go.mod / go.sum
tidy:
	go mod tidy
	@echo "✓ go.mod 已整理"

## clean: 清理构建产物
clean:
	rm -rf $(BUILD_DIR)
	go clean -cache
	@echo "✓ 清理完成"

## deps: 下载所有依赖
deps:
	go mod download
	@echo "✓ 依赖下载完成"

## config: 从示例生成 config.yaml（不覆盖已有文件）
config:
	@if [ -f config.yaml ]; then \
		echo "config.yaml 已存在，跳过（如需重置请手动删除）"; \
	else \
		cp config.example.yaml config.yaml; \
		echo "✓ config.yaml 已创建，请填入真实 token"; \
	fi

## help: 显示此帮助
help:
	@grep -E '^##' Makefile | sed 's/## //' | column -t -s ':'
