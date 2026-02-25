# 最近改进文档

## 2026-01-24 (晚上) - 修改时间字段与搜索状态优化

### 改进内容

#### 1. 添加文件修改时间字段显示

**需求**: 在搜索结果表格中显示文件的修改时间

**实现步骤**:

**步骤1: 在FileNode结构体中添加ModTime字段**
- **文件**: `main.go:25`
- **修改**: 在FileNode结构体中添加 `ModTime int64 \`json:"mod_time"\``
- **捕获时间**: 在扫描文件时设置 `ModTime: info.ModTime().Unix()` (main.go:376)

**步骤2: 在JSON输出中包含mod_time**
- **文件**: `main.go:409-420`
- **修改**: 在三种JSON输出格式中都添加 `mod_time` 字段
- **修改前**: `{\"path\":%q,\"name\":%q,\"size\":%d,\"disk_usage\":%d,\"is_dir\":%t}`
- **修改后**: `{\"path\":%q,\"name\":%q,\"size\":%d,\"disk_usage\":%d,\"mod_time\":%d,\"is_dir\":%t}`

**步骤3: 在索引器中读取和存储mod_time**
- **文件**: `indexer.go:1756, 1776`
- **修改**:
  - 在entry结构体中添加 `ModTime int64 \`json:"mod_time"\``
  - 在INSERT语句中使用 `entry.ModTime` 替代 `time.Now().Unix()`

**步骤4: 在前端显示mod_time**
- **文件**: `App.svelte`
- **修改**:
  - 添加格式化函数 `formatModTime()` (line ~483)
  - 调整列宽配置: `{ name: 25, path: 50, size: 10, modTime: 15 }` (line 51)
  - 添加表头列: "修改时间" (line ~760)
  - 添加数据列: `{formatModTime(result.mod_time)}` (line ~780)
  - 支持拖动调整"大小"和"修改时间"列宽度 (line ~98)

**时间格式**: 使用本地化格式显示（如：2026/01/24 20:30）
```javascript
function formatModTime(timestamp) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  return date.toLocaleDateString('zh-CN') + ' ' + date.toLocaleTimeString('zh-CN')
}
```

**列宽调整**: 减少"修改时间"宽度，增加"路径"宽度
- 时间字段大小相对固定，不需要太宽
- 路径字段更需要显示空间

#### 2. 优化搜索状态显示

**问题**: 用户输入搜索关键词后，在搜索完成前显示"未找到匹配的文件"，容易误导用户

**解决方案**:

**文件**: `App.svelte`

**修改**:
1. 添加 `isSearching` 状态变量 (line ~48)
2. 在 `performSearch()` 中设置搜索状态:
   ```javascript
   isSearching = true  // 搜索开始
   try {
     const results = await Search(...)
   } finally {
     isSearching = false  // 搜索结束
   }
   ```
3. 根据状态显示不同提示:
   ```svelte
   {:else if searchQuery}
     <div class="no-results">
       {#if isSearching}
         搜索中...
       {:else}
         未找到匹配的文件
       {/if}
     </div>
   ```

**效果**:
- 输入搜索词后，立即显示"搜索中..."
- 搜索完成后，如果无结果才显示"未找到匹配的文件"
- 用户体验更流畅，不会误以为搜索已完成

#### 3. 将单击改为双击打开文件

**需求**: 防止误触，改为双击打开文件

**文件**: `App.svelte:767`

**修改**:
- **修改前**: `on:click={() => openFile(result.path)}`
- **修改后**: `on:dblclick={() => openFile(result.path)}`

**效果**:
- 单击仅选中文件（高亮显示）
- 双击才打开文件
- 减少误操作

#### 4. 减少进度日志输出频率

**问题**: 索引导入时每5000条就输出一次进度，日志过于频繁

**文件**: `indexer.go:1700`

**修改**:
- **修改前**: 每批次（5000条）输出一次
- **修改后**: 每10万条输出一次
  ```go
  if insertCount%100000 == 0 {
      logToDebugWithTime(debugLog, "[PROGRESS] 已插入: %d 条", insertCount)
  }
  ```

**效果**:
- 200万文件从400次日志减少到20次
- 日志文件更简洁，更易于阅读

### 相关文件

- `main.go` - FileNode结构体，JSON输出格式
- `indexer.go` - JSON解析，数据库插入，进度日志
- `frontend/src/App.svelte` - 前端界面，搜索状态，修改时间显示

