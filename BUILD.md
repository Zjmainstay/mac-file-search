# 构建说明

## 快速开始

使用Makefile一键构建：

```bash
# 构建所有（推荐）- 会自动将mac-file-scan打包到APP内
make

# 或者分别构建
make scanner  # 只构建命令行工具
make app      # 构建GUI应用（会自动打包mac-file-scan）
```

## 构建产物

构建完成后会生成：

1. **命令行扫描工具**（两个位置）：
   - `./mac-file-scan` - 当前目录
   - `./mac-search-app/bin/mac-file-scan` - 开发环境副本

2. **GUI应用**（已包含扫描工具）：
   - `./mac-search-app/build/bin/mac-search-app.app` - **可直接分发给用户**
   - APP包内包含：
     ```
     mac-search-app.app/
       Contents/
         MacOS/
           mac-search-app          <- 主程序
         Resources/
           mac-file-scan           <- 嵌入的扫描工具（自动打包）
     ```

## Makefile 命令

| 命令 | 说明 |
|------|------|
| `make` 或 `make all` | 构建所有（命令行 + GUI + 打包） |
| `make scanner` 或 `make cli` | 只构建命令行工具 |
| `make app` 或 `make gui` | 构建GUI应用并自动打包扫描工具 |
| `make clean` | 清理所有构建产物 |
| `make test` | 测试命令行工具（扫描/Applications） |
| `make help` | 显示帮助信息 |

## 发布给用户

**重要**：用户只需要 `mac-search-app.app`，无需其他文件！

```bash
# 1. 构建
make

# 2. 打包发布（只需要这个文件）
zip -r mac-search-app.zip mac-search-app/build/bin/mac-search-app.app

# 3. 用户下载后直接使用
unzip mac-search-app.zip
open mac-search-app.app  # 或双击打开
```

**APP已包含**：
- ✓ 主程序
- ✓ mac-file-scan扫描工具（自动打包在Resources目录）
- ✓ 所有依赖

用户**不需要**：
- ✗ 安装Go
- ✗ 安装Wails
- ✗ 单独下载mac-file-scan
- ✗ 任何额外配置

## Makefile 命令

| 命令 | 说明 |
|------|------|
| `make` 或 `make all` | 构建所有（命令行 + GUI） |
| `make scanner` 或 `make cli` | 只构建命令行工具 |
| `make app` 或 `make gui` | 只构建GUI应用 |
| `make clean` | 清理所有构建产物 |
| `make test` | 测试命令行工具（扫描/Applications） |
| `make help` | 显示帮助信息 |

## 手动构建

### 命令行工具

```bash
# 编译
go build -o mac-file-scan main.go

# 复制到mac-search-app/bin（供GUI调用）
mkdir -p mac-search-app/bin
cp mac-file-scan mac-search-app/bin/
```

### GUI应用

```bash
cd mac-search-app
wails build
```

## 使用示例

### 命令行工具

```bash
# 扫描/Applications目录
sudo ./mac-file-scan -path /Applications -output result.json

# 扫描根目录，排除外置硬盘
sudo ./mac-file-scan -path / -output result.json -exclude /Volumes/MacExtDisk

# 显示错误详情
sudo ./mac-file-scan -path / -output result.json -errors
```

### GUI应用

```bash
# 运行
./mac-search-app/build/bin/mac-search-app.app/Contents/MacOS/mac-search-app

# 或者双击打开
open ./mac-search-app/build/bin/mac-search-app.app
```

## 性能优化说明

GUI应用使用**两步扫描法**实现高性能：

### 工作原理

1. **扫描阶段**（2分钟）：
   - 调用内嵌的 `mac-file-scan` 工具
   - 只需**一次sudo调用**，以root权限扫描全盘
   - 生成JSON文件（约几百MB）

2. **导入阶段**（1分钟）：
   - 解析JSON文件
   - 批量插入数据库（50000条/批）

### 性能对比

| 方案 | 全盘扫描耗时 | sudo调用次数 |
|------|-------------|-------------|
| 旧方案（逐目录sudo ls） | **10+ 分钟** | 约1000次 |
| 新方案（内嵌mac-file-scan） | **2-3 分钟** | 1次 |
| **性能提升** | **5倍** | **1000倍减少** |

### 技术细节

- **无需外部依赖**：mac-file-scan已嵌入APP包
- **自动查找**：优先从APP包内 `Contents/Resources/` 查找
- **降级策略**：找不到时自动降级到逐目录扫描（慢但可用）

## 故障排除

### APP包不完整（发布时）

**症状**：用户反馈找不到mac-file-scan

**原因**：没有用 `make app` 构建，而是直接用 `wails build`

**解决**：
```bash
# 正确的构建方式
make app  # 或 make

# 错误的构建方式（不会打包mac-file-scan）
cd mac-search-app && wails build  # ❌
```

### 验证APP包完整性

```bash
# 检查mac-file-scan是否在APP包内
ls -lh mac-search-app/build/bin/mac-search-app.app/Contents/Resources/mac-file-scan

# 应该显示：
# -rwxr-xr-x  1 user  staff   2.9M  日期 时间 mac-file-scan
```

### 开发环境扫描慢

**症状**：开发时扫描仍然很慢

**原因**：bin/目录下没有mac-file-scan

**解决**：
```bash
make scanner  # 生成到bin/目录
```

### 权限问题

**症状**：扫描时提示权限错误

**解决**：
- GUI应用会提示输入sudo密码
- 命令行工具需要用 `sudo ./mac-file-scan` 运行

### 编译错误

**症状**：`go build` 失败

**解决**：
```bash
# 检查Go版本（需要1.18+）
go version

# 更新依赖
go mod tidy
```
