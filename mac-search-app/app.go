package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>

extern void setupAppDelegate();
extern char* getFileIconBase64(const char* filePath);
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx            context.Context
	indexer        *Indexer
	watcher        *Watcher
	cleanupTasksCh chan func()   // 清理任务通道
	cleanupDone    chan struct{} // 清理完成信号
	windowHidden   atomic.Bool   // 窗口是否被隐藏（使用atomic保证线程安全）
}

// 全局context，供Objective-C回调使用
var globalApp *App

//export onDockIconClick
// 由 Cocoa 主线程调用。在 goroutine 里延迟执行窗口显示，避免在 CGO 回调线程直接调 Wails 导致闪退。
func onDockIconClick() {
	if globalApp == nil {
		return
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		if globalApp == nil || !globalApp.windowHidden.Load() {
			return
		}
		globalApp.windowHidden.Store(false)
		runtime.WindowShow(globalApp.ctx)
		runtime.WindowUnminimise(globalApp.ctx)
		runtime.EventsEmit(globalApp.ctx, "window-shown", nil)
	}()
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		cleanupTasksCh: make(chan func(), 100), // 缓冲100个清理任务
		cleanupDone:    make(chan struct{}),
	}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	globalApp = a // 设置全局引用供Objective-C回调使用

	fmt.Println("[Go] startup 开始，准备设置 AppDelegate")

	// 设置macOS应用委托，监听程序坞图标点击
	C.setupAppDelegate()

	fmt.Println("[Go] setupAppDelegate 调用完成")

	// 启动后台清理任务处理器
	go a.cleanupTaskWorker()

	// 初始化索引器
	homeDir, _ := os.UserHomeDir()
	dbPath := filepath.Join(homeDir, ".mac-search-app", "index.db")
	os.MkdirAll(filepath.Dir(dbPath), 0755)

	indexer, err := NewIndexer(dbPath)
	if err != nil {
		fmt.Printf("初始化索引器失败: %v\n", err)
		return
	}
	a.indexer = indexer

	// 设置清理任务回调
	a.indexer.cleanupTaskFunc = func(task func()) {
		// 写入日志
		homeDir, _ := os.UserHomeDir()
		logPath := filepath.Join(homeDir, ".mac-search-app", "cleanup.log")
		logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if logFile != nil {
			defer logFile.Close()
			msg := fmt.Sprintf("[%s] 任务已提交到队列\n", time.Now().Format("15:04:05"))
			logFile.WriteString(msg)
		}

		select {
		case a.cleanupTasksCh <- task:
			// 任务已提交
		default:
			// 通道已满或已关闭，直接执行
			if logFile != nil {
				msg := fmt.Sprintf("[%s] 警告：清理任务通道已满，同步执行任务\n", time.Now().Format("15:04:05"))
				logFile.WriteString(msg)
			}
			task()
		}
	}

	// 延迟检查缓存索引，确保前端已经准备好接收事件
	go func() {
		time.Sleep(100 * time.Millisecond) // 等待前端初始化完成
		stats, err := a.GetIndexStats()
		if err == nil && stats != nil {
			total, ok := stats["total"].(int64)
			if ok && total > 0 {
				// 有缓存索引，通知前端展示缓存信息
				runtime.EventsEmit(a.ctx, "index-cached", stats)
				fmt.Printf("检测到缓存索引: %d 文件, %d 目录\n", stats["fileCount"], stats["dirCount"])
			} else {
				fmt.Printf("没有缓存索引或索引为空\n")
			}
		} else if err != nil {
			fmt.Printf("获取索引统计失败: %v\n", err)
		}
	}()
}

// ShowWindow 显示窗口（供前端调用）
func (a *App) ShowWindow() {
	runtime.WindowShow(a.ctx)
	a.windowHidden.Store(false)
}

// HideWindow 隐藏窗口（供前端调用）
func (a *App) HideWindow() {
	fmt.Println("[Go] HideWindow 被调用")
	runtime.WindowHide(a.ctx)
	a.windowHidden.Store(true)
	fmt.Printf("[Go] 窗口已隐藏，windowHidden=%v\n", a.windowHidden.Load())
}

// cleanupTaskWorker 后台清理任务处理器
func (a *App) cleanupTaskWorker() {
	taskCount := 0

	// 写入日志文件
	homeDir, _ := os.UserHomeDir()
	logPath := filepath.Join(homeDir, ".mac-search-app", "cleanup.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("无法打开清理日志文件: %v\n", err)
		return
	}
	defer logFile.Close()

	log := func(format string, args ...interface{}) {
		msg := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
		logFile.WriteString(msg)
		fmt.Print(msg)
	}

	log("后台清理任务处理器已启动")

	for task := range a.cleanupTasksCh {
		taskCount++
		log("开始执行第 %d 个任务", taskCount)
		startTime := time.Now()
		task() // 执行清理任务
		log("第 %d 个任务完成，耗时: %.2f秒", taskCount, time.Since(startTime).Seconds())
	}

	log("处理器关闭，共执行了 %d 个任务", taskCount)
	close(a.cleanupDone)
}