### 技术要点

1. **Unix时间戳**: 使用 `info.ModTime().Unix()` 获取秒级时间戳
2. **JSON序列化**: 使用 `fmt.Sprintf` 手动构建JSON（性能优化）
3. **本地化显示**: 使用 `toLocaleDateString('zh-CN')` 和 `toLocaleTimeString('zh-CN')`
4. **异步状态管理**: 使用 `try...finally` 确保状态正确重置
5. **列宽调整**: 通过拖动分隔线动态调整列宽，使用百分比布局

### 验证方法

**验证mod_time字段**:
```bash
# 1. 重新构建mac-file-search
go build -o mac-file-search main.go

# 2. 扫描测试
./mac-file-search -path /Applications -output /tmp/test.json

# 3. 检查JSON是否包含mod_time
grep -v '^#' /tmp/test.json | head -3 | jq '.'

# 4. 重建索引后检查数据库
sqlite3 ~/.mac-search-app/index.db "SELECT name, mod_time FROM files LIMIT 5"
```

**验证搜索状态**:
1. 打开APP，输入搜索关键词
2. 应该立即看到"搜索中..."
3. 搜索完成后，显示结果或"未找到匹配的文件"

---

## 2026-01-24 (下午) - 索引优化，大幅加速DELETE操作

### 问题

用户反馈清空数据库很慢：
- 344万条记录的DELETE操作耗时18秒（从16:28:24到16:28:42）
- 怀疑是时间字段或索引问题

### 问题分析

检查数据库结构发现有3个索引：
```sql
CREATE INDEX idx_name ON files(name);
CREATE INDEX idx_ext ON files(ext);
CREATE INDEX idx_path ON files(path);
```

**问题**：
1. **idx_path是多余的** - path字段有UNIQUE约束，SQLite会自动创建唯一索引
2. **idx_ext没有用到** - 查询中没有 `WHERE ext = ?` 条件
3. **LIKE '%..%'无法利用索引** - 主要查询是 `name LIKE '%keyword%'`，通配符在开头无法有效利用索引
4. **DELETE操作需要更新所有索引** - 3个索引导致DELETE变慢

### 解决方案

#### 1. 删除多余索引

**只保留idx_name**，删除idx_ext和idx_path：

```go
// 创建表时只创建name索引
CREATE INDEX IF NOT EXISTS idx_name ON files(name);
-- 不再创建idx_ext和idx_path
```

**迁移旧数据库**：
```go
// 在NewIndexer时删除旧索引
db.Exec("DROP INDEX IF EXISTS idx_ext")
db.Exec("DROP INDEX IF EXISTS idx_path")
```

#### 2. 删除重复的索引创建代码

删除了3处创建多余索引的代码：
- `indexer.go:141-143` - 初始化时只创建idx_name
- `indexer.go:330-332` - 删除buildIndexWithMacFileScan后重复创建索引的代码
- `indexer.go:600-607` - 删除异步创建idx_ext和idx_path的goroutine

### 性能提升

| 操作 | 索引数量 | 预期效果 |
|------|---------|----------|
| DELETE 344万条 | 3个索引 → 1个索引 | **18秒 → 2-3秒** (6倍提升) |
| INSERT | 减少索引维护 | **10-20%提升** |
| 数据库大小 | 删除2个索引 | **减少约20-30%** |
| CREATE INDEX | 减少2个索引 | **索引创建时间减半** |

### 技术细节

**为什么idx_path是多余的**:
- `path TEXT NOT NULL UNIQUE` 已有UNIQUE约束
- SQLite自动为UNIQUE字段创建索引（名为`sqlite_autoindex_files_1`）
- 再创建idx_path相当于对同一列创建了两个索引

**为什么idx_ext没用**:
- 查询主要是 `name LIKE ? OR path LIKE ?`
- 从未使用 `WHERE ext = ?` 条件
- ext字段只用于显示，不用于筛选

**为什么idx_name仍然有用**:
- 虽然 `name LIKE '%keyword%'` 无法利用索引
- 但 `name LIKE 'keyword%'` 可以利用索引（前缀匹配）
- 用户经常使用前缀搜索（如"app"找"application"）
- 保留索引对部分场景仍有帮助

### 验证方法

构建后可以查看索引：
```bash
sqlite3 ~/.mac-search-app/index.db ".indexes files"
# 应该只看到: idx_name, sqlite_autoindex_files_1
```

