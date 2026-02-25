package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// FileEntry 文件索引条目
type FileEntry struct {
	ID      int64  `json:"id"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
	Ext     string `json:"ext"`
}

// sudoEntry 用于解析 sudo ls 输出的临时结构
type sudoEntry struct {
	name      string
	isDir     bool
	isSymlink bool
	size      int64
	modTime   int64
	ext       string
}

// Indexer 文件索引器
type Indexer struct {
	db              *sql.DB
	scanPath        string
	fileCount       atomic.Int64
	dirCount        atomic.Int64
	totalDisk       atomic.Int64 // 实际磁盘占用总和（用于进度条计算）
	diskUsedSize    int64        // 磁盘已使用空间大小（用于计算进度百分比）
	mu              sync.RWMutex
	onProgress      func(fileCount, dirCount int64, totalDisk int64, elapsed float64)
	onScanFile      func(filePath string)
	buildMu         sync.Mutex    // 确保同一时间只有一个BuildIndex在运行
	stopFlag        atomic.Bool   // 停止标志
	openFiles       atomic.Int64  // 当前打开的文件句柄数量（估算）
	excludePaths    []string      // 要排除的路径列表
	excludeMu       sync.RWMutex  // 保护 excludePaths 的读写锁
	realpathCache   sync.Map      // realpath 缓存，key: 原始路径, value: 规范路径
	sudoSem         chan struct{} // 限制并发调用 readDirWithSudo
	sudoPassword    string        // sudo 密码（内存中保存，不持久化）
	sudoMu          sync.RWMutex  // 保护 sudoPassword 的读写锁
	buildStartTime  time.Time     // 构建开始时间
	cleanupTaskFunc func(func())  // 提交清理任务的回调函数
}

// getOpenFilesCount 获取系统实际打开的文件句柄数量（macOS）
func getOpenFilesCount() int {
	// 使用 lsof 命令获取当前进程打开的文件数量
	cmd := exec.Command("lsof", "-p", strconv.Itoa(os.Getpid()))
	output, err := cmd.Output()
	if err != nil {
		return -1 // 获取失败
	}
	// 计算输出行数（减去标题行）
	lines := strings.Split(string(output), "\n")
	count := len(lines) - 1 // 减去标题行
	if count < 0 {
		count = 0
	}
	return count
}

// FileInfo 内部文件信息结构（用于channel传输）
type FileInfo struct {
	path       string
	name       string
	size       int64
	modTime    int64
	isDir      int
	ext        string
	diskUsage  int64 // 实际磁盘占用（用于进度条计算）
	isHardlink bool  // 是否为硬链接（用于去重）
}

// logWithTime 带时间戳的日志输出
func logWithTime(format string, args ...interface{}) {
	timestamp := time.Now().Format("15:04:05")
	message := fmt.Sprintf(format, args...)
	fmt.Printf("[%s] %s\n", timestamp, message)
}

// logToDebugWithTime 带时间戳写入 debugLog（如果不为 nil）
func logToDebugWithTime(debugLog *os.File, format string, args ...interface{}) {
	if debugLog != nil {
		timestamp := time.Now().Format("15:04:05")
		message := fmt.Sprintf(format, args...)
		debugLog.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, message))
	}
}

// NewIndexer 创建新的索引器
func NewIndexer(dbPath string) (*Indexer, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// SQLite 性能优化
	pragmas := `
	PRAGMA journal_mode=WAL;
	PRAGMA synchronous=NORMAL;
	PRAGMA cache_size=10000;
	PRAGMA temp_store=MEMORY;
	`
	if _, err := db.Exec(pragmas); err != nil {
		return nil, err
	}

	// 创建表
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		size INTEGER NOT NULL,
		mod_time INTEGER NOT NULL,
		is_dir INTEGER NOT NULL,
		ext TEXT NOT NULL,
		indexed_path TEXT NOT NULL DEFAULT ''
	);
	-- 优化：只保留name索引（主要搜索字段）
	-- path已有UNIQUE约束自带索引，且LIKE '%..%'无法利用索引
	-- ext未在WHERE中使用，无需索引
	CREATE INDEX IF NOT EXISTS idx_name ON files(name);
	-- 为多目录索引添加indexed_path索引，用于快速删除和统计特定目录
	CREATE INDEX IF NOT EXISTS idx_indexed_path ON files(indexed_path);
	-- 覆盖索引优化GetIndexedPaths查询（包含indexed_path和is_dir，避免回表）
	CREATE INDEX IF NOT EXISTS idx_indexed_path_isdir ON files(indexed_path, is_dir);

	CREATE TABLE IF NOT EXISTS config (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	-- 已索引路径表（存储统计信息，避免GROUP BY聚合）
	CREATE TABLE IF NOT EXISTS indexed_paths (
		path TEXT PRIMARY KEY,
		file_count INTEGER NOT NULL DEFAULT 0,
		dir_count INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	`

	if _, err := db.Exec(createTableSQL); err != nil {
		return nil, err
	}

	// 迁移：删除旧的多余索引（idx_ext和idx_path）
	// 这些索引会拖慢DELETE操作，且对查询无帮助
	db.Exec("DROP INDEX IF EXISTS idx_ext")
	db.Exec("DROP INDEX IF EXISTS idx_path")

	// 迁移：为旧数据添加indexed_path字段
	// 检查indexed_path列是否存在
	row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('files') WHERE name='indexed_path'")
	var colCount int
	if row.Scan(&colCount) == nil && colCount == 0 {
		// 列不存在，添加它
		if _, err := db.Exec("ALTER TABLE files ADD COLUMN indexed_path TEXT NOT NULL DEFAULT ''"); err != nil {
			// 忽略错误，可能已经存在
		}
		// 创建索引
		db.Exec("CREATE INDEX IF NOT EXISTS idx_indexed_path ON files(indexed_path)")

		// 对于旧数据，如果indexed_path为空，尝试从config读取index_path并填充
		var oldIndexPath string
		if db.QueryRow("SELECT value FROM config WHERE key = 'index_path'").Scan(&oldIndexPath) == nil && oldIndexPath != "" {
			// 将所有空的indexed_path设置为旧的index_path
			db.Exec("UPDATE files SET indexed_path = ? WHERE indexed_path = ''", oldIndexPath)
		}
	}

	idx := &Indexer{
		db:      db,
		sudoSem: make(chan struct{}, 4), // 增加sudo并发度：从1增加到4（经测试main.go用sudo运行整个程序很快，APP慢是因为频繁调用外部sudo命令且完全串行）
	}

	// 加载保存的排除路径（如果失败不影响索引器创建）
	_ = idx.loadExcludePaths()

	return idx, nil
}