// shutdown 清理资源
func (a *App) shutdown(ctx context.Context) {
	fmt.Println("应用正在关闭...")

	// 关闭清理任务通道，不再接受新任务
	close(a.cleanupTasksCh)

	// 不等待清理任务完成，直接退出
	// 清理任务会继续在后台运行直到完成或进程被杀死
	fmt.Println("后台清理任务将继续运行...")

	if a.watcher != nil {
		a.watcher.Stop()
	}
	if a.indexer != nil {
		a.indexer.Close()
	}

	fmt.Println("应用已关闭")
}

// SetExcludePaths 设置要排除的路径列表
func (a *App) SetExcludePaths(paths []string) error {
	if a.indexer == nil {
		return fmt.Errorf("索引器未初始化")
	}
	return a.indexer.SetExcludePaths(paths)
}

// GetExcludePaths 获取已保存的排除路径列表
func (a *App) GetExcludePaths() ([]string, error) {
	if a.indexer == nil {
		return nil, fmt.Errorf("索引器未初始化")
	}
	return a.indexer.GetExcludePaths()
}

// BuildIndex 构建索引
func (a *App) BuildIndex(path string) error {
	if a.indexer == nil {
		return fmt.Errorf("索引器未初始化")
	}

	// 注意：不再在这里请求 sudo 权限
	// 前端会在用户点击"构建索引"时弹出密码输入界面，用户输入密码后调用 SetSudoPassword

	startTime := time.Now()

	// 后台异步执行 du 计算目录大小，完成后通知前端
	// 这样不会阻塞索引，但可以提供进度百分比
	diskUsedSize := int64(0)
	diskUsedSizeMu := &sync.RWMutex{}

	go func() {
		// 最多等待 60 秒
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "du", "-sk", path)
		output, err := cmd.Output()

		if err == nil {
			var sizeKB int64
			fmt.Sscanf(string(output), "%d", &sizeKB)
			realDiskUsedSize := sizeKB * 1024

			fmt.Printf("[DU] 目录大小: %.2f GB\n", float64(realDiskUsedSize)/(1024*1024*1024))

			// 更新 diskUsedSize
			diskUsedSizeMu.Lock()
			diskUsedSize = realDiskUsedSize
			diskUsedSizeMu.Unlock()

			// 通知前端更新进度条
			runtime.EventsEmit(a.ctx, "disk-size-calculated", map[string]interface{}{
				"diskUsedSize": realDiskUsedSize,
			})
		} else if ctx.Err() == context.DeadlineExceeded {
			fmt.Printf("[DU] 命令超时（超过60秒），不显示进度百分比\n")
		} else {
			fmt.Printf("[DU] 命令失败: %v，不显示进度百分比\n", err)
		}
	}()

	a.indexer.onProgress = func(fileCount, dirCount int64, totalDisk int64, elapsed float64) {
		diskUsedSizeMu.RLock()
		currentDiskUsedSize := diskUsedSize
		diskUsedSizeMu.RUnlock()

		runtime.EventsEmit(a.ctx, "indexing-progress", map[string]interface{}{
			"fileCount":    fileCount,
			"dirCount":     dirCount,
			"total":        fileCount + dirCount,
			"totalDisk":    totalDisk,
			"elapsed":      elapsed,
			"diskUsedSize": currentDiskUsedSize,
		})
	}

	a.indexer.onScanFile = func(filePath string) {
		runtime.EventsEmit(a.ctx, "scanning-file", filePath)
	}

	// 调用indexer.BuildIndex，并传入通知回调
	err := a.indexer.BuildIndex(path, func() {
		runtime.EventsEmit(a.ctx, "indexing-start", map[string]interface{}{
			"path": path,
		})
	})

	if err != nil {
		return err
	}

	elapsed := time.Since(startTime)

	runtime.EventsEmit(a.ctx, "indexing-complete", map[string]interface{}{
		"fileCount": a.indexer.fileCount.Load(),
		"dirCount":  a.indexer.dirCount.Load(),
		"elapsed":   elapsed.Seconds(),
	})

	// 暂时禁用文件监听，避免打开太多文件句柄
	// TODO: 实现更高效的文件监听机制（例如：只监听根目录，或使用 kqueue/FSEvents）
	/*
		watcher, err := NewWatcher(a.indexer, path)
		if err != nil {
			return err
		}

		if err := watcher.Start(); err != nil {
			return err
		}

		a.watcher = watcher
		fmt.Println("索引构建完成，文件监听已启动")
	*/

	fmt.Println("索引构建完成（文件监听已禁用）")

	return nil
}

