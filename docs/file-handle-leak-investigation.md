# 文件句柄泄漏问题调查与修复

## 问题描述

在 mac-search-app 索引构建过程中，出现严重的文件句柄泄漏问题：
- **现象**：`lsof -p` 显示打开的文件数量高达 61,495 个
- **影响**：触发 "too many open files" 错误，导致无法扫描大型目录
- **观察**：关闭应用后句柄立即释放，说明是应用层面的引用问题

## 调查过程

### 1. 初步分析（错误方向）

**假设**：`os.ReadDir` 打开目录但未关闭文件句柄

**尝试的修复**：
- 使用 `os.Open` + `dir.Close()` 显式关闭目录
- 立即清空 `entries` 切片并调用 `runtime.GC()`
- 降低信号量容量，减少并发

**结果**：完全无效，文件句柄数量没有任何变化

### 2. 关键线索

通过 `lsof` 输出分析：
```bash
ps aux|grep mac-search-app.app | awk '{print $2}' | xargs lsof -p | grep "DIR" | wc -l
# 输出：11388 个目录句柄
```

发现：
- 有 **11,388 个 DIR（目录）** 文件描述符处于打开状态
- 这些目录对应 `/Applications` 下的所有子目录
- 文件描述符类型为 `r`（只读）

### 3. 时间点分析（突破口）

用户观察到关键现象：
```
02:20:43 - 70 个句柄（正常）
02:20:46 - 23,904 个句柄（突然暴增！）
02:20:50 - 47,330 个句柄
02:20:52 - 61,495 个句柄（稳定）
```

**关键发现**：
- 文件句柄暴增**不是在扫描过程中**
- 而是在**索引完成后的某个操作**
- 暴增发生在 3 秒内（02:20:43 → 02:20:46）

### 4. 真相大白

检查 `app.go` 的 `BuildIndex` 函数，发现索引完成后启动了 **文件监听器**（Watcher）：

```go
// app.go:160-167
watcher, err := NewWatcher(a.indexer, path)
if err != nil {
    return err
}

if err := watcher.Start(); err != nil {
    return err
}
```

查看 `watcher.go` 的 `Start()` 实现：

```go
// watcher.go:43-57
func (w *Watcher) Start() error {
    // 递归添加所有目录到监听列表
    err := filepath.Walk(w.rootPath, func(path string, info os.FileInfo, err error) error {
        if info.IsDir() && !w.indexer.shouldSkip(path) {
            if err := w.watcher.Add(path); err != nil {
                log.Printf("无法监听目录 %s: %v", path, err)
            }
        }
        return nil
    })
}
```

**罪魁祸首**：
- `fsnotify.Watcher.Add(path)` 为**每个目录**打开一个文件描述符来监听文件系统事件
- 监听 `/Applications` 的 11,388 个子目录 = 11,388 个目录句柄
- 加上其他系统文件，总计 61,495 个句柄

## 根本原因

**这不是 bug，而是 `fsnotify` 库的设计特性**：

1. **fsnotify 的工作原理**：
   - 使用 `inotify`（Linux）或 `kqueue`（macOS）监听文件系统事件
   - 在 macOS 上，每个被监听的目录需要一个文件描述符
   - 监听大型目录树会消耗大量文件描述符

2. **为什么关闭 APP 后句柄释放**：
   - 文件描述符被 `fsnotify.Watcher` 对象持有
   - 应用关闭时，Watcher 被销毁，文件描述符被操作系统回收
   - 说明不是底层泄漏，而是应用层正常持有

## 解决方案

### 临时方案（已实施）

**禁用文件监听功能**：

```go
// app.go:154-176
fmt.Println("索引构建完成（文件监听已禁用）")
// 注释掉 Watcher 的启动代码
```

**效果**：
- 索引完成后文件句柄保持在 70-100 个
- 不再出现 "too many open files" 错误
- 可以正常扫描大型目录

### 长期方案（建议）

如果需要恢复文件监听功能，可以考虑：