// BuildIndex 构建索引
func (idx *Indexer) BuildIndex(rootPath string, notifyStart func()) error {
	// 确保同一时间只有一个BuildIndex在运行（阻塞等待）
	idx.buildMu.Lock()
	defer idx.buildMu.Unlock()

	// 通知前端索引开始
	if notifyStart != nil {
		notifyStart()
	}

	// 创建调试日志
	homeDir, _ := os.UserHomeDir()
	debugLogPath := filepath.Join(homeDir, ".mac-search-app", "debug.log")
	debugLog, _ := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if debugLog != nil {
		defer debugLog.Close()
		debugLog.WriteString(fmt.Sprintf("\n=== BuildIndex 开始: %s (时间: %s) ===\n", rootPath, time.Now().Format("15:04:05")))
	}

	// 首先重置所有状态变量，确保每次构建都从干净状态开始
	// 注意：必须在获取锁之后、创建任何 goroutine 之前重置，确保每次构建都从干净状态开始
	oldStopFlag := idx.stopFlag.Load()
	idx.stopFlag.Store(false) // 必须先重置停止标志，否则可能立即退出
	idx.fileCount.Store(0)
	idx.dirCount.Store(0)
	idx.totalDisk.Store(0) // 重置磁盘占用计数
	idx.openFiles.Store(0) // 重置打开文件句柄计数

	// 验证 stopFlag 确实被重置了（用于调试）
	if debugLog != nil {
		newStopFlag := idx.stopFlag.Load()
		logToDebugWithTime(debugLog, "[RESET] stopFlag: %v -> %v, fileCount: 0, dirCount: 0, totalDisk: 0, openFiles: 0",
			oldStopFlag, newStopFlag)
		if newStopFlag {
			logToDebugWithTime(debugLog, "警告：stopFlag 重置后仍然是 true！")
		}
	}

	startTime := time.Now()
	idx.buildStartTime = startTime // 必须在最开始记录构建开始时间（用于计算 elapsed）
	var perfLog strings.Builder

	idx.scanPath = rootPath

	// 获取磁盘使用情况（参考 main.go）
	var stat syscall.Statfs_t
	if err := syscall.Statfs(rootPath, &stat); err == nil {
		// 已使用空间 = (总块数 - 空闲块数) * 块大小
		idx.diskUsedSize = (int64(stat.Blocks) - int64(stat.Bfree)) * int64(stat.Bsize)
		if debugLog != nil {
			totalSize := int64(stat.Blocks) * int64(stat.Bsize)
			freeSize := int64(stat.Bfree) * int64(stat.Bsize)
			usagePercent := float64(idx.diskUsedSize) / float64(totalSize) * 100
			logToDebugWithTime(debugLog, "磁盘总空间: %d, 已使用: %d (%.1f%%), 剩余: %d",
				totalSize, idx.diskUsedSize, usagePercent, freeSize)
		}
	} else {
		idx.diskUsedSize = 0
		if debugLog != nil {
			logToDebugWithTime(debugLog, "无法获取磁盘使用情况: %v", err)
		}
	}

	// 保存索引路径
	logToDebugWithTime(debugLog, "[TIMING] 准备保存索引路径")
	if err := idx.SaveIndexPath(rootPath); err != nil {
		logWithTime("保存索引路径失败: %v", err)
	}
	logToDebugWithTime(debugLog, "[TIMING] 索引路径已保存")

	// 记录当前排除路径列表（调试用）
	if debugLog != nil {
		idx.excludeMu.RLock()
		logToDebugWithTime(debugLog, "[EXCLUDE] 当前排除路径列表 (共 %d 个):", len(idx.excludePaths))
		for i, p := range idx.excludePaths {
			logToDebugWithTime(debugLog, "  [%d] %s", i, p)
		}
		idx.excludeMu.RUnlock()
	}

	// 清空旧索引 - 支持多目录索引
	// 策略：删除所有 path 以 rootPath 开头的文件（无论它们的 indexed_path 是什么）
	// 这样可以避免路径重叠导致的冲突
	logWithTime("清空旧数据（路径: %s）", rootPath)
	deleteStart := time.Now()

	// 先查询将要删除的数据数量
	var oldCount int64
	// 规范化路径：确保以 / 结尾用于精确匹配子路径
	normalizedPath := rootPath
	if !strings.HasSuffix(normalizedPath, "/") {
		normalizedPath += "/"
	}

	// 删除条件：path = rootPath OR path LIKE 'rootPath/%'
	// 这样可以删除该路径本身和所有子路径的文件
	idx.db.QueryRow("SELECT COUNT(*) FROM files WHERE path = ? OR path LIKE ?",
		rootPath, normalizedPath+"%").Scan(&oldCount)

	if debugLog != nil {
		logToDebugWithTime(debugLog, "准备删除 %d 条记录（路径: %s 及其子路径）", oldCount, rootPath)
	}

	// 执行删除
	if _, err := idx.db.Exec("DELETE FROM files WHERE path = ? OR path LIKE ?",
		rootPath, normalizedPath+"%"); err != nil {
		if debugLog != nil {
			msg := fmt.Sprintf("DELETE失败: %v", err)
			logWithTime("%s", msg)
			logToDebugWithTime(debugLog, "%s", msg)
		}
		return err
	}

	// 验证删除结果
	var newCount int64
	idx.db.QueryRow("SELECT COUNT(*) FROM files WHERE path = ? OR path LIKE ?",
		rootPath, normalizedPath+"%").Scan(&newCount)
	logToDebugWithTime(debugLog, "清空后数据库有 %d 条记录", newCount)

	deleteDuration := time.Since(deleteStart).Seconds()
	logWithTime("清空数据耗时: %.2f秒", deleteDuration)
	perfLog.WriteString(fmt.Sprintf("清空数据耗时: %.2f秒\n", deleteDuration))

	// 极致性能优化（仅在构建索引时）
	logWithTime("设置性能参数")
	performancePragmas := `
	PRAGMA synchronous=OFF;
	PRAGMA cache_size=200000;
	PRAGMA temp_store=MEMORY;
	PRAGMA mmap_size=268435456;
	PRAGMA page_size=4096;
	`
	idx.db.Exec(performancePragmas)

	logWithTime("开始扫描文件")
	scanStart := time.Now()

	// 优化：使用mac-file-search扫描，只需一次sudo调用，比逐目录调用sudo ls快得多
	// 策略：如果有sudo密码，先尝试用mac-file-search（2分钟扫完全盘），失败再降级到逐目录扫描
	useMacFileScan := idx.HasSudoPassword()
	if useMacFileScan {
		logToDebugWithTime(debugLog, "[STRATEGY] 检测到sudo密码，使用mac-file-search一次性扫描")
		err := idx.buildIndexWithMacFileScan(rootPath, debugLog)
		if err == nil {
			// 成功，直接返回
			scanDuration := time.Since(scanStart).Seconds()
			logWithTime("扫描耗时: %.2f秒", scanDuration)
			perfLog.WriteString(fmt.Sprintf("扫描耗时: %.2f秒\n", scanDuration))

			// 优化：索引已在buildIndexWithMacFileScan中创建，无需重复创建

			// 恢复性能参数
			idx.db.Exec("PRAGMA synchronous=NORMAL")

			// 提交后台清理任务：VACUUM + checkpoint
			if idx.cleanupTaskFunc != nil {
				idx.cleanupTaskFunc(func() {
					// 写入清理日志
					homeDir, _ := os.UserHomeDir()
					logPath := filepath.Join(homeDir, ".mac-search-app", "cleanup.log")
					logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
					if logFile != nil {
						defer logFile.Close()

						// VACUUM
						msg := fmt.Sprintf("[%s] 正在后台回收数据库空间...\n", time.Now().Format("15:04:05"))
						logFile.WriteString(msg)
						logToDebugWithTime(debugLog, "[VACUUM] 开始回收数据库空间")

						vacuumStart := time.Now()
						if _, err := idx.db.Exec("VACUUM"); err != nil {
							msg = fmt.Sprintf("[%s] VACUUM 失败: %v\n", time.Now().Format("15:04:05"), err)
							logFile.WriteString(msg)
							logToDebugWithTime(debugLog, "[VACUUM] 失败: %v", err)
						} else {
							vacuumDuration := time.Since(vacuumStart).Seconds()
							msg = fmt.Sprintf("[%s] 回收空间完成，耗时: %.2f秒\n", time.Now().Format("15:04:05"), vacuumDuration)
							logFile.WriteString(msg)
							logToDebugWithTime(debugLog, "[VACUUM] 完成，耗时: %.2f秒", vacuumDuration)
						}

						// WAL checkpoint
						msg = fmt.Sprintf("[%s] 正在执行 WAL checkpoint...\n", time.Now().Format("15:04:05"))
						logFile.WriteString(msg)

						checkpointStart := time.Now()

						// 执行 checkpoint 并检查返回值
						var busy, log, checkpointed int
						err := idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
						elapsed := time.Since(checkpointStart).Seconds()

						if err != nil {
							msg = fmt.Sprintf("[%s] WAL checkpoint 失败: %v, 耗时: %.2f秒\n", time.Now().Format("15:04:05"), err, elapsed)
							logFile.WriteString(msg)
						} else {
							msg = fmt.Sprintf("[%s] WAL checkpoint 完成: busy=%d, log=%d, checkpointed=%d, 耗时: %.2f秒\n",
								time.Now().Format("15:04:05"), busy, log, checkpointed, elapsed)
							logFile.WriteString(msg)

							// 检查文件大小
							dbPath := filepath.Join(homeDir, ".mac-search-app", "index.db-wal")
							if info, err := os.Stat(dbPath); err == nil {
								msg = fmt.Sprintf("[%s] WAL 文件大小: %.2f MB\n", time.Now().Format("15:04:05"), float64(info.Size())/(1024*1024))
								logFile.WriteString(msg)
							}
						}
					}
				})
			}

			// 写入性能日志
			totalDuration := time.Since(startTime).Seconds()
			perfLog.WriteString(fmt.Sprintf("总耗时: %.2f秒\n", totalDuration))

			homeDir, _ := os.UserHomeDir()
			perfLogPath := filepath.Join(homeDir, ".mac-search-app", "performance.log")
			os.WriteFile(perfLogPath, []byte(perfLog.String()), 0644)

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
			if err != nil {
				logToDebugWithTime(debugLog, "[WARNING] 更新indexed_paths表失败: %v", err)
			} else {
				logToDebugWithTime(debugLog, "[STATS] 已更新indexed_paths表: %s (文件:%d, 目录:%d)", rootPath, fileCount, dirCount)
			}

			logWithTime("索引构建完成")
			logToDebugWithTime(debugLog, "[COMPLETE] 索引构建完成，总耗时: %.2f秒", totalDuration)
			return nil
		}

		// mac-file-search失败，降级到逐目录扫描
		logToDebugWithTime(debugLog, "[STRATEGY] mac-file-search失败: %v，降级到逐目录扫描", err)
		logWithTime("mac-file-search扫描失败，降级到逐目录扫描: %v", err)
	}

	// 原有的逐目录扫描逻辑（降级方案）
	// 如果有sudo密码，在开始扫描前执行sudo -v更新时间戳，避免后续每次都要验证
	// 注意：这样可以让后续的sudo调用不需要密码验证，大幅提升性能
	if idx.HasSudoPassword() {
		password := idx.getSudoPassword()
		if password != "" {
			// 执行 sudo -v 更新sudo时间戳（5分钟内有效）
			cmdStr := fmt.Sprintf("echo '%s' | sudo -S -v 2>&1", password)
			cmd := exec.Command("sh", "-c", cmdStr)
			if output, err := cmd.CombinedOutput(); err == nil {
				logToDebugWithTime(debugLog, "[SUDO] sudo时间戳更新成功，后续sudo调用将更快")
			} else {
				logToDebugWithTime(debugLog, "[SUDO] sudo时间戳更新失败: %v, 输出: %s", err, string(output))
			}
		}
	}

	// 并发扫描参数（参考 main.go）
	// 优化：增加worker到CPU*8，提高并发度，充分利用多核和IO
	workerCount := runtime.NumCPU() * 8
	// 优化：增大队列容量，减少阻塞
	dirQueue := make(chan string, workerCount*20)
	// 优化：增大文件通道到20万，减少写入阻塞
	filesChan := make(chan FileInfo, 200000)

	// 获取初始打开文件句柄数量（仅在调试时使用）
	if debugLog != nil {
		initialOpenFiles := getOpenFilesCount()
		logToDebugWithTime(debugLog, "[CONFIG] Worker=%d, CPU=%d, 初始句柄=%d",
			workerCount, runtime.NumCPU(), initialOpenFiles)
	}

	var taskWg sync.WaitGroup   // 追踪队列中的任务数
	var workerWg sync.WaitGroup // 追踪worker数量

	var taskAddCount atomic.Int64  // 追踪Add调用次数
	var taskDoneCount atomic.Int64 // 追踪Done调用次数

	// 启动worker协程
	for i := 0; i < workerCount; i++ {
		workerWg.Add(1)
		go func(id int) {
			defer workerWg.Done()
			for dirPath := range dirQueue {
				// 检查停止标志
				if idx.stopFlag.Load() {
					taskWg.Done()
					taskDoneCount.Add(1)
					continue
				}
				// 处理目录
				idx.scanDirectory(dirPath, filesChan, dirQueue, &taskWg, &taskAddCount, debugLog)
				taskWg.Done()
				taskDoneCount.Add(1)
			}
		}(i)
	}

	// 启动写入协程
	var writeErr error
	writeDone := make(chan bool)

	var totalScanned atomic.Int64 // 总共扫描的数量
	var ignoredCount atomic.Int64 // 被IGNORE的数量

	go func() {
		defer close(writeDone)

		// 优化：增大批次到100万，进一步减少数据库提交次数，提升性能
		batchSize := 1000000 // 100万行一次提交，对于全盘扫描200万文件只需2次提交
		batch := 0

		tx, err := idx.db.Begin()
		if err != nil {
			writeErr = err
			return
		}

		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO files (path, name, size, mod_time, is_dir, ext, indexed_path)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			tx.Rollback()
			writeErr = err
			return
		}

		for fileInfo := range filesChan {
			// 检查停止标志
			if idx.stopFlag.Load() {
				break
			}
			totalScanned.Add(1)

			result, err := stmt.Exec(
				fileInfo.path,
				fileInfo.name,
				fileInfo.size,
				fileInfo.modTime,
				fileInfo.isDir,
				fileInfo.ext,
				rootPath,
			)

			// 只有真正插入时才计数（INSERT OR IGNORE 会忽略重复）
			if err == nil {
				affected, _ := result.RowsAffected()
				if affected > 0 {
					if fileInfo.isDir == 1 {
						idx.dirCount.Add(1)
					} else {
						idx.fileCount.Add(1)
						// 累加磁盘占用（参考 main.go，硬链接去重）
						if !fileInfo.isHardlink {
							idx.totalDisk.Add(fileInfo.diskUsage)
						}
					}
				} else {
					ignoredCount.Add(1)
				}

				batch++

				// 定期提交
				if batch >= batchSize {
					stmt.Close()
					if err := tx.Commit(); err != nil {
						writeErr = err
						return
					}

					tx, err = idx.db.Begin()
					if err != nil {
						writeErr = err
						return
					}

					stmt, err = tx.Prepare(`
						INSERT OR IGNORE INTO files (path, name, size, mod_time, is_dir, ext, indexed_path)
						VALUES (?, ?, ?, ?, ?, ?, ?)
					`)
					if err != nil {
						tx.Rollback()
						writeErr = err
						return
					}

					batch = 0
				}

				// 每10000条报告一次进度（降低频率，减少性能开销）
				total := idx.fileCount.Load() + idx.dirCount.Load()
				if total%10000 == 0 {
					if idx.onProgress != nil {
						elapsed := time.Since(idx.buildStartTime).Seconds()
						idx.onProgress(idx.fileCount.Load(), idx.dirCount.Load(), idx.totalDisk.Load(), elapsed)
					}
					if idx.onScanFile != nil {
						idx.onScanFile(fileInfo.path)
					}
					// 每1000条也输出一次打开文件句柄数量
					// openFilesCount := idx.openFiles.Load()
					// if debugLog != nil {
					// 	debugLog.WriteString(fmt.Sprintf("[STATUS] 当前打开文件句柄数(估算): %d (时间: %s)\n",
					// 		openFilesCount, time.Now().Format("15:04:05")))
					// }
				}
			}
		}

		// 提交最后一批
		if debugLog != nil {
			logToDebugWithTime(debugLog, "写入goroutine结束，总扫描=%d, 总插入=%d, 被忽略=%d, 最后batch=%d (时间: %s)",
				totalScanned.Load(), idx.fileCount.Load()+idx.dirCount.Load(), ignoredCount.Load(), batch, time.Now().Format("15:04:05"))
		}
		if stmt != nil {
			stmt.Close()
		}
		if tx != nil {
			if err := tx.Commit(); err != nil {
				logToDebugWithTime(debugLog, "最后一批提交失败: %v", err)
			} else {
				logToDebugWithTime(debugLog, "最后一批提交成功 (时间: %s)", time.Now().Format("15:04:05"))
			}
		}
	}()

	// 添加根目录到队列
	taskWg.Add(1)
	taskAddCount.Add(1)
	dirQueue <- rootPath

	// 等待所有任务完成
	taskWg.Wait()
	logToDebugWithTime(debugLog, "[完成] 扫描任务完成，taskAdd=%d, taskDone=%d",
		taskAddCount.Load(), taskDoneCount.Load())
	close(dirQueue)

	// 等待worker和写入完成
	workerWg.Wait()
	close(filesChan)
	<-writeDone

	if writeErr != nil {
		return writeErr
	}

	// 单次垃圾回收，释放资源
	runtime.GC()

	scanElapsed := time.Since(scanStart)
	scanDuration := scanElapsed.Seconds()
	logWithTime("文件扫描耗时: %.2f秒", scanDuration)
	perfLog.WriteString(fmt.Sprintf("文件扫描耗时: %.2f秒\n", scanDuration))

	// 恢复正常的安全设置
	restorePragmas := `
	PRAGMA synchronous=NORMAL;
	`
	idx.db.Exec(restorePragmas)

	// 重新创建索引（这个操作很快，一次性完成）
	logWithTime("正在创建索引")
	indexStart := time.Now()

	// 只创建最重要的索引，减少索引创建时间
	createIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_name ON files(name);
	`
	if _, err := idx.db.Exec(createIndexSQL); err != nil {
		return err
	}

	indexDuration := time.Since(indexStart).Seconds()
	logWithTime("创建索引耗时: %.2f秒", indexDuration)
	perfLog.WriteString(fmt.Sprintf("创建name索引耗时: %.2f秒\n", indexDuration))

	// 优化：删除异步创建其他索引（idx_ext和idx_path已移除）

	elapsed := time.Since(startTime)
	totalDuration := elapsed.Seconds()

	logMessage := fmt.Sprintf(`===============================