---

## 2026-01-24 (下午) - 并发性能优化

### 改进内容

基于前期的日志优化和数据库空间回收优化，进一步提升并发处理性能。

#### 1. 扫描Worker并发度优化

**文件**: `indexer.go:384-388`

**优化前**:
```go
workerCount := runtime.NumCPU() * 4
dirQueue := make(chan string, workerCount*10)
filesChan := make(chan FileInfo, 100000)
```

**优化后**:
```go
// 优化：增加worker到CPU*8，提高并发度，充分利用多核和IO
workerCount := runtime.NumCPU() * 8
// 优化：增大队列容量，减少阻塞
dirQueue := make(chan string, workerCount*20)
// 优化：增大文件通道到20万，减少写入阻塞
filesChan := make(chan FileInfo, 200000)
```

**理由**:
- **Worker数量** (CPU×4 → CPU×8): 文件扫描属于IO密集型任务，增加worker可以充分利用IO等待时间
- **目录队列** (×10 → ×20): 增大队列容量可以减少worker等待，提升吞吐量
- **文件通道** (10万 → 20万): 扫描和写入是生产者-消费者模式，增大缓冲可以减少阻塞

#### 2. 数据库批量写入优化

**文件**: `indexer.go:431-432`

**优化前**:
```go
batchSize := 500000 // 50万行一次提交
```

**优化后**:
```go
// 优化：增大批次到100万，进一步减少数据库提交次数，提升性能
batchSize := 1000000 // 100万行一次提交，对于全盘扫描200万文件只需2次提交
```

**理由**:
- 全盘扫描约200万文件，50万批次需要4次提交，100万批次只需2次
- 减少事务提交次数可以显著提升数据库写入性能
- SQLite的事务提交有较大开销，批次越大越高效

#### 3. JSON导入批量INSERT优化

**文件**: `indexer.go:1597-1600`

**优化前**:
```go
batchSize := 50000 // 5万行一次SQL
```

**优化后**:
```go
// 优化：增大批次到10万行，减少SQL执行频率，提升导入性能
batchSize := 100000 // 10万行一次SQL，对于200万文件只需20次SQL
```

**理由**:
- 用户反馈进度日志打印过于频繁（每秒多次）
- 增大批次可以减少SQL执行次数和日志输出频率
- 从5万提升到10万，200万文件从40次SQL减少到20次

### 性能预期

基于这些优化，预期性能提升：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| Worker并发数 | CPU×4 | CPU×8 | **2倍** |
| 目录队列容量 | Worker×10 | Worker×20 | **2倍** |
| 文件通道容量 | 10万 | 20万 | **2倍** |
| 数据库批次提交次数 | 4次 | 2次 | **50%减少** |
| JSON导入SQL次数 | 40次 | 20次 | **50%减少** |

**综合效果**:
- **扫描阶段**: 并发度提升2倍，队列容量提升2倍，预期扫描速度提升30-50%
- **写入阶段**: 批次翻倍，事务提交减半，预期写入速度提升20-30%
- **日志输出**: 进度打印频率降低50%，日志更清晰

### 注意事项

1. **内存使用**: 增大缓冲区和批次会增加内存占用
   - 20万文件通道 × 每个FileInfo约200字节 ≈ 40MB
   - 100万批次 × 6个字段 × 平均100字节 ≈ 600MB
   - 对于现代Mac（8GB+内存）不是问题

2. **CPU使用**: Worker数量增加会提高CPU使用率
   - 对于8核CPU，从32个worker增加到64个worker
   - 文件IO为主要瓶颈，CPU仍有余量

3. **适用场景**: 这些参数针对全盘扫描（200万+文件）优化
   - 小规模扫描（<10万文件）提升不明显
   - 建议保留这些参数作为默认值

---

## 2026-01-24 (下午) - 日志优化与数据库空间回收

### 问题
1. debugLog 文件中的日志没有时间戳，无法跟踪每个步骤的执行时间
2. 有大量频繁打印的调试日志，影响可读性
3. 数据库文件膨胀到 2.9GB，但只有 236,085 条记录
4. 重建索引时，界面上一个索引的搜索结果仍然显示

### 解决方案

#### 1. 为所有日志添加时间前缀

**文件**: `indexer.go`

