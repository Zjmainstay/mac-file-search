# 性能优化与问题修复日志

## 2026-01-24 晚间优化记录

### 工作总结

本次晚间优化共完成 6 项重要改进，显著提升了应用的性能和用户体验：

#### 1. 设置弹窗响应速度优化（立即响应）
- **问题**：点击设置按钮等待 1-2 秒才弹出窗口
- **原因**：`GetIndexedPaths()` 在 318 万条记录上执行 GROUP BY 聚合查询
- **解决**：创建 `indexed_paths` 专用表存储预计算的统计信息，查询时间从秒级降到毫秒级
- **效果**：弹窗立即打开，异步加载数据

#### 2. 删除按钮功能修复
- **问题**：点击删除已索引路径按钮完全没反应
- **原因**：`confirm()` 对话框在 Wails 生产环境中不工作，阻塞了代码执行
- **解决**：实现自定义确认对话框组件
- **效果**：删除功能正常工作

#### 3. 删除索引性能优化（22秒 → 2秒，-91%）
- **问题**：删除索引后关闭 APP 卡住 20-30 秒
- **原因**：VACUUM 操作重建整个数据库文件耗时 20 秒
- **解决**：移除 VACUUM 操作（空间会被重用），移除 shutdown 等待清理任务逻辑
- **效果**：删除操作从 22 秒降到 2 秒，APP 立即退出不卡顿

#### 4. 排除路径交互优化
- **问题**：添加/删除排除路径后需要点击"保存"按钮，交互繁琐
- **解决**：添加/删除操作立即自动保存，移除"取消"和"保存"按钮
- **效果**：交互更加简洁直观

#### 5. 搜索性能优化（智能路径/文件名识别）
- **问题**：搜索速度慢，无法精确搜索路径
- **解决**：
  - 关键词包含 `/` → 搜索路径字段
  - 关键词不含 `/` → 搜索文件名字段（使用 idx_name 索引）
  - 支持混合搜索：`CoreServices/ Contents/` 匹配同时包含两个片段的路径
- **效果**：文件名搜索达到毫秒级，路径搜索更精确

#### 6. 搜索输入防抖优化（查询减少 75-90%）
- **问题**：输入搜索词时每个字母都触发一次查询（输入 "User" 触发 4 次查询）
- **解决**：实现 500ms 防抖延迟，用户停止输入后才搜索
- **效果**：
  - 输入 4 个字母：查询次数从 4 次降到 1 次（-75%）
  - 输入 10 个字母：查询次数从 10 次降到 1 次（-90%）
  - 大幅减少数据库负载

#### 性能提升对比表

| 操作 | 优化前 | 优化后 | 改进幅度 |
|------|--------|--------|----------|
| 设置弹窗打开 | 1-2 秒 | 立即 | 100% |
| 删除索引耗时 | 22 秒 | 2 秒 | -91% |
| APP 退出 | 卡 20-30 秒 | 立即 | 100% |
| 搜索查询次数（输入4字母） | 4 次 | 1 次 | -75% |
| 文件名搜索速度 | 秒级 | 毫秒级 | 90%+ |

---

### 1. 搜索输入防抖优化

**问题**：输入搜索关键词时，每输入一个字母就触发一次搜索，导致大量不必要的数据库查询

**原因**：
- Svelte 响应式语句 `$: if (searchQuery)` 每次输入都立即执行
- 输入 "User" 会触发 4 次查询（U → Us → Use → User）
- 在 318万条记录的数据库中，每次查询都有性能开销

**解决方案**：实现搜索防抖（debounce）

**App.svelte:48-50 & 253-263**
```javascript
// 防抖配置
let searchDebounceTimer = null
const SEARCH_DEBOUNCE_DELAY = 500  // 500ms 延迟

// 响应式搜索（带防抖）
$: if (searchQuery !== undefined || useRegex !== undefined) {
    // 清除之前的定时器
    if (searchDebounceTimer) {
        clearTimeout(searchDebounceTimer)
    }

    // 设置新的定时器：500ms后执行搜索
    searchDebounceTimer = setTimeout(() => {
        performSearch()
    }, SEARCH_DEBOUNCE_DELAY)
}
```