性能分析报告
===============================
清空数据: %.2f秒
文件扫描+写入: %.2f秒 (%.0f%%)
创建索引: %.2f秒 (%.0f%%)
-------------------------------
总耗时: %.2f秒
文件数: %d
目录数: %d
平均速度: %.0f 项/秒
===============================
`, deleteDuration, scanDuration, scanDuration/totalDuration*100,
		indexDuration, indexDuration/totalDuration*100,
		totalDuration, idx.fileCount.Load(), idx.dirCount.Load(),
		float64(idx.fileCount.Load()+idx.dirCount.Load())/totalDuration)

	logWithTime("%s", logMessage)

	// 异步保存性能日志到文件，不阻塞用户
	go func() {
		logPath := filepath.Join(homeDir, ".mac-search-app", "performance.log")
		os.WriteFile(logPath, []byte(logMessage), 0644)
	}()

	// 保存统计信息到config表（避免每次COUNT(*)）
	fileCount := idx.fileCount.Load()
	dirCount := idx.dirCount.Load()
	total := fileCount + dirCount
	scanTime := time.Now().Unix()

	if err := idx.saveStats(fileCount, dirCount, total, scanTime); err != nil {
		logWithTime("保存统计信息失败: %v", err)
	}

	// 提交后台清理任务：WAL checkpoint
	if idx.cleanupTaskFunc != nil {
		idx.cleanupTaskFunc(func() {
			// 写入清理日志
			homeDir, _ := os.UserHomeDir()
			logPath := filepath.Join(homeDir, ".mac-search-app", "cleanup.log")
			logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if logFile != nil {
				defer logFile.Close()

				msg := fmt.Sprintf("[%s] 正在后台执行 WAL checkpoint...\n", time.Now().Format("15:04:05"))
				logFile.WriteString(msg)

				checkpointStart := time.Now()

				// 执行 checkpoint 并检查返回值
				var busy, log, checkpointed int
				err := idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
				elapsed := time.Since(checkpointStart).Seconds()

				if err != nil {
					msg = fmt.Sprintf("[%s] WAL checkpoint 失败: %v, 耗时: %.2f秒\n", time.Now().Format("15:04:05"), err, elapsed)
					logFile.WriteString(msg)
				} else {
					msg = fmt.Sprintf("[%s] WAL checkpoint 完成: busy=%d, log=%d, checkpointed=%d, 耗时: %.2f秒\n",
						time.Now().Format("15:04:05"), busy, log, checkpointed, elapsed)
					logFile.WriteString(msg)

					// 检查文件大小
					dbPath := filepath.Join(homeDir, ".mac-search-app", "index.db-wal")
					if info, err := os.Stat(dbPath); err == nil {
						msg = fmt.Sprintf("[%s] WAL 文件大小: %.2f MB\n", time.Now().Format("15:04:05"), float64(info.Size())/(1024*1024))
						logFile.WriteString(msg)
					}
				}
			}
		})
	}

	// 更新 indexed_paths 表统计信息
	now := time.Now().Unix()
	_, err := idx.db.Exec(`
		INSERT INTO indexed_paths (path, file_count, dir_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			file_count = excluded.file_count,
			dir_count = excluded.dir_count,
			updated_at = excluded.updated_at
	`, rootPath, fileCount, dirCount, now, now)
	if err != nil {
		logToDebugWithTime(debugLog, "[WARNING] 更新indexed_paths表失败: %v", err)
	} else {
		logToDebugWithTime(debugLog, "[STATS] 已更新indexed_paths表: %s (文件:%d, 目录:%d)", rootPath, fileCount, dirCount)
	}

	return nil
}

// scanDirectory 扫描单个目录
func (idx *Indexer) scanDirectory(dirPath string, filesChan chan FileInfo, dirQueue chan string, taskWg *sync.WaitGroup, taskAddCount *atomic.Int64, debugLog *os.File) {
	// 检查停止标志
	stopFlagValue := idx.stopFlag.Load()
	if stopFlagValue {
		// 注意：这里返回时，调用者（worker）会调用 taskWg.Done()，所以不需要在这里调用
		// 注意：这里还没有调用 os.ReadDir，所以不需要清空 entries
		return
	}

	// 检查是否应该跳过这个目录
	if idx.shouldSkip(dirPath) {
		// 被排除的目录，直接返回
		// 调试日志：记录被跳过的根目录
		if dirPath == idx.scanPath && debugLog != nil {
			logToDebugWithTime(debugLog, "[SCAN] 根目录被跳过: %s (shouldSkip=true)", dirPath)
		}
		// 注意：这里还没有调用 os.ReadDir，所以不需要清空 entries
		return
	}

	// 参考 main.go：不在这里打开 debug.log，避免频繁打开/关闭文件句柄
	// 如果需要日志，应该在 BuildIndex 层面统一处理

	// 使用 os.ReadDir 读取目录（它会自动关闭目录句柄）
	// 参考 main.go，直接调用不使用信号量限制
	entries, err := os.ReadDir(dirPath)

	// 关键：立即将所有信息提取到纯数据结构中，然后清空 entries
	// 这样可以让 GC 立即回收 DirEntry 对象，从而释放底层的文件描述符
	type SimpleEntry struct {
		name      string
		isDir     bool
		isSymlink bool
	}
	var simpleEntries []SimpleEntry
	if err == nil && len(entries) > 0 {
		simpleEntries = make([]SimpleEntry, 0, len(entries))
		for _, entry := range entries {
			simpleEntries = append(simpleEntries, SimpleEntry{
				name:      entry.Name(),
				isDir:     entry.IsDir(),
				isSymlink: entry.Type()&os.ModeSymlink != 0,
			})
		}
		// 立即清空 entries，释放资源
		for i := range entries {
			entries[i] = nil
		}
		entries = nil
	}

	// 调试日志：记录 ReadDir 结果（只记录根目录，不记录子目录避免日志过多）
	isRootDir := (dirPath == idx.scanPath)
	if debugLog != nil && isRootDir {
		logToDebugWithTime(debugLog, "[SCAN] 根目录 ReadDir: %s, entries=%d, err=%v", dirPath, len(simpleEntries), err)
	}

	if err != nil {
		// 记录 ReadDir 错误，特别是权限错误
		errStr := err.Error()
		isPermissionError := strings.Contains(errStr, "permission denied") ||
			strings.Contains(errStr, "operation not permitted")

		// 如果是权限错误，尝试使用 sudo 读取目录
		if isPermissionError {
			sudoEntries, sudoErr := idx.readDirWithSudo(dirPath)
			// 使用 defer 确保 sudoEntries 也被清空
			defer func() {
				if sudoEntries != nil {
					sudoEntries = nil
				}
			}()
			if sudoErr != nil {
				// sudo 执行失败，静默返回（参考 main.go，不频繁打开日志文件）
				// 注意：这里返回时，调用者（worker）会调用 taskWg.Done()，所以不需要在这里调用
				// defer 会自动清空 entries 和 sudoEntries
				return
			}

			// sudo 成功，使用解析出的 entries 继续处理
			// 如果 entries 为空，说明目录是空的（只有 . 和 ..），这是正常的，直接返回
			if len(sudoEntries) == 0 {
				// 空目录，正常返回
				// defer 会自动清空 entries 和 sudoEntries
				return
			}

			// 清空 os.ReadDir 的 entries，使用 sudo 的 entries
			entries = nil

			for _, entry := range sudoEntries {
				// 检查停止标志
				if idx.stopFlag.Load() {
					// defer 会自动清空 sudoEntries
					return
				}

				fullPath := filepath.Join(dirPath, entry.name)

				if idx.shouldSkip(fullPath) {
					continue
				}

				if entry.isSymlink {
					continue
				}

				// 发送文件信息到 channel
				isDirInt := 0
				if entry.isDir {
					isDirInt = 1
				}
				filesChan <- FileInfo{
					path:    fullPath,
					name:    entry.name,
					size:    entry.size,
					modTime: entry.modTime,
					isDir:   isDirInt,
					ext:     entry.ext,
				}

				// 如果是目录，添加到队列
				if entry.isDir {
					// 注意：taskWg.Add(1) 必须在发送到队列之前调用
					// Done() 会在 worker 处理完这个目录后调用（316行）
					taskWg.Add(1)
					taskAddCount.Add(1)
					// 记录前20个子目录添加到队列的情况（使用传入的 debugLog，避免频繁打开文件）
					// 注意：这里不再打开 debugLogFile，因为已经通过参数传递了
					go func(path string) {
						defer func() {
							if r := recover(); r != nil {
								// panic 时，taskWg.Add(1) 已经调用，需要 Done() 来平衡
								taskWg.Done()
							}
						}()
						if idx.stopFlag.Load() {
							// 停止标志已设置，taskWg.Add(1) 已经调用，需要 Done() 来平衡
							// 注意：这里 Done() 后，worker 不会处理这个目录，所以 Done() 是必须的
							taskWg.Done()
							return
						}
						// 成功发送到队列，taskWg.Done() 会在 worker 处理完这个目录后调用（316行）
						// 注意：这里不能调用 Done()，因为 worker 会处理这个目录
						dirQueue <- path
					}(fullPath)
				}
			}
			// sudo entries 处理完成，sudoEntries 会被 defer 自动清空
			return
		}

		// ReadDir 失败，直接返回（参考 main.go，不频繁打开日志文件）
		// 如果是 "too many open files" 错误，会在 BuildIndex 的 debug.log 中记录
		// 注意：这里返回时，调用者（worker）会调用 taskWg.Done()，所以不需要在这里调用
		// 记录错误日志
		logToDebugWithTime(debugLog, "[SCAN] ReadDir 失败: %s, err=%v, isRootDir=%v", dirPath, err, isRootDir)
		// defer 会自动清空 entries
		return
	}

	// 如果 simpleEntries 为空，说明目录是空的（只有 . 和 ..），这是正常的，直接返回
	if len(simpleEntries) == 0 {
		// 调试日志：记录空目录（只记录根目录，子目录太多了）
		if debugLog != nil && isRootDir {
			logToDebugWithTime(debugLog, "[SCAN] 根目录为空: %s", dirPath)
		}
		return
	}

	// 调试日志：记录根目录的扫描详情
	if isRootDir && debugLog != nil {
		dirCount := 0
		fileCount := 0
		skippedCount := 0
		for _, entry := range simpleEntries {
			fullPath := filepath.Join(dirPath, entry.name)
			if idx.shouldSkip(fullPath) {
				skippedCount++
				continue
			}
			if entry.isDir {
				dirCount++
			} else {
				fileCount++
			}
		}
		logToDebugWithTime(debugLog, "[SCAN] 根目录扫描详情: %s, 总entries=%d, 目录=%d, 文件=%d, 跳过=%d",
			dirPath, len(simpleEntries), dirCount, fileCount, skippedCount)
	}

	for i, entry := range simpleEntries {
		// 检查停止标志
		if idx.stopFlag.Load() {
			return
		}

		// 使用 SimpleEntry 的字段
		entryName := entry.name
		fullPath := filepath.Join(dirPath, entryName)

		if idx.shouldSkip(fullPath) {
			// 立即清空已处理的 entry
			simpleEntries[i] = SimpleEntry{}
			continue
		}

		// 使用 SimpleEntry 的字段
		isDir := entry.isDir
		isSymlink := entry.isSymlink

		if isSymlink {
			// 立即清空已处理的 entry
			simpleEntries[i] = SimpleEntry{}
			continue
		}

		// 对于文件，需要获取详细信息（大小、修改时间等）
		// 参考 main.go：使用 os.Lstat() 而不是 entry.Info()，文件句柄释放更快
		var size int64
		var modTime int64
		var name string
		var ext string
		var diskUsage int64 // 磁盘占用（用于进度条）
		var isHardlink bool // 是否为硬链接

		if isDir {
			// 目录：使用 entry.Name() 和 entry.Type()，完全避免打开文件句柄
			// 绝对不要对目录调用 entry.Info() 或 os.Stat()，这会打开目录句柄
			// 对于目录，使用默认的修改时间，避免打开文件句柄
			name = entryName
			size = 0
			modTime = time.Now().Unix() // 使用当前时间作为默认值，避免打开目录句柄
			// 优化：提前计算扩展名
			ext = strings.ToLower(filepath.Ext(name))
		} else {
			// 文件：参考 main.go，使用 os.Lstat() 而不是 entry.Info()
			// os.Lstat() 会立即释放文件句柄，比 entry.Info() 更安全
			// 参考 main.go 第 262 行：使用 os.Lstat() 获取文件信息（不跟随符号链接）
			info, err := os.Lstat(fullPath)

			if err != nil {
				// 对于 bad file descriptor 等预期的系统错误，完全忽略（参考 main.go）
				// 参考 main.go：静默跳过错误，不频繁打开日志文件
				continue
			}

			// 立即提取所有需要的信息
			name = info.Name()
			size = info.Size()
			modTime = info.ModTime().Unix()

			// 计算磁盘占用（参考 main.go）
			// 使用已获取的 info.Sys() 获取 stat，避免重复调用 os.Lstat()
			stat, ok := info.Sys().(*syscall.Stat_t)
			if ok {
				// Blocks 是 512 字节块的数量
				diskUsage = stat.Blocks * 512
				// 注意：硬链接去重需要维护 inodeMap，这里简化处理，暂时不去重
				// 如果需要去重，可以参考 main.go 的实现
			} else {
				// 无法获取块信息，使用逻辑大小
				diskUsage = size
			}

			// 优化：提前计算扩展名（在清空 info 之前）
			ext = strings.ToLower(filepath.Ext(name))

			// 关键：立即清空 info，虽然 os.FileInfo 通常不持有文件句柄，但为了安全还是清空
			// 注意：这里不能直接设置 info = nil，因为它是值类型，我们需要确保不再引用它
			// 实际上，os.FileInfo 是接口类型，但它的实现（*os.fileStat）可能持有某些资源
			// 为了安全，我们在使用完所有信息后，通过作用域结束来释放
			_ = info // 标记已使用，确保编译器不会优化掉
		}

		isDirInt := 0
		if isDir {
			isDirInt = 1
			// 必须使用 goroutine，否则 dirQueue 满时会阻塞 scanDirectory
			// dirQueue 缓冲为 workerCount*10，如果直接发送可能阻塞整个扫描流程
			taskWg.Add(1)
			taskAddCount.Add(1)
			// 删除频繁的子目录添加日志
			go func(path string) {
				// 使用 recover 防止在 dirQueue 关闭后发送导致 panic
				defer func() {
					if r := recover(); r != nil {
						// dirQueue 已关闭导致 panic，taskWg.Add(1) 已经调用，需要 Done() 来平衡
						taskWg.Done()
					}
				}()
				// 检查停止标志
				if idx.stopFlag.Load() {
					// 停止标志已设置，taskWg.Add(1) 已经调用，需要 Done() 来平衡
					taskWg.Done()
					return
				}
				// 成功发送到队列，taskWg.Done() 会在 worker 处理完这个目录后调用
				dirQueue <- path
			}(fullPath)
		}

		// 发送文件信息到 channel
		// 直接发送，如果 filesChan 已关闭会 panic，但此时应该已经不会有新的 scanDirectory 被调用了
		filesChan <- FileInfo{
			path:       fullPath,
			name:       name,
			size:       size,
			modTime:    modTime,
			isDir:      isDirInt,
			ext:        ext,
			diskUsage:  diskUsage,
			isHardlink: isHardlink,
		}

		// 立即清空已处理的 entry
		simpleEntries[i] = SimpleEntry{}
	}

	// 清空 simpleEntries 切片
	simpleEntries = nil
}

// Search 搜索文件
func (idx *Indexer) Search(keyword string, useRegex bool, offset int, limit int) ([]FileEntry, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// 去掉前后空格
	keyword = strings.TrimSpace(keyword)

	if keyword == "" {
		return []FileEntry{}, nil
	}

	var query string
	var args []interface{}

	// 检查是否是多层空格分隔搜索（如：业务线 代码 sleep_run.php）
	keywords := strings.Fields(keyword) // 按空格分隔
	if len(keywords) > 1 {
		// 多层搜索：构建 path LIKE '%keyword1%' AND path LIKE '%keyword2%' ...
		var conditions []string
		for _, kw := range keywords {
			conditions = append(conditions, "path LIKE ?")
			args = append(args, "%"+kw+"%")
		}

		query = `SELECT id, path, name, size, mod_time, is_dir, ext
				 FROM files
				 WHERE ` + strings.Join(conditions, " AND ") + `
				 ORDER BY
				   length(path),
				   path
				 LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	} else if useRegex {
		// 正则表达式搜索：分批查询，在应用层用正则过滤
		// 默认不区分大小写，除非用户显式使用 (?-i) 标志
		regexPattern := keyword
		if !strings.HasPrefix(keyword, "(?i)") && !strings.HasPrefix(keyword, "(?-i)") {
			regexPattern = "(?i)" + keyword
		}
		re, err := regexp.Compile(regexPattern)
		if err != nil {
			// 正则表达式无效，返回错误
			return nil, fmt.Errorf("无效的正则表达式: %v", err)
		}

		var results []FileEntry
		batchSize := 5000
		dbOffset := 0
		matchedCount := 0
		skippedCount := 0

		for {
			// 分批查询
			query = `SELECT id, path, name, size, mod_time, is_dir, ext
					 FROM files
					 LIMIT ? OFFSET ?`
			args = []interface{}{batchSize, dbOffset}

			rows, err := idx.db.Query(query, args...)
			if err != nil {
				return nil, err
			}

			hasRows := false
			for rows.Next() {
				hasRows = true
				var entry FileEntry
				var isDir int
				err := rows.Scan(&entry.ID, &entry.Path, &entry.Name, &entry.Size, &entry.ModTime, &isDir, &entry.Ext)
				if err != nil {
					continue
				}
				entry.IsDir = isDir == 1

				// 用正则表达式匹配文件名或路径
				if re.MatchString(entry.Name) || re.MatchString(entry.Path) {
					matchedCount++
					// 跳过前offset条匹配结果
					if skippedCount < offset {
						skippedCount++
						continue
					}
					// 收集limit条结果
					results = append(results, entry)
					if len(results) >= limit {
						rows.Close()
						return results, nil
					}
				}
			}
			rows.Close()

			if !hasRows {
				break
			}

			dbOffset += batchSize
		}

		return results, nil
	} else {
		// 通配符搜索
		searchPattern := strings.ReplaceAll(keyword, "*", "%")
		searchPattern = strings.ReplaceAll(searchPattern, "?", "_")

		// 如果没有通配符，自动添加前后匹配
		if !strings.Contains(keyword, "*") && !strings.Contains(keyword, "?") {
			searchPattern = "%" + searchPattern + "%"
		}

		// 搜索文件名和路径
		query = `SELECT id, path, name, size, mod_time, is_dir, ext
				 FROM files
				 WHERE name LIKE ? OR path LIKE ?
				 ORDER BY
				   CASE
				     WHEN name LIKE ? THEN 0
				     WHEN name LIKE ? THEN 1
				     ELSE 2
				   END,
				   is_dir DESC,
				   length(name),
				   name
				 LIMIT ? OFFSET ?`
		exactPattern := keyword + "%"
		startPattern := keyword + "%"
		args = []interface{}{searchPattern, searchPattern, exactPattern, startPattern, limit, offset}
	}

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileEntry
	for rows.Next() {
		var entry FileEntry
		var isDir int
		err := rows.Scan(&entry.ID, &entry.Path, &entry.Name, &entry.Size, &entry.ModTime, &isDir, &entry.Ext)
		if err != nil {
			continue
		}
		entry.IsDir = isDir == 1
		results = append(results, entry)
	}

	return results, nil
}