#### 方案 1：使用 macOS FSEvents API
```go
// 使用 FSEvents 替代 fsnotify
// FSEvents 只需要监听根目录，不需要为每个子目录打开句柄
import "github.com/fsnotify/fsevents"
```

**优点**：
- 只监听根目录，文件句柄数量固定
- 性能更好，延迟更低
- macOS 原生 API，更加可靠

#### 方案 2：选择性监听
```go
// 只监听根目录，不递归监听所有子目录
func (w *Watcher) Start() error {
    // 只添加根目录
    return w.watcher.Add(w.rootPath)
}
```

**优点**：
- 大幅减少文件句柄数量
- 可以监听到新建/删除的子目录

**缺点**：
- 无法监听到深层子目录的文件变化
- 需要手动处理新建目录的递归监听

#### 方案 3：可选功能
```go
// 让用户选择是否启用文件监听
type IndexOptions struct {
    EnableWatcher bool
    MaxWatchDirs  int  // 最大监听目录数
}
```

**优点**：
- 用户可以根据需求开启/关闭
- 可以设置监听目录数量限制

## 其他优化

### 1. 进度百分比计算修复

**问题**：进度条使用整个磁盘的已使用空间作为分母

**修复**：使用 `du -sk` 命令获取扫描目录的实际大小

```go
// app.go:103-120
cmd := exec.Command("du", "-sk", path)
output, duErr := cmd.Output()
if duErr == nil {
    var sizeKB int64
    fmt.Sscanf(string(output), "%d", &sizeKB)
    diskUsedSize = sizeKB * 1024 // 转换为字节
}
```

### 2. 并发控制优化

**调整信号量容量**：

```go
// indexer.go:293-298
maxConcurrentFileOps := runtime.NumCPU() * 2
infoSemaphore := make(chan struct{}, maxConcurrentFileOps)
readDirSemaphore := make(chan struct{}, maxConcurrentFileOps)
```

**从 1000 降低到 CPU 核心数 * 2**，避免过度并发。

### 3. 内存优化

**使用轻量级数据结构**：

```go
// indexer.go:668-695
type SimpleEntry struct {
    name      string
    isDir     bool
    isSymlink bool
}

// 立即提取数据并释放 DirEntry
simpleEntries := make([]SimpleEntry, 0, len(entries))
for _, entry := range entries {
    simpleEntries = append(simpleEntries, SimpleEntry{
        name:      entry.Name(),
        isDir:     entry.IsDir(),
        isSymlink: entry.Type()&os.ModeSymlink != 0,
    })
}
// 清空 entries，释放资源
for i := range entries {
    entries[i] = nil
}
entries = nil
runtime.GC()
```

## 经验总结

### 1. 调试技巧

- **使用 `lsof` 分析文件句柄**：`lsof -p <pid> | grep DIR`
- **观察时间点**：问题发生的精确时刻往往是关键线索
- **分段测试**：隔离问题是在扫描中还是扫描后
- **检查库的文档**：了解第三方库的资源使用特性

### 2. 设计教训

- **文件监听需谨慎**：监听大型目录树会消耗大量系统资源
- **资源限制考虑**：设计时要考虑系统的 ulimit 限制
- **可选功能原则**：资源密集型功能应该是可选的
- **平台差异注意**：不同操作系统的文件监听机制差异很大

### 3. 性能优化原则

- **测量优先**：先测量再优化，不要凭感觉
- **权衡取舍**：功能丰富度 vs 资源消耗
- **渐进优化**：先解决最大的瓶颈，再优化细节

## 相关链接

- [fsnotify GitHub](https://github.com/fsnotify/fsnotify)
- [macOS FSEvents](https://developer.apple.com/documentation/coreservices/file_system_events)
- [ulimit 文件描述符限制](https://www.freedesktop.org/software/systemd/man/systemd.exec.html#LimitNOFILE=)

## 修改记录

| 日期 | 修改内容 | 作者 |
|------|---------|------|
| 2026-01-24 | 初始版本，记录文件句柄泄漏调查过程 | Claude |
| 2026-01-24 | 禁用文件监听功能，修复文件句柄泄漏 | Claude |