创建 `logToDebugWithTime()` 辅助函数：
```go
// logToDebugWithTime 带时间戳写入 debugLog（如果不为 nil）
func logToDebugWithTime(debugLog *os.File, format string, args ...interface{}) {
    if debugLog != nil {
        timestamp := time.Now().Format("15:04:05")
        message := fmt.Sprintf(format, args...)
        debugLog.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
    }
}
```

**修改范围**:
- 将所有 `debugLog.WriteString(msg + "\n")` 替换为 `logToDebugWithTime(debugLog, "格式", 参数...)`
- 涉及约 40+ 处日志输出

**效果**:
```
[15:51:39] [RESET] stopFlag: false -> false, fileCount: 0, dirCount: 0, totalDisk: 0, openFiles: 0
[15:51:39] 磁盘总空间: 245107195904, 已使用: 218069319680 (89.0%), 剩余: 27037876224
[15:51:39] [EXCLUDE] 当前排除路径列表 (共 1 个):
[15:51:39]   [0] /Volumes/MacExtDisk
[15:51:39] 清空前数据库有 236085 条记录，索引路径: /Applications
[15:51:40] 清空后数据库有 0 条记录
[15:51:40] 清空数据耗时: 0.52秒
[15:51:40] 设置性能参数
[15:51:40] 开始扫描文件
[15:51:40] [STRATEGY] 检测到sudo密码，使用mac-file-search一次性扫描
[15:51:40] 调用mac-file-search扫描（预计2分钟）
[15:51:47] [MAC-FILE-SEARCH] 扫描完成，耗时: 6.71秒
[15:51:47] [MAC-FILE-SEARCH] 输出文件大小: 51.98 MB
[15:51:47] 解析JSON并导入数据库
[15:51:50] [PROGRESS] 已插入: 5000 条
[15:51:53] [MAC-FILE-SEARCH] 解析完成，共236090行，插入236085条，耗时: 6.04秒
[15:51:53] [CLEANUP] 临时文件已删除
[15:51:53] [COMPLETE] 索引构建完成，总耗时: 17.30秒
```

#### 2. 删除频繁的调试日志

**删除的日志**:
- ❌ 子目录的 ReadDir 日志（每个子目录都打印）
- ❌ 子目录添加到队列的日志（每个子目录都打印）
- ❌ 空子目录的日志

**保留的日志**:
- ✅ 根目录的 ReadDir 日志（重要）
- ✅ 批量 INSERT 进度日志（每 5000 条一次）
- ✅ 所有带特殊标记的日志（`[STRATEGY]`, `[COMPLETE]`, `[CLEANUP]` 等）

**代码位置**:
- `indexer.go:728-736` - 只记录根目录 ReadDir
- `indexer.go:802-806` - 只记录根目录为空情况
- `indexer.go:968-971` - 删除子目录添加到队列的日志

#### 3. 数据库空间回收（VACUUM）

**问题分析**:
```bash
$ sqlite3 ~/.mac-search-app/index.db "PRAGMA page_count; PRAGMA page_size; PRAGMA freelist_count;"
755486     # 总页数
4096       # 页大小
722577     # 空闲页数（95.6%！）
```

计算：755,486 × 4,096 = 3,094,310,912 字节 ≈ 2.9GB

**根本原因**:
- 多次 DROP TABLE 和重建索引导致大量碎片
- SQLite 不会自动回收空间

**解决方案**:

在每次重建索引后自动执行 VACUUM：

```go
// 执行 VACUUM 回收空间（删除旧数据后会有大量碎片）
logWithTime("回收数据库空间...")
logToDebugWithTime(debugLog, "[VACUUM] 开始回收数据库空间")
vacuumStart := time.Now()
if _, err := idx.db.Exec("VACUUM"); err != nil {
    logToDebugWithTime(debugLog, "[VACUUM] 失败: %v", err)
} else {
    vacuumDuration := time.Since(vacuumStart).Seconds()
    logWithTime("回收空间耗时: %.2f秒", vacuumDuration)
    logToDebugWithTime(debugLog, "[VACUUM] 完成，耗时: %.2f秒", vacuumDuration)
}
```

**代码位置**: `indexer.go:337-346`

**效果**:
- 数据库从 **2.9GB 缩小到 123MB**（减少 95%！）
- 对 236,085 条记录来说，123MB 是合理大小
- VACUUM 耗时约 1-2 秒，可接受