// SearchWithPagination 搜索文件（支持分页）
func (idx *Indexer) SearchWithPagination(keyword string, useRegex bool, offset int, limit int) ([]FileEntry, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// 去掉前后空格
	keyword = strings.TrimSpace(keyword)

	if keyword == "" {
		return []FileEntry{}, nil
	}

	var query string
	var args []interface{}

	// 检查是否是多层空格分隔搜索
	keywords := strings.Fields(keyword)

	// 判断是否需要搜索路径（关键词包含斜杠）
	hasPathSearch := false
	var pathConditions []string
	var nameConditions []string

	for _, kw := range keywords {
		if strings.Contains(kw, "/") {
			// 包含斜杠，搜索路径
			hasPathSearch = true
			pathConditions = append(pathConditions, "path LIKE ?")
			args = append(args, "%"+kw+"%")
		} else {
			// 不包含斜杠，搜索文件名
			nameConditions = append(nameConditions, "name LIKE ?")
			args = append(args, "%"+kw+"%")
		}
	}

	if len(keywords) > 1 || hasPathSearch {
		// 多层搜索或路径搜索
		var conditions []string
		conditions = append(conditions, pathConditions...)
		conditions = append(conditions, nameConditions...)

		query = `SELECT id, path, name, size, mod_time, is_dir, ext
				 FROM files
				 WHERE ` + strings.Join(conditions, " AND ") + `
				 ORDER BY length(path), path
				 LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	} else if useRegex {
		// 正则搜索：调用Search函数，传递offset和limit
		return idx.Search(keyword, useRegex, offset, limit)
	} else {
		// 通配符搜索 - 只搜索name字段，利用idx_name索引
		searchPattern := strings.ReplaceAll(keyword, "*", "%")
		searchPattern = strings.ReplaceAll(searchPattern, "?", "_")

		if !strings.Contains(keyword, "*") && !strings.Contains(keyword, "?") {
			searchPattern = "%" + searchPattern + "%"
		}

		// 优化：只搜索name字段，可以使用idx_name索引
		query = `SELECT id, path, name, size, mod_time, is_dir, ext
				 FROM files
				 WHERE name LIKE ?
				 ORDER BY
				   CASE
				     WHEN name = ? THEN 0
				     WHEN name LIKE ? THEN 1
				     ELSE 2
				   END,
				   is_dir DESC,
				   length(name),
				   name
				 LIMIT ? OFFSET ?`
		exactMatch := keyword
		startPattern := keyword + "%"
		args = []interface{}{searchPattern, exactMatch, startPattern, limit, offset}
	}

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileEntry
	for rows.Next() {
		var entry FileEntry
		var isDir int
		err := rows.Scan(&entry.ID, &entry.Path, &entry.Name, &entry.Size, &entry.ModTime, &isDir, &entry.Ext)
		if err != nil {
			continue
		}
		entry.IsDir = isDir == 1
		results = append(results, entry)
	}

	return results, nil
}

// UpdateFile 更新单个文件索引
func (idx *Indexer) UpdateFile(path string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		// 文件不存在，删除索引
		_, err = idx.db.Exec("DELETE FROM files WHERE path = ?", path)
		return err
	}

	ext := strings.ToLower(filepath.Ext(info.Name()))
	isDir := 0
	if info.IsDir() {
		isDir = 1
	}

	_, err = idx.db.Exec(`
		INSERT OR REPLACE INTO files (path, name, size, mod_time, is_dir, ext)
		VALUES (?, ?, ?, ?, ?, ?)
	`, path, info.Name(), info.Size(), info.ModTime().Unix(), isDir, ext)

	return err
}

// DeleteFile 删除文件索引
func (idx *Indexer) DeleteFile(path string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	_, err := idx.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// GetStats 获取索引统计
// GetStats 获取索引统计（优先从缓存读取，避免慢COUNT查询）
func (idx *Indexer) GetStats() (fileCount, dirCount int64, err error) {
	// 先尝试从config读取缓存的统计
	var statsJSON string
	err = idx.db.QueryRow("SELECT value FROM config WHERE key = 'index_stats'").Scan(&statsJSON)
	if err == nil {
		// 有缓存，解析JSON
		var stats map[string]interface{}
		if json.Unmarshal([]byte(statsJSON), &stats) == nil {
			if fc, ok := stats["fileCount"].(float64); ok {
				fileCount = int64(fc)
			}
			if dc, ok := stats["dirCount"].(float64); ok {
				dirCount = int64(dc)
			}
			// 如果解析成功，直接返回
			if fileCount > 0 || dirCount > 0 {
				return fileCount, dirCount, nil
			}
		}
	}

	// 没有缓存或缓存无效，降级到COUNT查询（慢但准确）
	err = idx.db.QueryRow("SELECT COUNT(*) FROM files WHERE is_dir = 0").Scan(&fileCount)
	if err != nil {
		return 0, 0, err
	}

	err = idx.db.QueryRow("SELECT COUNT(*) FROM files WHERE is_dir = 1").Scan(&dirCount)
	if err != nil {
		return 0, 0, err
	}

	return fileCount, dirCount, nil
}

// SaveIndexPath 保存索引路径
func (idx *Indexer) SaveIndexPath(path string) error {
	_, err := idx.db.Exec(`
		INSERT OR REPLACE INTO config (key, value)
		VALUES ('index_path', ?)
	`, path)
	return err
}

// GetIndexPath 获取索引路径
func (idx *Indexer) GetIndexPath() (string, error) {
	var path string
	err := idx.db.QueryRow("SELECT value FROM config WHERE key = 'index_path'").Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// Close 关闭数据库
func (idx *Indexer) Close() error {
	return idx.db.Close()
}

// SetSudoPassword 设置 sudo 密码（参考 SwitchHosts 的做法）
func (idx *Indexer) SetSudoPassword(password string) {
	idx.sudoMu.Lock()
	defer idx.sudoMu.Unlock()
	// 转义特殊字符，防止命令注入
	idx.sudoPassword = strings.ReplaceAll(password, "\\", "\\\\")
	idx.sudoPassword = strings.ReplaceAll(idx.sudoPassword, "'", "\\x27")
}

// getSudoPassword 获取 sudo 密码
func (idx *Indexer) getSudoPassword() string {
	idx.sudoMu.RLock()
	defer idx.sudoMu.RUnlock()
	return idx.sudoPassword
}

// HasSudoPassword 检查是否已设置 sudo 密码
func (idx *Indexer) HasSudoPassword() bool {
	idx.sudoMu.RLock()
	defer idx.sudoMu.RUnlock()
	return idx.sudoPassword != ""
}

// readDirWithSudo 使用 sudo 读取目录内容（参考 SwitchHosts 的做法）
// 关键：使用 echo 'password' | sudo -S command 的方式，从标准输入读取密码
func (idx *Indexer) readDirWithSudo(dirPath string) ([]sudoEntry, error) {
	// 使用信号量限制并发调用
	idx.sudoSem <- struct{}{}
	defer func() { <-idx.sudoSem }()

	// 优化：首先尝试直接用sudo（不带密码），利用之前sudo -v更新的时间戳
	// 如果失败，再用密码重试
	cmdStr := fmt.Sprintf("sudo ls -la '%s' 2>&1", dirPath)
	cmd := exec.Command("sh", "-c", cmdStr)
	output, err := cmd.CombinedOutput()

	// 如果失败且提示需要密码，则用密码重试
	if err != nil && strings.Contains(string(output), "password") {
		password := idx.getSudoPassword()
		if password == "" {
			return nil, fmt.Errorf("sudo密码未设置")
		}
		cmdStr = fmt.Sprintf("echo '%s' | sudo -S ls -la '%s' 2>&1", password, dirPath)
		cmd = exec.Command("sh", "-c", cmdStr)
		output, err = cmd.CombinedOutput()
	}

	// 确保命令进程已完全退出，释放所有资源
	// 注意：cmd.Process 可能为 nil（如果命令未启动），需要检查
	if cmd.Process != nil {
		// 等待进程完全退出（忽略错误）
		_, _ = cmd.Process.Wait()
	}

	// 关键：先转换为字符串，再清空 output 和 cmd
	outputStr := string(output)

	// 清空 output 和 cmd，释放资源
	output = nil
	cmd = nil

	// 强制 GC，确保进程资源被立即释放
	runtime.GC()

	if err != nil {
		// 如果密码错误，清空密码
		if strings.Contains(outputStr, "password") || strings.Contains(outputStr, "incorrect") {
			idx.SetSudoPassword("")
		}
		return nil, fmt.Errorf("sudo执行失败: %v, 输出: %s", err, outputStr)
	}
	if strings.Contains(outputStr, "error:") || strings.Contains(outputStr, "Permission denied") {
		return nil, fmt.Errorf("sudo执行错误: %s", outputStr)
	}

	// 解析 ls -la 输出
	// 格式示例：
	// total 1234
	// drwxr-xr-x  3 user  staff   102 Jan  1 12:00 .
	// drwxr-xr-x  5 user  staff   170 Jan  1 12:00 ..
	// -rw-r--r--  1 user  staff  1234 Jan  1 12:00 file.txt
	// drwxr-xr-x  2 user  staff    68 Jan  1 12:00 dir

	lines := strings.Split(strings.TrimSpace(outputStr), "\n")
	var entries []sudoEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total") {
			continue
		}

		// 跳过 . 和 ..
		if strings.HasSuffix(line, " .") || strings.HasSuffix(line, " ..") {
			continue
		}

		// 解析 ls -la 输出
		// 格式：权限 链接数 用户 组 大小 月 日 时间 文件名
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}

		// 第一个字段是权限字符串，第一个字符表示类型
		// d=目录, l=符号链接, -=文件
		permStr := fields[0]
		isDir := strings.HasPrefix(permStr, "d")
		isSymlink := strings.HasPrefix(permStr, "l")

		// 大小在第5个字段
		size, _ := strconv.ParseInt(fields[4], 10, 64)

		// 文件名从第9个字段开始（可能包含空格）
		name := strings.Join(fields[8:], " ")

		// 跳过 . 和 ..
		if name == "." || name == ".." {
			continue
		}

		// 计算扩展名
		ext := strings.ToLower(filepath.Ext(name))

		// 修改时间需要组合月、日、时间字段（字段5、6、7）
		// 简化处理：使用当前时间，因为解析时间格式较复杂
		modTime := time.Now().Unix()

		entries = append(entries, sudoEntry{
			name:      name,
			isDir:     isDir,
			isSymlink: isSymlink,
			size:      size,
			modTime:   modTime,
			ext:       ext,
		})
	}

	return entries, nil
}

// buildIndexWithMacFileScan 使用mac-file-search一次性扫描，然后解析JSON构建索引
// 优势：只需一次sudo调用，比逐目录调用sudo ls快得多（2分钟 vs 10+分钟）
func (idx *Indexer) buildIndexWithMacFileScan(rootPath string, debugLog *os.File) error {
	// 生成临时文件路径
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("mac-file-search-%d.json", time.Now().Unix()))
	progressFile := filepath.Join(os.TempDir(), fmt.Sprintf("mac-file-search-progress-%d.json", time.Now().Unix()))

	defer func() {
		// 确保删除临时文件
		if err := os.Remove(tmpFile); err == nil {
			logToDebugWithTime(debugLog, "[CLEANUP] 临时文件已删除: %s", tmpFile)
		} else if !os.IsNotExist(err) {
			logToDebugWithTime(debugLog, "[WARN] 删除临时文件失败: %s, err=%v", tmpFile, err)
		}
		// 删除进度文件
		if err := os.Remove(progressFile); err == nil {
			logToDebugWithTime(debugLog, "[CLEANUP] 进度文件已删除: %s", progressFile)
		}
	}()

	// 构建排除路径参数
	var excludeArgs string
	idx.excludeMu.RLock()
	if len(idx.excludePaths) > 0 {
		excludeArgs = "-exclude " + strings.Join(idx.excludePaths, ",")
	}
	idx.excludeMu.RUnlock()

	// 获取mac-file-search可执行文件路径（按优先级查找）
	// 1. APP包内: Contents/Resources/mac-file-search（生产环境）
	// 2. 开发环境: bin/mac-file-search
	// 3. 父目录: ../mac-file-search
	// 4. 系统路径: /usr/local/bin/mac-file-search

	var macFileScanPath string

	// 查找顺序1：APP包内 Contents/Resources/mac-file-search
	if exePath, err := os.Executable(); err == nil {
		// exePath类似：/path/to/mac-search-app.app/Contents/MacOS/mac-search-app
		appResourcePath := filepath.Join(filepath.Dir(exePath), "..", "Resources", "mac-file-search")
		if absPath, err := filepath.Abs(appResourcePath); err == nil {
			if _, err := os.Stat(absPath); err == nil {
				macFileScanPath = absPath
				logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 找到可执行文件(APP包内): %s", macFileScanPath)
			}
		}
	}

	// 查找顺序2：开发环境 bin/mac-file-search
	if macFileScanPath == "" {
		binPath := filepath.Join("bin", "mac-file-search")
		if absPath, err := filepath.Abs(binPath); err == nil {
			if _, err := os.Stat(absPath); err == nil {
				macFileScanPath = absPath
				logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 找到可执行文件(开发环境): %s", macFileScanPath)
			}
		}
	}

	// 查找顺序3：../mac-file-search
	if macFileScanPath == "" {
		parentPath := filepath.Join("..", "mac-file-search")
		if absPath, err := filepath.Abs(parentPath); err == nil {
			if _, err := os.Stat(absPath); err == nil {
				macFileScanPath = absPath
				logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 找到可执行文件(父目录): %s", macFileScanPath)
			}
		}
	}

	// 查找顺序4：/usr/local/bin/mac-file-search
	if macFileScanPath == "" {
		systemPath := "/usr/local/bin/mac-file-search"
		if _, err := os.Stat(systemPath); err == nil {
			macFileScanPath = systemPath
			logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 找到可执行文件(系统路径): %s", macFileScanPath)
		}
	}

	if macFileScanPath == "" {
		return fmt.Errorf("找不到mac-file-search可执行文件，已尝试: APP包内, bin/, ../, /usr/local/bin/")
	}

	if debugLog != nil {
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 可执行文件: %s", macFileScanPath)
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 扫描路径: %s", rootPath)
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 输出文件: %s", tmpFile)
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 进度文件: %s", progressFile)
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 排除路径: %s", excludeArgs)
	}

	// 构建命令
	password := idx.getSudoPassword()
	if password == "" {
		return fmt.Errorf("sudo密码未设置")
	}

	// 使用sudo执行mac-file-search，添加 -progress-file 参数以获取实时进度
	cmdStr := fmt.Sprintf("echo '%s' | sudo -S '%s' -path '%s' -output '%s' -progress-file '%s' %s",
		password, macFileScanPath, rootPath, tmpFile, progressFile, excludeArgs)

	if debugLog != nil {
		// 记录命令（隐藏密码）
		logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 执行命令: sudo '%s' -path '%s' -output '%s' -progress-file '%s' %s",
			macFileScanPath, rootPath, tmpFile, progressFile, excludeArgs)
	}

	logWithTime("调用mac-file-search扫描（预计2分钟）")
	cmd := exec.Command("sh", "-c", cmdStr)

	// 启动命令
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动mac-file-search: %v", err)
	}

	// 启动goroutine监控进度文件
	scanStartTime := time.Now()
	stopProgress := make(chan bool)
	var totalFilesScanned int64 // 记录扫描总文件数，用于导入阶段计算进度

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond) // 每0.5秒读取一次进度
		defer ticker.Stop()

		lastOffset := int64(0)
		for {
			select {
			case <-stopProgress:
				return
			case <-ticker.C:
				// 读取进度文件的新内容
				if file, err := os.Open(progressFile); err == nil {
					file.Seek(lastOffset, 0)
					scanner := bufio.NewScanner(file)
					for scanner.Scan() {
						line := scanner.Text()
						// 解析JSON进度
						var progress struct {
							Elapsed      float64 `json:"elapsed"`
							DirCount     int64   `json:"dirCount"`
							FileCount    int64   `json:"fileCount"`
							TotalDisk    int64   `json:"totalDisk"`
							DiskUsedSize int64   `json:"diskUsedSize"`
							Percentage   float64 `json:"percentage"`
							DirSpeed     float64 `json:"dirSpeed"`
							FileSpeed    float64 `json:"fileSpeed"`
							DiskSpeed    float64 `json:"diskSpeed"`
							ErrorCount   int64   `json:"errorCount"`
						}
						if err := json.Unmarshal([]byte(line), &progress); err == nil {
							// 扫描阶段：进度映射到0-70%
							// 将原始进度（0-99.9%）映射到0-70%
							mappedPercentage := progress.Percentage * 0.7

							// 计算映射后的totalDisk（让前端进度条显示0-70%）
							var mappedTotalDisk int64
							if progress.DiskUsedSize > 0 {
								mappedTotalDisk = int64(float64(progress.DiskUsedSize) * mappedPercentage / 100)
							}

							// 更新内部计数器
							idx.fileCount.Store(progress.FileCount)
							idx.dirCount.Store(progress.DirCount)
							idx.totalDisk.Store(mappedTotalDisk)
							totalFilesScanned = progress.FileCount + progress.DirCount

							// 触发进度回调（如果有）
							if idx.onProgress != nil {
								// 传递映射后的totalDisk和diskUsedSize
								idx.onProgress(progress.FileCount, progress.DirCount, mappedTotalDisk, progress.Elapsed)
							}
						}
					}
					// 记录当前读取位置
					lastOffset, _ = file.Seek(0, 1)
					file.Close()
				}
			}
		}
	}()

	// 等待命令完成，同时监控停止标志
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// 定期检查停止标志
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var err error
	for {
		select {
		case err = <-done:
			// 命令完成
			close(stopProgress) // 停止进度监控
			goto scanComplete
		case <-ticker.C:
			// 检查停止标志
			if idx.stopFlag.Load() {
				logToDebugWithTime(debugLog, "[STOP] 检测到停止标志，终止mac-file-search进程")
				// 杀死进程
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				close(stopProgress)
				return fmt.Errorf("用户停止索引")
			}
		}
	}

scanComplete:
	scanDuration := time.Since(scanStartTime).Seconds()

	if err != nil {
		return fmt.Errorf("mac-file-search执行失败: %v", err)
	}

	logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 扫描完成，耗时: %.2f秒", scanDuration)
	logWithTime("扫描完成，耗时: %.2f秒", scanDuration)

	// 检查输出文件
	fileInfo, err := os.Stat(tmpFile)
	if err != nil {
		return fmt.Errorf("输出文件不存在: %v", err)
	}
	logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 输出文件大小: %.2f MB", float64(fileInfo.Size())/(1024*1024))

	// 记录扫描阶段的diskUsedSize，用于导入阶段计算进度（70%-100%）
	// 从进度文件获取最终的diskUsedSize
	var finalDiskUsedSize int64
	if totalFilesScanned > 0 {
		// 从最后一次进度更新获取diskUsedSize
		if file, err := os.Open(progressFile); err == nil {
			scanner := bufio.NewScanner(file)
			var lastLine string
			for scanner.Scan() {
				lastLine = scanner.Text()
			}
			if lastLine != "" {
				var progress struct {
					DiskUsedSize int64 `json:"diskUsedSize"`
				}
				if err := json.Unmarshal([]byte(lastLine), &progress); err == nil {
					finalDiskUsedSize = progress.DiskUsedSize
				}
			}
			file.Close()
		}
	}

	// 解析JSON文件并导入数据库
	logWithTime("解析JSON并导入数据库")
	parseStart := time.Now()

	// 重置计数器，导入阶段从0开始重新统计实际导入的文件数
	// 扫描阶段的计数来自进度文件（可能包含跳过的文件），导入阶段统计实际插入的文件
	idx.fileCount.Store(0)
	idx.dirCount.Store(0)

	file, err := os.Open(tmpFile)
	if err != nil {
		return fmt.Errorf("无法打开输出文件: %v", err)
	}
	defer file.Close()

	// 开始事务
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("无法开始事务: %v", err)
	}
	defer tx.Rollback()

	// 批量INSERT策略：不再逐行INSERT，而是构建批量INSERT语句
	// INSERT INTO files VALUES (?,?,?),(?,?,?),...
	// 这样可以大幅减少SQL执行次数

	// 逐行解析JSON
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var lineCount int64
	var insertCount int64
	var localFileCount int64 // 本地计数器，用于统计实际导入的文件数
	var localDirCount int64  // 本地计数器，用于统计实际导入的目录数

	// SQLite参数限制：SQLITE_MAX_VARIABLE_NUMBER = 32766
	// 每条记录7个字段，所以最多 32766/7 = 4680 条
	// 为安全起见，设置为4500条一批
	batchSize := 4500 // 4500条一批，对于200万文件需要445次SQL

	// 批量INSERT缓冲区
	var batchValues []interface{}
	currentBatch := 0

	// 执行批量INSERT的函数
	executeBatch := func() error {
		if currentBatch == 0 {
			return nil
		}

		// 构建批量INSERT语句
		// INSERT INTO files VALUES (?,?,?,?,?,?,?),(?,?,?,?,?,?,?),...
		placeholders := strings.Repeat("(?,?,?,?,?,?,?),", currentBatch)
		placeholders = placeholders[:len(placeholders)-1] // 去掉最后一个逗号

		sql := "INSERT INTO files (path, name, size, mod_time, is_dir, ext, indexed_path) VALUES " + placeholders
		_, err := tx.Exec(sql, batchValues...)
		if err != nil {
			return fmt.Errorf("批量插入失败: %v", err)
		}

		// 清空缓冲区
		batchValues = batchValues[:0]
		currentBatch = 0

		// 优化：每10万条才输出一次进度，减少日志量
		if insertCount%100000 == 0 {
			logToDebugWithTime(debugLog, "[PROGRESS] 已插入: %d 条", insertCount)
		}

		return nil
	}

	// 进度更新定时器（每1秒更新一次前端）
	lastProgressTime := time.Now()
	progressInterval := 1 * time.Second

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		// 检查停止标志
		if idx.stopFlag.Load() {
			logToDebugWithTime(debugLog, "[STOP] 检测到停止标志，停止导入（已导入 %d 条）", insertCount)
			// 执行已有的批次
			executeBatch()
			// 回滚事务
			tx.Rollback()
			return fmt.Errorf("用户停止索引")
		}

		// 跳过注释行
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// 解析JSON
		var entry struct {
			Path      string `json:"path"`
			Name      string `json:"name"`
			Size      int64  `json:"size"`
			ModTime   int64  `json:"mod_time"` // 添加修改时间
			IsDir     bool   `json:"is_dir"`
			DiskUsage int64  `json:"disk_usage"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			logToDebugWithTime(debugLog, "[WARN] 解析JSON失败(行%d): %v", lineCount, err)
			continue
		}

		// 添加到批量INSERT缓冲区
		ext := strings.ToLower(filepath.Ext(entry.Name))
		isDir := 0
		if entry.IsDir {
			isDir = 1
			localDirCount++ // 使用本地计数器
		} else {
			localFileCount++ // 使用本地计数器
		}

		batchValues = append(batchValues, entry.Path, entry.Name, entry.Size, entry.ModTime, isDir, ext, rootPath)
		insertCount++
		currentBatch++

		// 更新导入进度（每1秒一次）
		// 导入阶段：从70%到100%
		if time.Since(lastProgressTime) >= progressInterval {
			if idx.onProgress != nil && totalFilesScanned > 0 {
				elapsed := time.Since(idx.buildStartTime).Seconds()
				// 计算导入进度：已插入数量 / 总扫描数量
				importProgress := float64(insertCount) / float64(totalFilesScanned)
				// 映射到70%-100%：70% + importProgress * 30%
				mappedPercentage := 70.0 + importProgress*30.0
				// 计算映射后的totalDisk
				var mappedTotalDisk int64
				if finalDiskUsedSize > 0 {
					mappedTotalDisk = int64(float64(finalDiskUsedSize) * mappedPercentage / 100)
				}
				idx.totalDisk.Store(mappedTotalDisk)
				// 更新全局计数器为当前实际导入的数量
				idx.fileCount.Store(localFileCount)
				idx.dirCount.Store(localDirCount)
				idx.onProgress(localFileCount, localDirCount, mappedTotalDisk, elapsed)
			}
			lastProgressTime = time.Now()
		}

		// 批量提交
		if currentBatch >= batchSize {
			if err := executeBatch(); err != nil {
				return err
			}
		}

		// 更新进度（每10万行）
		if lineCount%100000 == 0 {
			logWithTime("已解析: %d 行，已插入: %d 条", lineCount, insertCount)
		}
	}

	// 插入剩余的批次
	if err := executeBatch(); err != nil {
		return err
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取文件失败: %v", err)
	}

	parseDuration := time.Since(parseStart).Seconds()
	logToDebugWithTime(debugLog, "[MAC-FILE-SEARCH] 解析完成，共%d行，插入%d条，耗时: %.2f秒",
		lineCount, insertCount, parseDuration)
	logWithTime("解析完成，共%d行，插入%d条，耗时: %.2f秒", lineCount, insertCount, parseDuration)

	// 更新最终的计数器（使用本地计数的结果）
	idx.fileCount.Store(localFileCount)
	idx.dirCount.Store(localDirCount)

	// 保存统计信息到config表（避免每次COUNT(*)）
	// 这样打开APP时可以立即显示统计，无需等待COUNT查询
	fileCount := localFileCount
	dirCount := localDirCount
	total := fileCount + dirCount
	scanTime := time.Now().Unix()

	if err := idx.saveStats(fileCount, dirCount, total, scanTime); err != nil {
		if debugLog != nil {
			debugLog.WriteString(fmt.Sprintf("[WARN] 保存统计信息失败: %v\n", err))
		}
		// 失败不影响主流程
	}

	return nil
}

