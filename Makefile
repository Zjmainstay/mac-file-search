# Makefile for mac-file-search project
# 构建命令行扫描工具和GUI应用

# 变量定义
GO := go
BINARY_NAME := mac-file-search
MAIN_GO := main.go
BUILD_DIR := .
APP_BIN_DIR := mac-search-app/bin
WAILS := wails

# 默认目标
.PHONY: all
all: scanner app

# 构建命令行扫描工具（放两个位置）
.PHONY: scanner
scanner:
	@echo "==> 编译命令行扫描工具..."
	@$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_GO)
	@echo "==> 生成: $(BUILD_DIR)/$(BINARY_NAME)"
	@mkdir -p $(APP_BIN_DIR)
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(APP_BIN_DIR)/$(BINARY_NAME)
	@echo "==> 复制到: $(APP_BIN_DIR)/$(BINARY_NAME)"
	@echo "✓ 命令行扫描工具构建完成"

# 构建GUI应用（自动打包mac-file-search）
.PHONY: app
app: scanner
	@echo "==> 编译GUI应用..."
	@cd mac-search-app && $(WAILS) build
	@echo "==> 将mac-file-search打包到APP内..."
	@mkdir -p "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources"
	@cp $(BUILD_DIR)/$(BINARY_NAME) "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@chmod +x "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@echo "==> 已复制到: Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@echo "✓ GUI应用构建完成（已包含扫描工具）"

# 构建GUI应用（强制跳过前端构建，使用已有的 dist/）
.PHONY: app-skip-frontend
app-skip-frontend: scanner
	@echo "==> 编译GUI应用（跳过前端构建）..."
	@cd mac-search-app && $(WAILS) build -s
	@echo "==> 将mac-file-search打包到APP内..."
	@mkdir -p "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources"
	@cp $(BUILD_DIR)/$(BINARY_NAME) "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@chmod +x "mac-search-app/build/bin/Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@echo "==> 已复制到: Mac文件搜索.app/Contents/Resources/$(BINARY_NAME)"
	@echo "✓ GUI应用构建完成（已包含扫描工具）"

# 只构建命令行工具
.PHONY: cli
cli: scanner

# 只构建GUI应用
.PHONY: gui
gui: app

# 清理构建产物
.PHONY: clean
clean:
	@echo "==> 清理构建产物..."
	@rm -f $(BUILD_DIR)/$(BINARY_NAME)
	@rm -f $(APP_BIN_DIR)/$(BINARY_NAME)
	@rm -rf mac-search-app/build
	@echo "✓ 清理完成"

# 测试命令行工具（扫描/Applications目录）
.PHONY: test
test: scanner
	@echo "==> 测试扫描 /Applications 目录..."
	@sudo ./$(BINARY_NAME) -path /Applications -output test-output.json
	@echo "✓ 测试完成，结果保存到 test-output.json"

# 显示帮助
.PHONY: help
help:
	@echo "Mac File Scan 项目构建工具"
	@echo ""
	@echo "使用方法："
	@echo "  make          - 构建所有（命令行工具 + GUI应用）"
	@echo "  make scanner  - 只构建命令行扫描工具"
	@echo "  make cli      - 同 scanner"
	@echo "  make app      - 只构建GUI应用"
	@echo "  make gui      - 同 app"
	@echo "  make clean    - 清理所有构建产物"
	@echo "  make test     - 测试命令行工具"
	@echo "  make help     - 显示此帮助信息"
	@echo ""
	@echo "构建产物："
	@echo "  ./mac-file-search                    - 命令行工具"
	@echo "  ./mac-search-app/bin/mac-file-search - 命令行工具副本（供GUI调用）"
	@echo "  ./mac-search-app/build/bin/          - GUI应用包"