// Search 搜索文件（支持分页）
func (a *App) Search(keyword string, useRegex bool, offset int) ([]FileEntry, error) {
	if a.indexer == nil {
		return nil, fmt.Errorf("索引器未初始化")
	}

	return a.indexer.SearchWithPagination(keyword, useRegex, offset, 500)
}

// SearchAdvanced 高级搜索
func (a *App) SearchAdvanced(opts SearchOptions) ([]FileEntry, error) {
	if a.indexer == nil {
		return nil, fmt.Errorf("索引器未初始化")
	}

	return a.indexer.SearchAdvanced(opts)
}

// CopyToClipboard 复制文本到剪贴板
func (a *App) CopyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	_, err = pipe.Write([]byte(text))
	if err != nil {
		return err
	}
	pipe.Close()

	return cmd.Wait()
}

// SelectFolder 选择文件夹
func (a *App) SelectFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择要索引的文件夹",
	})
}

// RebuildIndex 重建索引
func (a *App) RebuildIndex(path string) error {
	if a.indexer == nil {
		return fmt.Errorf("索引器未初始化")
	}

	// 停止当前监听
	if a.watcher != nil {
		a.watcher.Stop()
		a.watcher = nil
	}

	// 在新的goroutine中重建索引
	// BuildIndex会自己管理buildMu锁，确保不会同时运行多个构建
	go func() {
		if err := a.BuildIndex(path); err != nil {
			fmt.Printf("重建索引失败: %v\n", err)
		}
	}()

	return nil
}

// StopIndexing 停止索引
func (a *App) StopIndexing() error {
	if a.indexer == nil {
		return fmt.Errorf("索引器未初始化")
	}

	a.indexer.StopIndexing()

	// 停止文件监听
	if a.watcher != nil {
		a.watcher.Stop()
		a.watcher = nil
	}

	// 通知前端索引已停止
	runtime.EventsEmit(a.ctx, "indexing-stopped", nil)

	return nil
}

// GetIndexStats 获取索引统计
func (a *App) GetIndexStats() (map[string]interface{}, error) {
	if a.indexer == nil {
		return nil, fmt.Errorf("索引器未初始化")
	}

	fileCount, dirCount, err := a.indexer.GetStats()
	if err != nil {
		return nil, err
	}

	indexPath, _ := a.indexer.GetIndexPath()

	return map[string]interface{}{
		"fileCount": fileCount,
		"dirCount":  dirCount,
		"total":     fileCount + dirCount,
		"indexPath": indexPath,
	}, nil
}

// OpenInFinder 在 Finder 中打开文件
func (a *App) OpenInFinder(path string) error {
	return executeCommand("open", "-R", path)
}

// OpenFile 打开文件
func (a *App) OpenFile(path string) error {
	return executeCommand("open", path)
}

// GetPerformanceLog 获取性能日志
func (a *App) GetPerformanceLog() (string, error) {
	homeDir, _ := os.UserHomeDir()
	logPath := filepath.Join(homeDir, ".mac-search-app", "performance.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetSudoPassword 设置 sudo 密码（参考 SwitchHosts 的做法）
// 前端会弹出密码输入界面，用户输入密码后调用此方法
func (a *App) SetSudoPassword(password string) {
	if a.indexer == nil {
		return
	}
	a.indexer.SetSudoPassword(password)
	fmt.Println("sudo密码已设置")
}

// HasSudoPassword 检查是否已设置 sudo 密码
func (a *App) HasSudoPassword() bool {
	if a.indexer == nil {
		return false
	}
	return a.indexer.HasSudoPassword()
}

// GetIndexedPaths 获取所有已索引的路径
func (a *App) GetIndexedPaths() ([]map[string]interface{}, error) {
	if a.indexer == nil {
		return nil, fmt.Errorf("索引器未初始化")
	}

	paths, err := a.indexer.GetIndexedPaths()
	if err != nil {
		return nil, err
	}

	// 转换为map格式方便前端使用
	result := make([]map[string]interface{}, len(paths))
	for i, p := range paths {
		result[i] = map[string]interface{}{
			"path":       p.Path,
			"file_count": p.FileCount,
			"dir_count":  p.DirCount,
		}
	}

	return result, nil
}

// DeleteIndexedPath 删除指定路径的索引
func (a *App) DeleteIndexedPath(path string) error {
	if a.indexer == nil {
		return fmt.Errorf("索引器未初始化")
	}

	// DELETE 操作同步执行，快速返回给用户
	// VACUUM 和 checkpoint 会在后台异步执行
	if err := a.indexer.DeleteIndexedPath(path); err != nil {
		return err
	}

	fmt.Printf("索引已删除: %s (空间回收正在后台进行)\n", path)
	return nil
}

// GetFileIcon 获取文件图标的base64编码
func (a *App) GetFileIcon(path string) string {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cIcon := C.getFileIconBase64(cPath)
	if cIcon == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cIcon))

	return C.GoString(cIcon)
}
