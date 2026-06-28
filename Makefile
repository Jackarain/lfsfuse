# LFSFuse - Git LFS 虚拟文件系统
#
# 常用命令:
#   make build      编译项目
#   make install    安装到 $GOPATH/bin
#   make clean      清理编译产物
#   make test       运行测试
#   make lint       运行代码检查

BINARY_NAME := lfsfuse
OUTPUT_DIR := bin
GO := go
GOFLAGS := -ldflags="-s -w"
INSTALL_DIR := $(shell $(GO) env GOPATH)/bin

.PHONY: all build install clean test lint run help

all: clean build

build:
	@echo ">>> 编译 $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) -o $(OUTPUT_DIR)/$(BINARY_NAME) ./cmd/lfsfuse
	@echo ">>> 编译完成: $(OUTPUT_DIR)/$(BINARY_NAME)"

install: build
	@echo ">>> 安装到 $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)
	cp $(OUTPUT_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@echo ">>> 安装完成"

clean:
	@echo ">>> 清理编译产物..."
	rm -rf $(OUTPUT_DIR)
	@echo ">>> 清理完成"

test:
	@echo ">>> 运行测试..."
	$(GO) test -v -race ./...
	@echo ">>> 测试完成"

lint:
	@echo ">>> 运行代码检查..."
	@command -v golangci-lint >/dev/null 2>&1 || (echo "请先安装 golangci-lint: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; exit 1)
	golangci-lint run ./...
	@echo ">>> 代码检查完成"

vet:
	@echo ">>> 运行 go vet..."
	$(GO) vet ./...
	@echo ">>> go vet 完成"

run: build
	@echo ">>> 启动 $(BINARY_NAME) (示例用法)..."
	@echo "    用法: $(OUTPUT_DIR)/$(BINARY_NAME) --repo <path> --lfsurl <url> --mount <path>"
	@echo "    配置文件: $(OUTPUT_DIR)/$(BINARY_NAME) --config <config_file>"

help:
	@echo "LFSFuse 构建系统"
	@echo ""
	@echo "用法: make <target>"
	@echo ""
	@echo "目标列表:"
	@echo "  build     编译二进制文件到 $(OUTPUT_DIR)/"
	@echo "  install   编译并安装到 \$$GOPATH/bin"
	@echo "  clean     删除编译产物"
	@echo "  test      运行所有测试"
	@echo "  lint      运行 golangci-lint 代码检查"
	@echo "  vet       运行 go vet 静态分析"
	@echo "  run       编译并显示使用说明"
	@echo "  all       clean + build"
	@echo "  help      显示本帮助信息"