**验证**:
```bash
$ ls -lh ~/.mac-search-app/index.db
-rw-r--r--  1 macbok  staff   123M  1 24 15:58 index.db
```

#### 4. 重建索引时清空UI表格

**文件**: `frontend/src/App.svelte`

**问题**: 用户点击"重建索引"时，上一个索引的搜索结果仍然显示在界面上

**解决方案**:

在 `indexing-start` 事件处理中添加：
```javascript
// 清空搜索结果表格，还原到初始状态
searchResults = []
totalCount = 0
query = ''
```

**代码位置**: `App.svelte:513-515`

**效果**:
- 点击重建索引后，搜索框和结果表格立即清空
- 用户体验更清晰，不会混淆新旧索引

---

### 性能数据对比

#### 批量 INSERT 优化效果（之前已完成）
| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 扫描时间 | 109秒 | 119秒 | -10秒（正常波动） |
| 导入时间 | 150秒 | 108秒 | **+42秒（28%提升）** |
| 总耗时 | 305秒 | 228秒 | **+77秒（25%提升）** |

#### 数据库空间优化效果
| 指标 | 优化前 | 优化后 | 减少 |
|------|--------|--------|------|
| 数据库大小 | 2.9GB | 123MB | **95%** |
| 空闲页 | 722,577 | ~0 | ~100% |
| VACUUM 耗时 | - | ~1-2秒 | 可接受 |

---

### 技术要点

1. **时间戳格式**: 使用 `15:04:05` 格式（HH:MM:SS），简洁清晰
2. **日志级别**: 保留重要的日志，删除频繁的细节日志
3. **空间回收**: VACUUM 是 SQLite 的标准操作，安全可靠
4. **UI 同步**: 使用事件驱动确保 UI 状态与后端同步

---

### 相关文件

- `mac-search-app/indexer.go` - 日志函数、VACUUM 逻辑
- `mac-search-app/frontend/src/App.svelte` - UI 清空逻辑

---

### 数据库信息

- **位置**: `~/.mac-search-app/index.db`
- **优化后大小**: ~123MB (236,085 条记录)
- **格式**: SQLite
- **索引**: path, name, ext 三个字段都有索引
- **维护**: 每次重建索引后自动 VACUUM

---

## 2026-01-24 (上午) - 批量扫描与导入优化

日期：2026-01-24

## 0. 全盘扫描性能优化（重大改进）

### 问题
APP扫描全盘超过10分钟都没完成，而main.go扫描全盘只需要约2分钟。性能差距巨大（5倍以上）。

### 根本原因
**APP在遇到权限错误时会调用 `sudo ls -la`**，导致：
1. **大量外部进程调用**：每个无权限目录都执行 `echo 'password' | sudo -S ls -la`
2. **串行化处理**：`sudoSem` 限制为1，同一时间只能有一个sudo调用
3. **每次都强制GC**：`runtime.GC()` 调用严重拖慢速度
4. **复杂的输出解析**：需要解析 `ls -la` 的文本输出

### 解决方案
**参考 main.go，移除所有 sudo 相关逻辑，遇到权限错误直接跳过**

**文件**: `indexer.go`

**代码位置**: `indexer.go:621-632`

**修改前** (621-727行，约107行代码):
```go
if err != nil {
    // 记录 ReadDir 错误，特别是权限错误
    errStr := err.Error()
    isPermissionError := strings.Contains(errStr, "permission denied") ||
        strings.Contains(errStr, "operation not permitted")

    // 如果是权限错误，尝试使用 sudo 读取目录
    if isPermissionError {
        sudoEntries, sudoErr := idx.readDirWithSudo(dirPath)
        // ... 100多行处理 sudo 结果的代码
        runtime.GC()  // 强制GC！
        return
    }
    // ...
    return
}
```

**修改后** (621-632行，仅12行):
```go
if err != nil {
    // 参考 main.go: 遇到错误直接跳过，不尝试使用 sudo
    errStr := err.Error()
    isBadFileDescriptor := strings.Contains(errStr, "bad file descriptor")

    if !isBadFileDescriptor && debugLog != nil {
        debugLog.WriteString(fmt.Sprintf("[ERROR] 无法读取目录 %s: %v\n", dirPath, err))
    }
    return
}
```

