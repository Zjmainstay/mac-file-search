# Mac File Search - 高性能文件扫描与搜索工具

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)](https://golang.org)

基于 Go 语言和多协程实现的高性能磁盘文件遍历工具，提供 **GUI 应用** 和 **命令行工具** 两种使用方式。

## 🚀 快速开始

### GUI应用（推荐给普通用户）

1. 下载 `mac-search-app.app`
2. 双击打开
3. 输入sudo密码（仅首次）
4. 选择扫描路径，等待2-3分钟
5. 开始搜索文件！

**特点**：
- ⚡ 极速扫描：2分钟扫描全盘（200万+ 文件）
- 🔍 强大搜索：支持模糊搜索、正则表达式
- 💾 离线使用：一次扫描，永久搜索
- 📦 开箱即用：无需任何配置

### 命令行工具（推荐给开发者）

```bash
# 扫描根目录并保存结果
sudo ./mac-file-search -path / -output result.json

# 查找大于100MB的文件
sudo ./mac-file-search -path / -min 100M -output large_files.json
```

---

## 功能特性

- **多协程并发扫描**：充分利用多核 CPU，默认使用 CPU 核心数 × 2 个工作协程
- **智能去重**：自动检测并跳过重复目录（firmlinks、硬链接等），避免重复计算磁盘占用
- **文件树构建**：构建完整的文件系统树状结构
- **文件大小筛选**：支持设置最小/最大文件大小过滤条件
- **路径排除**：支持排除指定路径（如外接硬盘、临时目录等）
- **实时进度显示**：扫描过程中实时显示进度统计
  - 🎯 **智能进度条**：根据磁盘已使用空间显示扫描进度百分比
  - ⏱️  已用时间
  - 📁 目录统计（数量 + 扫描速度）
  - 📄 文件统计（数量 + 扫描速度）
  - 💿 磁盘占用（已扫描 + 扫描速度 GB/s）
  - ⚠️  错误计数
- **详细统计报告**：扫描完成后显示详细统计和平均速度
  - 💿 磁盘占用统计
  - 🔗 硬链接去重（避免重复计算）
  - 内部自动处理重复目录（firmlinks/挂载点）
- **全盘扫描支持**：可以从根目录 `/` 扫描整个磁盘
- **智能错误处理**：自动过滤预期的系统错误（如 /dev/fd 的 bad file descriptor），保持输出清晰
- **权限处理**：自动处理权限错误，不中断扫描进程
- **性能优化**：使用 sync.Map 和原子操作保证并发安全

> 💡 **关于进度显示**：程序会自动获取磁盘已使用空间，并根据扫描进度显示进度条和百分比，让您直观了解扫描完成情况。

## 🔧 安装与构建

### 依赖要求

- Go 1.21+
- Make
- Wails v2（仅 GUI 应用需要）

### 构建命令

```bash
# 克隆仓库
git clone https://github.com/Zjmainstay/mac-file-search.git
cd mac-file-search

# 构建所有（命令行 + GUI）
make

# 只构建命令行工具
make scanner

# 只构建 GUI 应用
make app

# 清理构建产物
make clean
```

详细构建说明请参考 [BUILD.md](BUILD.md)。

## 使用方法

### 基本用法

```bash
# 扫描当前目录
./file-scan

# 扫描指定目录
./file-scan -path /Users/username/Documents

# 扫描整个磁盘（需要管理员权限）
sudo ./file-scan -path /
```

### 文件大小筛选

```bash
# 只扫描大于 100MB 的文件（人性化单位）
./mac-file-search -path /Users -min 100M

# 只扫描 100MB - 500MB 之间的文件
./mac-file-search -path /Users -min 100M -max 500M

# 只扫描小于 10KB 的文件
./mac-file-search -path /Users -max 10K
```

### 文件类型过滤

```bash
# 只扫描 .txt 和 .log 文件
./mac-file-search -path /var/log -include-ext .txt,.log

# 排除临时文件和缓存文件
./mac-file-search -path /Users -exclude-ext .tmp,.cache,.bak

# 使用正则表达式匹配文件名
./mac-file-search -path /src -name "^test.*\.go$"
```

### 显示文件树

```bash
# 显示文件树结构（默认深度3层）
./file-scan -path /path/to/scan -tree

# 自定义显示深度
./file-scan -path /path/to/scan -tree -depth 5
```

### 自定义并发数

```bash
# 使用 16 个工作协程
./file-scan -path /path/to/scan -workers 16
```

## 命令行参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-path` | string | `.` | 扫描的根目录路径 |
| `-min` | string | `0` | 最小文件大小 (支持: 100M, 1.5G, 1024) |
| `-max` | string | `0` | 最大文件大小 (支持: 100M, 1.5G, 1024), 0表示不限制 |
| `-workers` | int | `CPU×2` | 并发工作协程数 |
| `-tree` | bool | `false` | 是否显示文件树结构 |
| `-depth` | int | `0` | 文件树显示深度，0表示不限制 |
| `-output` | string | `""` | 输出文件路径（JSON Lines格式），实时写入 |
| `-errors` | bool | `false` | 是否显示错误详情 |
| `-exclude` | string | `""` | 排除的路径，多个用逗号分隔 |
| `-include-ext` | string | `""` | 只包含的文件扩展名，多个用逗号分隔 |
| `-exclude-ext` | string | `""` | 排除的文件扩展名，多个用逗号分隔 |
| `-name` | string | `""` | 文件名正则表达式过滤 |

## 使用示例

### 示例 1：查找大文件（100MB以上）

查找整个系统中所有大于 100MB 的文件并保存结果：

```bash
# 使用人性化单位
sudo ./mac-file-search -path / -min 100M -output large_files.json -workers 32

# 分析结果，找出最大的文件
grep '"is_dir":false' large_files.json | \
  jq -r '"\(.disk_usage)\t\(.path)"' | \
  sort -rn | head -20 | \
  awk '{printf "%.2f GB\t%s\n", $1/1024/1024/1024, $2}'
```

### 示例 2：查找特定类型的大文件

查找所有大于 10MB 的视频文件：

```bash
./mac-file-search -path /Users -min 10M -include-ext .mp4,.mkv,.avi,.mov -output videos.json
```

### 示例 3：统计目录信息

统计项目目录的文件数量和总大小：

```bash
./mac-file-search -path ~/projects/myapp
```

### 示例 3：全盘扫描（排除外接硬盘）

扫描整个磁盘，但排除外接硬盘（macOS 需要 sudo）：

```bash
sudo ./mac-file-search -path / -workers 32 -exclude /Volumes/MacExtDisk -output disk_scan.json
```

### 示例 4：限制显示深度

只显示前3层目录结构：

```bash
./file-scan -path /Users -tree -depth 3
```

### 示例 5：全盘扫描并保存结果

全盘扫描，实时保存到文件，并显示错误：

```bash
sudo ./file-scan -path / -output /tmp/disk-scan.jsonl -errors
```

### 示例 6：分析扫描结果

使用输出文件可以随时中断扫描，数据不会丢失。提供了分析脚本：

```bash
# 开始扫描（即使中途 Ctrl+C 中断，已扫描的数据也保存在文件中）
sudo ./file-scan -path / -output scan.jsonl -errors

# 使用分析脚本查看统计信息和最大文件
./build-tree.sh scan.jsonl

# 或者手动分析：
# 查看已扫描多少文件
wc -l scan.jsonl

# 查看最大的文件
grep -v '^#' scan.jsonl | jq -r 'select(.is_dir==false) | "\(.size)\t\(.path)"' | sort -rn | head -10

# 按扩展名统计文件数量
grep -v '^#' scan.jsonl | jq -r 'select(.is_dir==false) | .name' | grep -o '\.[^.]*$' | sort | uniq -c | sort -rn

# 计算总大小
grep -v '^#' scan.jsonl | jq -s 'map(select(.is_dir==false) | .size) | add'

# 查找特定路径下的文件
grep -v '^#' scan.jsonl | jq -r 'select(.path | startswith("/usr/local")) | .path'
```

## 输出示例

```
💿 磁盘总空间: 500.00 GB
📊 已使用: 350.25 GB (70.1%) | 剩余: 149.75 GB
开始扫描: /usr
工作协程数: 16

💡 将根据已使用空间显示扫描进度

[████████████████████░░░░░░░░░░░░░░░░░░░░] 48.5%
⏱️  2s | 📁 12,094 (5,012/s) | 📄 70,185 (34,808/s) | 💿 169.75 GB (84.9 GB/s)

所有扫描任务已完成，等待 worker 退出...

════════════════════════════════════════
✅ 扫描完成!
════════════════════════════════════════
⏱️  用时: 10.529s
📁 目录数: 19,112
📄 文件数: 140,413
💿 磁盘占用: 4.32 GB
⚡ 平均速度: 13,334 个文件/秒, 410.5 MB/秒
🔗 符号链接: 11,427 (已跳过)
🔗 硬链接: 156 (已去重)
⚠️  错误数: 7
════════════════════════════════════════
```

## 项目文件

- `main.go` - 主程序源码
- `README.md` - 项目文档
- `examples.sh` - 使用示例脚本
- `build-tree.sh` - 分析扫描结果的工具脚本
- `benchmark_workers.sh` - Worker 性能测试脚本
- `benchmark_result.txt` - 性能测试结果
- `.gitignore` - Git 忽略文件配置
- `mac-file-search` - 编译后的可执行文件

## 性能说明

### Worker 数量性能测试

测试环境：MacBook (测试路径: /Users/macbok, ~140万文件)

| Workers | 用时(秒) | 速度(文件/秒) | 说明 |
|---------|----------|---------------|------|
| 8       | 25.45    | 56,784        | 较慢 |
| 16      | 22.29    | 64,835        | 性价比高 |
| **32**  | **21.42** | **67,463**   | **推荐** ⭐ |
| 64      | 22.60    | 63,950        | 开始下降 |
| 128     | 23.06    | 62,678        | 过多反而慢 |

**结论**：
- **推荐使用 32 个 workers** 获得最佳性能
- Worker 数量不是越多越好，过多会导致上下文切换开销
- 16-32 是性价比最高的区间
- 具体最优值取决于 CPU 核心数和磁盘 IO 性能

### 性能特点

- **CPU 使用**：默认使用 CPU 核心数 × 2 个协程，可通过 `-workers` 参数调整
- **内存使用**：会在内存中构建完整的文件树，大规模扫描时注意内存占用
- **IO 优化**：使用并发读取目录，充分利用磁盘 IOPS
- **错误处理**：权限错误不会中断扫描，统计在错误计数中
- **智能去重**：自动检测 firmlinks 和硬链接，避免重复计算（macOS 的 `/Users` 和 `/System/Volumes/Data/Users` 指向同一位置）

## 注意事项

1. **权限问题**：扫描系统目录或根目录时可能需要 sudo 权限
2. **内存占用**：全盘扫描会占用较多内存，建议在内存充足的机器上运行
3. **符号链接**：程序会自动跳过符号链接，避免循环引用和重复计算文件大小
4. **特殊文件**：设备文件、socket 等特殊文件会被自动跳过
5. **智能错误处理**：
   - 对于 `/dev/fd` 等动态虚拟目录可能出现的 "bad file descriptor" 错误会被自动过滤
   - 这类预期的系统错误不会显示在错误详情中，但会计入错误计数
   - 其他错误使用 `-errors` 参数可以查看详情
6. **隐藏文件**：会扫描所有文件，包括隐藏文件（以 `.` 开头的文件）
7. **重复目录去重**：
   - macOS 系统中 `/Users` 和 `/System/Volumes/Data/Users` 指向同一位置
   - 程序会自动检测并跳过重复目录，避免重复计算磁盘占用
   - 统计信息中会显示跳过的重复目录数量
8. **数据安全**：
   - 使用 `-output` 参数可实时保存扫描结果，即使中途中断也不会丢失数据
   - 输出文件采用 JSON Lines 格式，每行一个文件记录，方便处理
   - 建议全盘扫描时始终使用 `-output` 参数
9. **错误处理**：
   - 默认情况下错误会被静默处理，只统计错误数量
   - 使用 `-errors` 参数可以实时显示错误详情
   - 权限错误不会中断扫描进程
10. **虚拟内存文件建议**：
   - 如需排除虚拟内存文件（通常几十GB），可以使用：
   ```bash
   sudo ./mac-file-search -path / -exclude /private/var/vm,/System/Volumes/VM
   ```

## 技术实现

- **并发模型**：使用 Worker Pool 模式，多个协程从队列中获取目录任务
- **数据结构**：使用 sync.Map 存储节点映射，支持并发安全访问
- **原子操作**：使用 atomic.Int64 统计文件数、目录数等，避免锁竞争
- **互斥锁**：在更新文件树结构时使用 sync.RWMutex 保护

## 后续改进计划

- [x] 支持符号链接检测和跳过
- [x] 实时扫描速度显示
- [x] 实时保存扫描结果（防止数据丢失）
- [x] 错误详情显示选项
- [x] 稀疏文件检测
- [x] 硬链接去重
- [x] 重复目录检测（firmlinks/挂载点）
- [x] 路径排除功能
- [x] 人性化文件大小参数（100M, 1G等）
- [x] 文件类型过滤（按扩展名）
- [x] 正则表达式匹配文件名
- [x] 进度条可视化（基于磁盘已使用空间）
- [x] 智能错误过滤（自动过滤预期的系统错误）
- [ ] 支持导出为完整 JSON 格式
- [ ] 支持暂停和恢复扫描
- [ ] 生成扫描报告（HTML/PDF）

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启 Pull Request

## 📝 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件。

## 🙏 致谢

- 感谢 Go 语言社区
- 感谢 Wails 框架
- 感谢所有贡献者

---

**注意**：扫描系统目录或根目录时需要 sudo 权限。