// saveStats 保存统计信息到config表
func (idx *Indexer) saveStats(fileCount, dirCount, total, scanTime int64) error {
	// 使用JSON保存统计信息
	stats := map[string]interface{}{
		"fileCount": fileCount,
		"dirCount":  dirCount,
		"total":     total,
		"scanTime":  scanTime,
	}

	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}

	_, err = idx.db.Exec(`
		INSERT OR REPLACE INTO config (key, value)
		VALUES ('index_stats', ?)
	`, string(statsJSON))

	return err
}

// StopIndexing 停止索引
func (idx *Indexer) StopIndexing() {
	// 设置停止标志
	idx.stopFlag.Store(true)
}

// loadExcludePaths 从数据库加载排除路径
func (idx *Indexer) loadExcludePaths() error {
	var jsonData string
	err := idx.db.QueryRow("SELECT value FROM config WHERE key = 'exclude_paths'").Scan(&jsonData)
	if err == sql.ErrNoRows {
		// 没有保存的排除路径，使用空列表
		return nil
	}
	if err != nil {
		return err
	}

	var paths []string
	if err := json.Unmarshal([]byte(jsonData), &paths); err != nil {
		return fmt.Errorf("反序列化排除路径失败: %v", err)
	}

	// 处理路径（转换为绝对路径并解析符号链接）
	idx.excludeMu.Lock()
	defer idx.excludeMu.Unlock()

	var processedPaths []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// 转换为绝对路径
		absExclude, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		processedPaths = append(processedPaths, absExclude)

		// 使用 realpath 命令获取规范路径（可以处理 firmlink）
		cmd := exec.Command("realpath", absExclude)
		output, err := cmd.Output()
		if err == nil {
			realPath := strings.TrimSpace(string(output))
			if realPath != absExclude && realPath != "" {
				processedPaths = append(processedPaths, realPath)
			}
		}
	}

	idx.excludePaths = processedPaths
	return nil
}