**对比 main.go** (main.go:226-242):
```go
entries, err := os.ReadDir(dirPath)
if err != nil {
    errStr := err.Error()
    isBadFileDescriptor := strings.Contains(errStr, "bad file descriptor")

    if !isBadFileDescriptor {
        s.errorCount.Add(1)
        if s.options.ShowErrors || isTooManyFiles {
            fmt.Fprintf(os.Stderr, "\n⚠️  无法读取目录 %s: %v\n", dirPath, err)
        }
    }
    return  // 直接跳过！
}
```

**效果**:
- **预期性能提升**: 5倍以上（从 10+ 分钟降到约 2 分钟）
- **代码简化**: 删除约 100 行 sudo 相关代码
- **无需密码**: 不再需要用户输入 sudo 密码
- **更稳定**: 减少外部进程调用和潜在的错误

**后续清理**:
需要删除以下不再使用的代码（未完成）:
- `readDirWithSudo()` 函数
- `sudoEntry` 结构体
- `sudoSem` 信号量
- `sudoPassword` 相关字段和方法
- `SetSudoPassword()`, `HasSudoPassword()`, `getSudoPassword()` 方法
- `app.go` 中的 `SetSudoPassword()`, `HasSudoPassword()` 方法
- 前端的密码输入界面

---

## 1. 排除路径处理优化

### 问题
在 macOS 中，`/Volumes/MacExtDisk` 在扫描根目录时会以 `/System/Volumes/Data/Volumes/MacExtDisk` 的形式出现（firmlink 机制），导致用户配置的排除路径无法生效。

### 解决方案
**文件**: `indexer.go`

1. **使用 `realpath` 命令解析路径**
   - 在 `loadExcludePaths()` 和 `SetExcludePaths()` 中，使用 `realpath` 命令获取规范路径
   - 将原始路径和 realpath 解析后的路径都加入排除列表

2. **优化性能**
   - 在 `shouldExcludePath()` 中添加 `realpathCache`（`sync.Map`）缓存 realpath 结果
   - 只对 `/System/Volumes/Data/` 开头的路径调用 `realpath`，避免对所有路径都执行外部命令
   - 先进行快速字符串匹配，只在必要时才调用 `realpath`

**代码位置**:
- `indexer.go:1393-1405` - `loadExcludePaths()` 使用 realpath
- `indexer.go:1418-1441` - `SetExcludePaths()` 使用 realpath 并记录日志
- `indexer.go:1489-1531` - `shouldExcludePath()` 带缓存的路径检查

**效果**:
- 用户排除 `/Volumes/MacExtDisk` 后，扫描根目录时遇到 `/System/Volumes/Data/Volumes/MacExtDisk` 也会被正确排除
- 通过缓存和条件检查，避免性能问题

---

## 2. 索引启动状态管理优化

### 问题
用户连续点击"重建索引"3次后，第3次没有出现进度条和"停止索引"按钮。原因是前端手动设置 `isIndexing = true`，但后端因为锁阻塞或其他原因没有发送 `indexing-start` 事件，导致状态不同步。

### 解决方案
**文件**: `frontend/src/App.svelte`

改为**完全事件驱动**的状态管理：

1. **前端不再手动设置 `isIndexing`**
   - 调用 `RebuildIndex()` 后，不立即设置 `isIndexing = true`
   - 等待后端发送 `indexing-start` 事件后，才设置 `isIndexing = true`

2. **添加保护检查**
   - 在 `selectAndRebuild()` 开始时检查 `isIndexing` 状态
   - 如果已经在索引中，直接返回，避免重复触发

**代码位置**:
- `App.svelte:261-289` - `selectAndRebuild()` 不手动设置 isIndexing
- `App.svelte:291-316` - `confirmSudoPassword()` 同样不手动设置
- `App.svelte:495-513` - `indexing-start` 事件监听设置 isIndexing

**效果**:
- 状态完全由后端事件控制，避免前后端状态不一致
- 用户点击后立即看到"停止索引"按钮（当后端发送事件时）

---

## 3. 目录大小计算与进度条显示优化

### 问题
1. 使用 `du` 命令计算目录大小时，大目录（如根目录）可能需要很长时间，导致用户感觉"卡住"
2. 之前错误地使用 `syscall.Statfs()` 替代 `du`，但 Statfs 返回的是整个磁盘大小，对于子目录完全不准确

### 解决方案
**文件**: `app.go`

采用**智能进度条策略**：