**工作原理**：

**优化前**（每次输入都搜索）：
```
用户输入 "User"：
U     → 立即搜索 "U"      (第1次查询)
Us    → 立即搜索 "Us"     (第2次查询)
Use   → 立即搜索 "Use"    (第3次查询)
User  → 立即搜索 "User"   (第4次查询)
总共：4次数据库查询
```

**优化后**（防抖延迟）：
```
用户输入 "User"：
U     → 等待500ms（定时器启动）
Us    → 取消上次定时器，重新等待500ms
Use   → 取消上次定时器，重新等待500ms
User  → 取消上次定时器，重新等待500ms
        ↓ 停止输入
        500ms后执行搜索 "User"
总共：1次数据库查询（节省75%）
```

**效果**：
- 输入4个字母：查询次数从 4次 降到 1次（节省75%）
- 输入10个字母：查询次数从 10次 降到 1次（节省90%）
- 用户停止输入500ms后自动搜索
- 大幅减少数据库负载

---

### 2. 设置弹窗响应速度优化

**问题**：点击设置按钮弹窗慢（等待1-2秒）

**原因**：
- `openSettings()` 函数在显示弹窗前等待两个后端API调用
- `GetIndexedPaths()` 执行 GROUP BY 查询扫描整个 files 表（318万条记录）
- GROUP BY 聚合计算耗时较长

**解决方案**：

#### 1.1 创建 indexed_paths 专用表

**indexer.go:152-164**
```sql
CREATE TABLE IF NOT EXISTS indexed_paths (
    path TEXT PRIMARY KEY,
    file_count INTEGER NOT NULL DEFAULT 0,
    dir_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
```

#### 1.2 BuildIndex 完成时自动更新统计