// SetExcludePaths 设置要排除的路径列表（参考 main.go 的实现）
func (idx *Indexer) SetExcludePaths(paths []string) error {
	// 打开 debugLog 一次，在所有路径处理完成后关闭
	homeDir, _ := os.UserHomeDir()
	debugLogPath := filepath.Join(homeDir, ".mac-search-app", "debug.log")
	debugLog, _ := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if debugLog != nil {
		defer debugLog.Close() // 确保函数返回时关闭
	}

	// 处理路径：转换为绝对路径并解析符号链接
	var processedPaths []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// 转换为绝对路径
		absExclude, err := filepath.Abs(p)
		if err != nil {
			// 如果路径无效，记录日志但继续处理其他路径
			if debugLog != nil {
				debugLog.WriteString(fmt.Sprintf("[WARN] 无法解析排除路径 %s: %v (时间: %s)\n",
					p, err, time.Now().Format("15:04:05")))
			}
			continue
		}
		processedPaths = append(processedPaths, absExclude)

		// 使用 realpath 命令获取规范路径（可以处理 firmlink 和符号链接）
		cmd := exec.Command("realpath", absExclude)
		output, err := cmd.Output()
		if err == nil {
			realPath := strings.TrimSpace(string(output))
			if realPath != absExclude && realPath != "" {
				processedPaths = append(processedPaths, realPath)
				// 记录日志
				if debugLog != nil {
					debugLog.WriteString(fmt.Sprintf("[INFO] 排除路径: %s (realpath: %s) (时间: %s)\n",
						absExclude, realPath, time.Now().Format("15:04:05")))
				}
			}
		} else if debugLog != nil {
			debugLog.WriteString(fmt.Sprintf("[WARN] realpath 失败: %s, err: %v (时间: %s)\n",
				absExclude, err, time.Now().Format("15:04:05")))
		}
	}

	// 更新内存中的排除路径（在锁内更新）
	idx.excludeMu.Lock()
	idx.excludePaths = processedPaths
	idx.excludeMu.Unlock()

	// 保存到数据库（只保存用户输入的原始路径，不保存处理后的路径）
	// 使用 JSON 格式存储
	jsonData, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("序列化排除路径失败: %v", err)
	}

	_, err = idx.db.Exec(`
		INSERT OR REPLACE INTO config (key, value)
		VALUES ('exclude_paths', ?)
	`, string(jsonData))
	if err != nil {
		return fmt.Errorf("保存排除路径失败: %v", err)
	}

	return nil
}