1. **后台异步执行 `du -sk` 命令**（不阻塞索引启动）
   - 点击重建索引后，立即发送 `indexing-start` 事件
   - 后台 goroutine 执行 `du` 命令（最多等待60秒）

2. **5秒阈值判断**
   - 如果 `du` 在 5 秒内完成 → 小目录，不显示进度条（`diskUsedSize = 0`）
   - 如果 5 秒后 `du` 还没完成 → 大目录，显示假进度条（约1%）

3. **假进度机制**
   - 5秒后 `du` 未完成时，设置 `diskUsedSize = totalDisk * 100`，让进度显示约1%
   - 立即通知前端，进度条出现

4. **真实进度更新**
   - `du` 真正完成后（如20秒），如果用时 ≥ 5秒，更新为真实的 `diskUsedSize`
   - 通知前端，进度条更新为真实百分比

**代码位置**:
- `app.go:88-195` - `BuildIndex()` 函数的完整逻辑
- `app.go:99-163` - 目录大小计算和进度条策略
- `app.go:165-194` - 5秒定时器设置假进度

**关键代码片段**:
```go
// 后台计算 du
go func() {
    duStart := time.Now()
    cmd := exec.CommandContext(ctx, "du", "-sk", path)
    output, err := cmd.Output()
    duElapsed := time.Since(duStart)

    if err == nil {
        if duElapsed < 5*time.Second {
            // 小目录，不显示进度条
        } else {
            // 大目录，更新为真实进度
            diskUsedSize = sizeKB * 1024
            // 通知前端
        }
    }
}()

// 5秒定时器
go func() {
    select {
    case <-duCompleted:
        return
    case <-time.After(5 * time.Second):
        // 设置假进度（~1%）
        diskUsedSize = currentTotalDisk * 100
        // 通知前端
    }
}()
```

**效果**:
- **小目录**（如 Desktop，du 2秒完成）：无进度条，界面简洁
- **大目录**（如根目录，du 30秒完成）：5秒后进度条出现显示 ~1%，du 完成后更新为真实进度
- 用户体验流畅，不会感觉"卡住"

---

## 4. 其他改进

### 4.1 删除未使用的导入
- 删除 `app.go` 中未使用的 `syscall` 导入（改用完全异步的 du 方案后不再需要）

### 4.2 错误处理增强
- `RebuildIndex()` 调用添加 try-catch，失败时重置 `isIndexing` 状态
- 在前端添加错误提示，提升用户体验

---

## 技术要点总结

### 性能优化
1. **路径检查缓存**: `realpathCache` 缓存避免重复调用外部命令
2. **条件调用**: 只对特定前缀（`/System/Volumes/Data/`）调用 realpath
3. **异步计算**: du 命令在后台执行，不阻塞主流程

### 用户体验优化
1. **立即反馈**: 点击重建索引后立即显示"停止索引"按钮
2. **智能进度条**: 小目录不显示进度条，大目录5秒后显示
3. **假进度机制**: 大目录在 du 完成前先显示1%，给用户反馈

### 状态管理
1. **事件驱动**: 完全由后端事件控制前端状态
2. **状态同步**: 避免前后端状态不一致
3. **并发保护**: 使用 `sync.RWMutex` 保护共享变量

---

## 相关文件

- `mac-search-app/app.go` - 主要业务逻辑
- `mac-search-app/indexer.go` - 索引器和排除路径处理
- `mac-search-app/frontend/src/App.svelte` - 前端界面和状态管理

---

## 测试建议

1. **排除路径测试**
   - 添加 `/Volumes/XXX` 到排除列表
   - 扫描根目录 `/`，验证 `/System/Volumes/Data/Volumes/XXX` 被正确排除

2. **进度条测试**
   - 测试小目录（如 Desktop）：应该无进度条
   - 测试大目录（如 /Users）：5秒后应该出现进度条

3. **连续操作测试**
   - 连续多次点击"重建索引"
   - 验证状态切换正确，不会出现卡住或按钮不显示的情况

---

## 后续优化方向

1. **进度条精度**
   - 考虑使用 `du` 的 `--apparent-size` 或其他选项获取更准确的估算
   - 或者完全放弃百分比，只显示绝对数量和速度

2. **取消 du 命令**
   - 当用户停止索引时，尝试终止后台的 du 进程

3. **缓存优化**
   - 可以将 realpath 缓存持久化，避免重启后重新计算