**indexer.go:428-444 & 773-787**
```go
// 更新 indexed_paths 表统计信息
fileCount := idx.fileCount.Load()
dirCount := idx.dirCount.Load()
now := time.Now().Unix()
_, err = idx.db.Exec(`
    INSERT INTO indexed_paths (path, file_count, dir_count, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(path) DO UPDATE SET
        file_count = excluded.file_count,
        dir_count = excluded.dir_count,
        updated_at = excluded.updated_at
`, rootPath, fileCount, dirCount, now, now)
```

#### 1.3 GetIndexedPaths 直接查表

**indexer.go:2308-2330**
```go
// 优化前：GROUP BY 聚合查询
SELECT indexed_path,
       SUM(CASE WHEN is_dir = 0 THEN 1 ELSE 0 END) as file_count,
       SUM(CASE WHEN is_dir = 1 THEN 1 ELSE 0 END) as dir_count
FROM files
WHERE indexed_path != ''
GROUP BY indexed_path

// 优化后：直接查表
SELECT path, file_count, dir_count
FROM indexed_paths
ORDER BY path
```

#### 1.4 前端异步加载

**App.svelte:381-405**
```javascript
async function openSettings() {
    // 先显示弹窗
    showSettings = true

    // 然后异步加载数据
    const savedPaths = await GetExcludePaths()
    excludePaths = savedPaths || []

    const indexed = await GetIndexedPaths()  // 现在超快！
    indexedPaths = indexed || []
}
```

**效果**：
- 弹窗立即打开（不等待数据加载）
- GetIndexedPaths 查询从 O(n) 降到 O(m)（n=文件数，m=索引路径数）
- 查询时间从秒级降到毫秒级

---

### 2. 删除索引按钮问题修复

**问题**：点击删除按钮完全没反应

**原因**：`confirm()` 对话框在 Wails 应用中不工作，阻塞了后续代码执行

**解决方案**：实现自定义确认对话框

**App.svelte:1003-1020**
```javascript
// 添加状态变量
let showDeleteConfirm = false
let deleteTargetPath = ''

// 删除函数
function deleteIndexedPath(path) {
    deleteTargetPath = path
    showDeleteConfirm = true  // 显示自定义对话框
}

// 自定义确认对话框（HTML）
<div class="sudo-overlay">
  <div class="sudo-dialog">
    <div class="sudo-header">确认删除</div>
    <div class="sudo-body">
      确定要删除索引路径 "{deleteTargetPath}" 吗？
    </div>
    <div class="sudo-footer">
      <button on:click={cancelDelete}>取消</button>
      <button on:click={confirmDelete}>确定删除</button>
    </div>
  </div>
</div>
```

**效果**：删除按钮正常工作，弹出自定义确认对话框

---

### 3. 删除索引性能优化

**问题**：删除索引后关闭APP会卡住 20-30 秒

**原因分析**：
```
删除 /Applications (236182 条记录):
- DELETE: 1.67秒  ← 快
- VACUUM: 20.29秒 ← 慢！重建整个数据库文件
- checkpoint: 0.31秒 ← 快
总计: 22.28秒
```

**VACUUM 为什么慢？**
- 重建整个数据库文件
- 复制所有剩余记录（318万条）到新文件
- 即使只删除 3万条，也要复制剩下的 315万条

**解决方案1：去掉 VACUUM**

**indexer.go:2340-2403**
```go
// 优化前：DELETE + VACUUM + checkpoint
cleanupTaskFunc(func() {
    // DELETE (1.67秒)
    idx.db.Exec("DELETE FROM files WHERE indexed_path = ?", path)

    // VACUUM (20秒)
    idx.db.Exec("VACUUM")

    // checkpoint (0.31秒)
    idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)")
})

// 优化后：DELETE + checkpoint（去掉VACUUM）
cleanupTaskFunc(func() {
    // DELETE (1.67秒)
    idx.db.Exec("DELETE FROM files WHERE indexed_path = ?", path)

    // checkpoint (0.31秒)
    idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)")

    // 数据库文件不会立即缩小，但空间会被重用
})
```

**解决方案2：去掉等待清理任务**

**app.go:129-146**
```go
// 优化前：等待清理任务完成
func (a *App) shutdown(ctx context.Context) {
    close(a.cleanupTasksCh)

    // 等待最多30秒
    select {
    case <-a.cleanupDone:
        fmt.Println("清理任务已完成")
    case <-time.After(30 * time.Second):
        fmt.Println("超时强制退出")
    }
}

// 优化后：不等待，立即退出
func (a *App) shutdown(ctx context.Context) {
    close(a.cleanupTasksCh)
    // 不等待，直接退出
}
```

**效果**：
- 删除任务耗时：22秒 → 2秒（-91%）
- 关闭APP：立即退出，不卡顿
- 数据库空间在下次插入时重用

---

### 4. 排除路径交互优化

**问题**：需要点击"保存"按钮才能保存，交互繁琐

**解决方案**：添加/删除立即自动保存

**App.svelte:437-466**
```javascript
// 优化前：需要点击保存按钮
function addExcludePath() {
    excludePaths = [...excludePaths, newExcludePath.trim()]
    // 不保存，需要点击"保存"按钮
}

// 优化后：自动保存
async function addExcludePath() {
    excludePaths = [...excludePaths, newExcludePath.trim()]
    newExcludePath = ''

    // 立即保存到后端
    await SetExcludePaths(excludePaths)
}

async function removeExcludePath(index) {
    excludePaths = excludePaths.filter((_, i) => i !== index)

    // 立即保存到后端
    await SetExcludePaths(excludePaths)
}
```

**移除的元素**：
- 删除了 `saveSettings()` 函数
- 删除了设置弹窗底部的"取消"和"保存"按钮

**效果**：
- 添加/删除排除路径立即生效
- 交互更简洁直观

---

### 5. 搜索性能优化

**问题**：搜索速度慢，尤其是在 318万条记录的数据库中

**原因分析**：
```sql
-- 优化前：全表扫描
WHERE name LIKE '%keyword%' OR path LIKE '%keyword%'

执行计划：SCAN files (全表扫描318万条)
```

**解决方案**：智能区分路径搜索和文件名搜索

**indexer.go:1268-1320**
```go
// 搜索规则
for _, kw := range keywords {
    if strings.Contains(kw, "/") {
        // 包含斜杠 → 搜索路径
        pathConditions = append(pathConditions, "path LIKE ?")
        args = append(args, "%"+kw+"%")
    } else {
        // 不包含斜杠 → 搜索文件名（使用索引）
        nameConditions = append(nameConditions, "name LIKE ?")
        args = append(args, "%"+kw+"%")
    }
}
```

**搜索示例**：

| 输入 | 搜索方式 | 性能 |
|------|----------|------|
| `myfile` | `WHERE name LIKE '%myfile%'` | 快（使用idx_name索引） |
| `Applications/` | `WHERE path LIKE '%Applications/%'` | 慢（全表扫描，但精确） |
| `CoreServices/ Contents/` | `WHERE path LIKE '%CoreServices/%' AND path LIKE '%Contents/%'` | 慢但精确 |
| `System/ SS` | `WHERE path LIKE '%System/%' AND name LIKE '%SS%'` | 混合搜索 |

**效果**：
- 纯文件名搜索：毫秒级（使用索引）
- 路径搜索：仍需全表扫描，但结果更精确
- 支持路径+文件名混合搜索

---

## 2026-01-24 上午优化记录

### 1. 正则搜索修复

**问题**：用户搜索 `.avi$` 时，匹配了不以 `.avi` 结尾的文件（如 `JavaVirtualMachines`）

**原因**：
- 旧代码自动添加 `(?i)` 前缀用于大小写不敏感
- 但在某个版本中被错误删除

**解决方案**：
```go
// 恢复智能添加 (?i) 前缀
if !strings.HasPrefix(keyword, "(?i)") && !strings.HasPrefix(keyword, "(?-i)") {
    regexPattern = "(?i)" + keyword
}
```

**效果**：
- 默认不区分大小写
- 用户可以通过 `(?-i)` 强制区分大小写
- 用户可以手动添加 `(?i)` 避免重复

---

### 2. WAL 文件过大问题

**问题**：发现 `index.db-wal` 文件达到 1.1GB，占用大量磁盘空间

**原因**：
- 索引完成后没有执行 WAL checkpoint
- VACUUM 操作没有执行 checkpoint
- 数据一直在 WAL 中累积

**解决方案**：实现后台清理任务管理系统

#### 2.1 后台清理任务架构

```go
// App 层面的清理任务管理
type App struct {
    cleanupTasksCh chan func()   // 清理任务通道（缓冲100个）
    cleanupDone    chan struct{} // 清理完成信号
}

// 后台清理任务处理器（单线程串行执行）
func (a *App) cleanupTaskWorker() {
    for task := range a.cleanupTasksCh {
        task() // 执行清理任务
    }
    close(a.cleanupDone)
}
```

#### 2.2 Indexer 提交任务

```go
type Indexer struct {
    cleanupTaskFunc func(func())  // 提交清理任务的回调
}

// 索引完成后提交 checkpoint 任务
if idx.cleanupTaskFunc != nil {
    idx.cleanupTaskFunc(func() {
        idx.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
    })
}
```

#### 2.3 优雅退出

```go
func (a *App) shutdown(ctx context.Context) {
    // 关闭通道，不再接受新任务
    close(a.cleanupTasksCh)

    // 等待所有清理任务完成（最多30秒）
    select {
    case <-a.cleanupDone:
        // 任务完成
    case <-time.After(30 * time.Second):
        // 超时强制退出
    }

    // 关闭数据库
    a.indexer.Close()
}
```

#### 2.4 清理日志

所有清理任务写入 `~/.mac-search-app/cleanup.log`：
```
[21:37:11] 任务已提交到队列
[21:37:11] 开始执行第 1 个任务
[21:37:11] 正在后台回收数据库空间...
[21:37:47] 回收空间完成，耗时: 36.10秒
[21:37:47] 正在执行 WAL checkpoint...
[21:37:50] WAL checkpoint 完成: busy=0, log=XXX, checkpointed=XXX, 耗时: 3.21秒
[21:37:50] WAL 文件大小: 0.00 MB
[21:37:50] 第 1 个任务完成，耗时: 39.31秒
```

---

### 3. 索引性能优化

**问题**：重建 /Applications 索引耗时 31 秒，其中有 12 秒时间不明

**分析**：
```
21:40:23 开始
21:40:23 重置标志
21:40:35 排除路径列表 ← 12秒空白！
21:40:42 删除完成（6秒）
21:40:48 扫描完成（6秒）
21:40:54 插入完成（6秒）
```

**原因**：`du -sk /Applications` 命令在 app.go 中同步执行（虽然在 goroutine 中，但占用了大量系统资源）

**解决方案**：将 du 改为完全异步，不阻塞索引

#### 3.1 优化前

```go
// 同步等待 du 完成
cmd := exec.Command("du", "-sk", path)
output, err := cmd.Output()  // 阻塞 12 秒
```

#### 3.2 优化后

```go
// 后台异步执行 du
go func() {
    cmd := exec.CommandContext(ctx, "du", "-sk", path)
    output, err := cmd.Output()

    if err == nil {
        // 计算完成后通知前端
        runtime.EventsEmit(a.ctx, "disk-size-calculated", map[string]interface{}{
            "diskUsedSize": realDiskUsedSize,
        })
    }
}()

// 索引立即开始，不等待 du
a.indexer.BuildIndex(path, notifyStart)
```

#### 3.3 前端进度条优化

```javascript
// 监听目录大小计算完成事件
EventsOn('disk-size-calculated', (data) => {
    diskUsedSize = data.diskUsedSize  // 更新后进度条自动显示
})
```

**效果**：
- 索引时间从 31 秒降到 **18 秒**（节省 13 秒）
- 前几秒不显示进度条（只显示文件数和速度）
- du 完成后（约 12 秒），进度条自动出现
- 用户体验：大目录有进度反馈，小目录快速完成

---

### 4. 清理任务详细说明

#### 4.1 BuildIndex 完成时

```go
// 提交 WAL checkpoint 任务
cleanupTaskFunc(func() {
    var busy, log, checkpointed int
    idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
    // 记录详细日志到 cleanup.log
})
```

#### 4.2 buildIndexWithMacFileScan 完成时

```go
// 提交 VACUUM + checkpoint 任务
cleanupTaskFunc(func() {
    idx.db.Exec("VACUUM")              // 30-60 秒
    idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(...)  // 几秒
    // 记录详细日志到 cleanup.log
})
```

#### 4.3 DeleteIndexedPath 时

```go
// DELETE 同步执行（3-5秒）
idx.db.Exec("DELETE FROM files WHERE indexed_path = ?", path)

// VACUUM + checkpoint 异步执行
cleanupTaskFunc(func() {
    idx.db.Exec("VACUUM")
    idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(...)
})
```

---

## 性能对比

### 索引构建性能

| 操作 | 优化前 | 优化后 | 改进 |
|------|--------|--------|------|
| /Applications 索引 | 31 秒 | 18 秒 | -42% |
| du 命令 | 阻塞 12 秒 | 后台并行 | 不阻塞 |
| 用户等待时间 | 31 秒 | 18 秒 | -13 秒 |

### 删除索引性能

| 操作 | 优化前 | 优化后 | 改进 |
|------|--------|--------|------|
| DELETE | 3-5 秒 | 3-5 秒 | 无变化 |
| VACUUM | 阻塞 30 秒 | 后台执行 | 不阻塞 |
| checkpoint | 阻塞 5 秒 | 后台执行 | 不阻塞 |
| 用户等待时间 | 40 秒 | 5 秒 | -87% |

---

## 观察清理任务

### 日志文件路径

所有日志文件位于：`~/.mac-search-app/`

| 日志文件 | 路径 | 用途 |
|---------|------|------|
| cleanup.log | `~/.mac-search-app/cleanup.log` | 后台清理任务日志（VACUUM、checkpoint） |
| debug.log | `~/.mac-search-app/debug.log` | 索引构建详细日志 |
| performance.log | `~/.mac-search-app/performance.log` | 性能统计报告 |
| index.db | `~/.mac-search-app/index.db` | 主数据库文件 |
| index.db-wal | `~/.mac-search-app/index.db-wal` | WAL 日志文件 |
| index.db-shm | `~/.mac-search-app/index.db-shm` | 共享内存文件 |

### 查看清理日志

```bash
# 实时监控
tail -f ~/.mac-search-app/cleanup.log

# 查看完整日志
cat ~/.mac-search-app/cleanup.log
```

**cleanup.log 示例内容**：
```
[21:36:19] 后台清理任务处理器已启动
[21:37:11] 任务已提交到队列
[21:37:11] 开始执行第 1 个任务
[21:37:11] 正在后台回收数据库空间...
[21:37:47] 回收空间完成，耗时: 36.10秒
[21:37:47] 正在执行 WAL checkpoint...
[21:37:50] WAL checkpoint 完成: busy=0, log=12345, checkpointed=12345, 耗时: 3.21秒
[21:37:50] WAL 文件大小: 0.00 MB
[21:37:50] 第 1 个任务完成，耗时: 39.31秒
```

### 查看构建日志

```bash
# 查看最近的索引构建日志
tail -100 ~/.mac-search-app/debug.log
```

**debug.log 示例内容**：
```
=== BuildIndex 开始: /Applications (时间: 21:40:23) ===
[21:40:23] [RESET] stopFlag: false -> false, fileCount: 0, dirCount: 0
[21:40:23] 磁盘总空间: 245107195904, 已使用: 219427188736 (89.5%)
[21:40:23] [TIMING] 准备保存索引路径
[21:40:23] [TIMING] 索引路径已保存
[21:40:35] [EXCLUDE] 当前排除路径列表 (共 1 个):
[21:40:35]   [0] /Volumes/MacExtDisk
[21:40:36] 准备删除 236182 条记录（路径: /Applications 及其子路径）
[21:40:42] 清空后数据库有 0 条记录
[21:40:42] [STRATEGY] 检测到sudo密码，使用mac-file-search一次性扫描
[21:40:42] [MAC-FILE-SCAN] 扫描路径: /Applications
[21:40:48] [MAC-FILE-SCAN] 扫描完成，耗时: 6.24秒
[21:40:54] [MAC-FILE-SCAN] 解析完成，共236187行，插入236182条，耗时: 5.99秒
[21:40:54] [COMPLETE] 索引构建完成，总耗时: 31.27秒
```

### 查看性能报告

```bash
cat ~/.mac-search-app/performance.log
```

**performance.log 示例内容**：
```
===============================
性能分析报告
===============================
清空数据: 6.12秒
文件扫描+写入: 12.23秒 (65%)
创建索引: 0.15秒 (1%)
-------------------------------
总耗时: 18.50秒
文件数: 196821
目录数: 39361
平均速度: 12768 项/秒
===============================
```

### 查看 WAL 文件大小

```bash
# 查看当前大小
ls -lh ~/.mac-search-app/index.db-wal

# 定期监控
watch -n 5 'ls -lh ~/.mac-search-app/*.db*'
```

### 手动触发 checkpoint（测试用）

```bash
sqlite3 ~/.mac-search-app/index.db "PRAGMA wal_checkpoint(TRUNCATE);"
```

---

## 注意事项

1. **WAL 模式优势**：
   - 支持并发读写
   - 后台清理不影响搜索
   - 崩溃恢复能力强

2. **清理任务执行时机**：
   - 索引完成后：checkpoint（几秒）
   - 使用 mac-file-search：VACUUM + checkpoint（30-60秒）
   - 删除索引：VACUUM + checkpoint（30-60秒）

3. **优雅退出**：
   - 应用退出时等待清理任务完成
   - 最多等待 30 秒
   - 确保数据库完整性

4. **进度显示**：
   - 小目录：只显示文件数和速度
   - 大目录：du 完成后显示进度百分比
   - 不阻塞索引开始

---

## 未来优化方向

1. **增量 checkpoint**：定期执行小规模 checkpoint，避免一次性处理大量数据
2. **智能 VACUUM**：根据删除数据量决定是否执行 VACUUM
3. **进度估算优化**：使用历史数据预测目录大小
4. **并发清理**：多个清理任务可以并行执行（需要评估数据库锁竞争）