// GetExcludePaths 获取已保存的排除路径列表（返回用户输入的原始路径）
func (idx *Indexer) GetExcludePaths() ([]string, error) {
	var jsonData string
	err := idx.db.QueryRow("SELECT value FROM config WHERE key = 'exclude_paths'").Scan(&jsonData)
	if err == sql.ErrNoRows {
		// 没有保存的排除路径，返回空列表
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	var paths []string
	if err := json.Unmarshal([]byte(jsonData), &paths); err != nil {
		return nil, fmt.Errorf("反序列化排除路径失败: %v", err)
	}

	return paths, nil
}

// shouldExcludePath 判断路径是否应该被排除（参考 main.go 的实现）
func (idx *Indexer) shouldExcludePath(path string) bool {
	idx.excludeMu.RLock()
	defer idx.excludeMu.RUnlock()

	// 快速检查：直接匹配原始路径
	for _, excludePath := range idx.excludePaths {
		// 精确匹配
		if path == excludePath {
			return true
		}
		// 前缀匹配
		if strings.HasPrefix(path, excludePath+string(filepath.Separator)) {
			return true
		}
	}

	// 如果路径以 /System/Volumes/Data 开头，尝试使用 realpath 解析
	// 只对这种特殊路径调用 realpath，避免性能问题
	if strings.HasPrefix(path, "/System/Volumes/Data/") {
		// 尝试从缓存获取 realpath
		var realPath string
		if cached, ok := idx.realpathCache.Load(path); ok {
			realPath, _ = cached.(string)
		} else {
			// 缓存未命中，调用 realpath 并缓存结果
			cmd := exec.Command("realpath", path)
			output, err := cmd.Output()
			if err == nil {
				realPath = strings.TrimSpace(string(output))
			} else {
				realPath = path // 失败则使用原路径
			}
			idx.realpathCache.Store(path, realPath)
		}

		// 如果 realpath 返回了不同的路径，再次检查排除列表
		if realPath != path && realPath != "" {
			for _, excludePath := range idx.excludePaths {
				if realPath == excludePath {
					return true
				}
				if strings.HasPrefix(realPath, excludePath+string(filepath.Separator)) {
					return true
				}
			}
		}
	}

	return false
}

// shouldSkip 判断是否应该跳过某些路径
func (idx *Indexer) shouldSkip(path string) bool {
	// 首先检查用户配置的排除路径
	if idx.shouldExcludePath(path) {
		return true
	}

	// 然后检查系统默认的跳过路径
	skipPaths := []string{
		"/dev",
		"/private/var/vm",
		"/System/Volumes/VM",
		"/System/Volumes/Preboot",
		"/System/Volumes/Update",
		"/.Spotlight-V100",
		"/.fseventsd",
		"/.Trashes",
	}

	for _, skip := range skipPaths {
		if strings.HasPrefix(path, skip) {
			return true
		}
	}

	return false
}

// formatDuration 格式化时间
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// IndexedPath 表示一个已索引的路径及其统计信息
type IndexedPath struct {
	Path      string `json:"path"`
	FileCount int64  `json:"file_count"`
	DirCount  int64  `json:"dir_count"`
}

// GetIndexedPaths 获取所有已索引的路径及其统计信息（直接读取indexed_paths表）
func (idx *Indexer) GetIndexedPaths() ([]IndexedPath, error) {
	rows, err := idx.db.Query(`
		SELECT path, file_count, dir_count
		FROM indexed_paths
		ORDER BY path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []IndexedPath
	for rows.Next() {
		var p IndexedPath
		if err := rows.Scan(&p.Path, &p.FileCount, &p.DirCount); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}

	return paths, rows.Err()
}

// DeleteIndexedPath 删除指定路径的索引（异步后台删除）
func (idx *Indexer) DeleteIndexedPath(path string) error {
	// 先从 indexed_paths 表中删除记录（秒级操作，立即返回）
	_, err := idx.db.Exec("DELETE FROM indexed_paths WHERE path = ?", path)
	if err != nil {
		logWithTime("警告：删除indexed_paths表记录失败: %v", err)
	}

	// 提交后台删除任务：DELETE + checkpoint（去掉VACUUM避免退出卡顿）
	if idx.cleanupTaskFunc != nil {
		idx.cleanupTaskFunc(func() {
			// 写入清理日志
			homeDir, _ := os.UserHomeDir()
			logPath := filepath.Join(homeDir, ".mac-search-app", "cleanup.log")
			logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if logFile != nil {
				defer logFile.Close()

				msg := fmt.Sprintf("[%s] 开始后台删除索引路径: %s\n", time.Now().Format("15:04:05"), path)
				logFile.WriteString(msg)

				// DELETE 操作
				deleteStart := time.Now()
				result, err := idx.db.Exec("DELETE FROM files WHERE indexed_path = ?", path)
				if err != nil {
					msg = fmt.Sprintf("[%s] DELETE 失败: %v\n", time.Now().Format("15:04:05"), err)
					logFile.WriteString(msg)
					return
				}

				affected, _ := result.RowsAffected()
				msg = fmt.Sprintf("[%s] DELETE 完成，删除 %d 条记录，耗时: %.2f秒\n",
					time.Now().Format("15:04:05"), affected, time.Since(deleteStart).Seconds())
				logFile.WriteString(msg)

				if affected == 0 {
					msg = fmt.Sprintf("[%s] 无记录被删除，跳过 checkpoint\n", time.Now().Format("15:04:05"))
					logFile.WriteString(msg)
					return
				}

				// WAL checkpoint（快速，不执行VACUUM）
				msg = fmt.Sprintf("[%s] 开始执行 WAL checkpoint...\n", time.Now().Format("15:04:05"))
				logFile.WriteString(msg)

				checkpointStart := time.Now()

				// 执行 checkpoint 并检查返回值
				var busy, log, checkpointed int
				err = idx.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
				elapsed := time.Since(checkpointStart).Seconds()

				if err != nil {
					msg = fmt.Sprintf("[%s] WAL checkpoint 失败: %v, 耗时: %.2f秒\n", time.Now().Format("15:04:05"), err, elapsed)
					logFile.WriteString(msg)
				} else {
					msg = fmt.Sprintf("[%s] WAL checkpoint 完成: busy=%d, log=%d, checkpointed=%d, 耗时: %.2f秒\n",
						time.Now().Format("15:04:05"), busy, log, checkpointed, elapsed)
					logFile.WriteString(msg)

					// 检查文件大小
					homeDir, _ := os.UserHomeDir()
					dbPath := filepath.Join(homeDir, ".mac-search-app", "index.db-wal")
					if info, err := os.Stat(dbPath); err == nil {
						msg = fmt.Sprintf("[%s] WAL 文件大小: %.2f MB\n", time.Now().Format("15:04:05"), float64(info.Size())/(1024*1024))
						logFile.WriteString(msg)
					}
				}

				msg = fmt.Sprintf("[%s] 删除任务完成（数据库文件空间将在下次插入时重用）\n", time.Now().Format("15:04:05"))
				logFile.WriteString(msg)
			}
		})
	}

	logWithTime("索引删除任务已提交到后台，路径: %s", path)
	return nil
}
