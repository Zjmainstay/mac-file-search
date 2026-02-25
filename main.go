package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// FileNode 表示文件树中的一个节点
type FileNode struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Size       int64       `json:"size"`       // 逻辑大小（文件声称的大小）
	DiskUsage  int64       `json:"disk_usage"` // 实际磁盘占用（块数 * 512）
	ModTime    int64       `json:"mod_time"`   // 修改时间（Unix timestamp）
	IsDir      bool        `json:"is_dir"`
	IsSparse   bool        `json:"is_sparse,omitempty"`   // 是否为稀疏文件
	IsHardlink bool        `json:"is_hardlink,omitempty"` // 是否为硬链接（重复的inode）
	Children   []*FileNode `json:"children,omitempty"`
	mu         sync.RWMutex
}

// ScanOptions 扫描选项
type ScanOptions struct {
	RootPath         string
	MinSize          int64
	MaxSize          int64
	WorkerCount      int
	OutputFile       string   // 输出文件路径
	ShowErrors       bool     // 是否显示错误详情
	ExcludePaths     []string // 要排除的路径列表
	IncludeExts      []string // 包含的文件扩展名列表 (如: .txt, .log)
	ExcludeExts      []string // 排除的文件扩展名列表
	NamePattern      string   // 文件名正则表达式模式
	ProgressFile     string   // 进度信息输出文件（JSON格式，供APP调用）
	nameRegex        *regexp.Regexp // 编译后的正则表达式（内部使用）
}

// Scanner 文件扫描器
type Scanner struct {
	options        ScanOptions
	root           *FileNode
	dirQueue       chan string
	taskWg         sync.WaitGroup // 任务计数
	workerWg       sync.WaitGroup // worker 计数
	nodeMap        sync.Map       // 用于快速查找父节点
	inodeMap       sync.Map       // 跟踪已处理的 inode (key: "dev:ino")
	dirInodeMap    sync.Map       // 跟踪已扫描的目录 inode，避免重复扫描（firmlinks等）
	fileCount      atomic.Int64
	dirCount       atomic.Int64
	symlinkCount   atomic.Int64   // 符号链接计数
	sparseCount    atomic.Int64   // 稀疏文件计数
	hardlinkCount  atomic.Int64   // 硬链接计数（重复的 inode）
	dupDirCount    atomic.Int64   // 重复目录计数（firmlinks等）
	excludedCount  atomic.Int64   // 排除的目录计数
	errorCount     atomic.Int64
	totalSize      atomic.Int64   // 文件逻辑大小总和
	totalDisk      atomic.Int64   // 实际磁盘占用总和（去重后）
	diskUsedSize   int64          // 磁盘已使用空间大小
	outputFile     *os.File       // 输出文件句柄
	outputMu       sync.Mutex     // 输出文件锁
}

// NewScanner 创建新的扫描器
func NewScanner(options ScanOptions) *Scanner {
	if options.WorkerCount <= 0 {
		options.WorkerCount = runtime.NumCPU() * 4
	}

	// 编译正则表达式（如果提供）
	if options.NamePattern != "" {
		regex, err := regexp.Compile(options.NamePattern)
		if err != nil {
			log.Fatalf("正则表达式编译失败: %v", err)
		}
		options.nameRegex = regex
	}

	return &Scanner{
		options:  options,
		dirQueue: make(chan string, options.WorkerCount*10),
		root: &FileNode{
			Path:     options.RootPath,
			Name:     filepath.Base(options.RootPath),
			IsDir:    true,
			Children: make([]*FileNode, 0),
		},
	}
}

// shouldIncludeFile 判断文件是否符合大小筛选条件
func (s *Scanner) shouldIncludeFile(size int64) bool {
	if s.options.MinSize > 0 && size < s.options.MinSize {
		return false
	}
	if s.options.MaxSize > 0 && size > s.options.MaxSize {
		return false
	}
	return true
}

// shouldExcludePath 判断路径是否应该被排除
func (s *Scanner) shouldExcludePath(path string) bool {
	// 检查用户指定的排除列表
	for _, excludePath := range s.options.ExcludePaths {
		// 精确匹配
		if path == excludePath {
			return true
		}
		// 前缀匹配：确保排除路径的子路径
		// 例如：排除 /Volumes/Data 应该匹配 /Volumes/Data/subdir
		// 但不应该匹配 /Volumes/DataBackup
		if strings.HasPrefix(path, excludePath+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// shouldIncludeFileByExt 判断文件扩展名是否符合过滤条件
func (s *Scanner) shouldIncludeFileByExt(filename string) bool {
	// 如果没有配置扩展名过滤，则包含所有文件
	if len(s.options.IncludeExts) == 0 && len(s.options.ExcludeExts) == 0 {
		return true
	}

	ext := strings.ToLower(filepath.Ext(filename))

	// 如果配置了排除列表，检查是否在排除列表中
	if len(s.options.ExcludeExts) > 0 {
		for _, excludeExt := range s.options.ExcludeExts {
			if ext == strings.ToLower(excludeExt) {
				return false
			}
		}
	}

	// 如果配置了包含列表，只包含列表中的扩展名
	if len(s.options.IncludeExts) > 0 {
		for _, includeExt := range s.options.IncludeExts {
			if ext == strings.ToLower(includeExt) {
				return true
			}
		}
		return false // 不在包含列表中
	}

	return true
}

// shouldIncludeFileByName 判断文件名是否符合正则表达式模式
func (s *Scanner) shouldIncludeFileByName(filename string) bool {
	// 如果没有配置正则表达式，则包含所有文件
	if s.options.nameRegex == nil {
		return true
	}

	return s.options.nameRegex.MatchString(filename)
}

// worker 工作协程，处理目录扫描
func (s *Scanner) worker(id int) {
	defer s.workerWg.Done()

	for dirPath := range s.dirQueue {
		s.scanDirectory(dirPath)
		s.taskWg.Done()
	}
}

// scanDirectory 扫描单个目录
func (s *Scanner) scanDirectory(dirPath string) {
	defer func() {
		if r := recover(); r != nil {
			s.errorCount.Add(1)
			if s.options.ShowErrors {
				fmt.Fprintf(os.Stderr, "\n⚠️  panic in %s: %v\n", dirPath, r)
			}
		}
	}()

	// 先验证路径是否仍然存在且是目录（避免竞态条件）
	info, err := os.Lstat(dirPath)
	if err != nil {
		// 文件/目录可能在扫描过程中被删除，这是正常的
		s.errorCount.Add(1)
		if s.options.ShowErrors {
			fmt.Fprintf(os.Stderr, "\n⚠️  路径不存在或无法访问 %s: %v\n", dirPath, err)
		}
		return
	}

	// 确保是目录而不是文件（避免竞态条件导致类型变化）
	if !info.IsDir() {
		// 可能在加入队列后从目录变成了文件，跳过即可
		if s.options.ShowErrors {
			fmt.Fprintf(os.Stderr, "\n⚠️  路径不再是目录 %s\n", dirPath)
		}
		return
	}

	// 检查目录是否已经扫描过（通过 dev:ino 去重，避免 firmlinks/硬链接等重复扫描）
	// 注意：只在非根目录时进行检查，根目录总是需要扫描
	if dirPath != s.options.RootPath {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok {
			dirInodeKey := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
			if _, exists := s.dirInodeMap.LoadOrStore(dirInodeKey, dirPath); exists {
				// 这个目录已经扫描过（可能是 firmlink 或其他方式的重复访问）
				// 静默跳过，这是正常的内部处理
				s.dupDirCount.Add(1)
				return
			}
		}
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		// 对于 bad file descriptor 等预期的系统错误，完全忽略（不计数、不显示）
		// 这通常发生在 /dev/fd 等动态变化的虚拟目录中
		errStr := err.Error()
		isBadFileDescriptor := strings.Contains(errStr, "bad file descriptor")
		isTooManyFiles := strings.Contains(errStr, "too many open files")

		// 只对非预期错误计数和显示
		if !isBadFileDescriptor {
			s.errorCount.Add(1)
			// "too many open files" 错误总是显示，即使没有 -errors 参数
			if s.options.ShowErrors || isTooManyFiles {
				fmt.Fprintf(os.Stderr, "\n⚠️  无法读取目录 %s: %v\n", dirPath, err)
			}
		}
		return
	}

	// 获取或创建当前目录节点
	parentNode := s.getOrCreateNode(dirPath)
	if parentNode == nil {
		s.errorCount.Add(1)
		if s.options.ShowErrors {
			fmt.Fprintf(os.Stderr, "\n⚠️  无法创建节点 %s\n", dirPath)
		}
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())

		// 优先检查是否应该排除此路径（在获取文件信息之前，节省系统调用）
		if s.shouldExcludePath(fullPath) {
			s.excludedCount.Add(1)
			continue
		}

		// 获取文件信息（不跟随符号链接）
		info, err := os.Lstat(fullPath)
		if err != nil {
			// 对于 bad file descriptor 等预期的系统错误，完全忽略
			errStr := err.Error()
			isBadFileDescriptor := strings.Contains(errStr, "bad file descriptor")
			isTooManyFiles := strings.Contains(errStr, "too many open files")

			// 只对非预期错误计数和显示
			if !isBadFileDescriptor {
				s.errorCount.Add(1)
				// "too many open files" 错误总是显示，即使没有 -errors 参数
				if s.options.ShowErrors || isTooManyFiles {
					fmt.Fprintf(os.Stderr, "\n⚠️  无法获取文件信息 %s: %v\n", fullPath, err)
				}
			}
			continue
		}

		// 跳过符号链接，避免循环引用和重复计算
		if info.Mode()&os.ModeSymlink != 0 {
			s.symlinkCount.Add(1)
			continue
		}

		// 跳过特殊文件（设备文件、socket等）
		if !info.Mode().IsRegular() && !info.Mode().IsDir() {
			continue
		}

		if info.IsDir() {
			// 创建子目录节点
			childNode := &FileNode{
				Path:     fullPath,
				Name:     entry.Name(),
				IsDir:    true,
				Children: make([]*FileNode, 0),
			}

			// 添加到父节点
			parentNode.mu.Lock()
			parentNode.Children = append(parentNode.Children, childNode)
			parentNode.mu.Unlock()

			// 存储节点映射
			s.nodeMap.Store(fullPath, childNode)
			s.dirCount.Add(1)

			// 实时写入目录信息（如果设置了文件大小筛选，则排除目录）
			if s.options.MinSize == 0 && s.options.MaxSize == 0 {
				s.writeFileRecord(childNode)
			}

			// 将子目录加入队列
			s.taskWg.Add(1)
			go func(path string) {
				s.dirQueue <- path
			}(fullPath)
		} else {
			// 处理文件
			size := info.Size()

			// 检查文件大小和扩展名过滤条件
			if !s.shouldIncludeFile(size) {
				continue
			}

			if !s.shouldIncludeFileByExt(entry.Name()) {
				continue
			}

			if !s.shouldIncludeFileByName(entry.Name()) {
				continue
			}

			// 获取实际磁盘占用
			var diskUsage int64
			var isSparse bool
			var isHardlink bool

			stat, ok := info.Sys().(*syscall.Stat_t)
			if ok {
				// Blocks 是 512 字节块的数量
				diskUsage = stat.Blocks * 512

				// 如果实际占用小于逻辑大小的 95%，认为是稀疏文件
				if size > 0 && float64(diskUsage) < float64(size)*0.95 {
					isSparse = true
					s.sparseCount.Add(1)
				}

				// 检查是否为硬链接（通过 dev:ino 去重）
				// 只对硬链接数 > 1 的文件进行去重检查
				if stat.Nlink > 1 {
					inodeKey := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
					if _, exists := s.inodeMap.LoadOrStore(inodeKey, true); exists {
						// 这是一个硬链接，已经计算过磁盘占用
						isHardlink = true
						s.hardlinkCount.Add(1)
					}
				}
			} else {
				// 无法获取块信息，使用逻辑大小
				diskUsage = size
			}

			fileNode := &FileNode{
				Path:       fullPath,
				Name:       entry.Name(),
				Size:       size,
				DiskUsage:  diskUsage,
				ModTime:    info.ModTime().Unix(), // 添加修改时间
				IsSparse:   isSparse,
				IsHardlink: isHardlink,
				IsDir:      false,
			}

			parentNode.mu.Lock()
			parentNode.Children = append(parentNode.Children, fileNode)
			parentNode.mu.Unlock()

			s.fileCount.Add(1)
			s.totalSize.Add(size)

			// 只在首次遇到 inode 时累加磁盘占用
			if !isHardlink {
				s.totalDisk.Add(diskUsage)
			}

			// 实时写入文件信息
			s.writeFileRecord(fileNode)
		}
	}
}

// writeFileRecord 实时写入文件记录
func (s *Scanner) writeFileRecord(node *FileNode) {
	if s.outputFile == nil {
		return
	}

	s.outputMu.Lock()
	defer s.outputMu.Unlock()

	// 写入JSON Lines格式，每行一个文件记录
	var line string
	if node.IsHardlink {
		line = fmt.Sprintf("{\"path\":%q,\"name\":%q,\"size\":%d,\"disk_usage\":%d,\"mod_time\":%d,\"is_dir\":%t,\"is_hardlink\":true}\n",
			node.Path, node.Name, node.Size, node.DiskUsage, node.ModTime, node.IsDir)
	} else if node.IsSparse {
		line = fmt.Sprintf("{\"path\":%q,\"name\":%q,\"size\":%d,\"disk_usage\":%d,\"mod_time\":%d,\"is_dir\":%t,\"is_sparse\":true}\n",
			node.Path, node.Name, node.Size, node.DiskUsage, node.ModTime, node.IsDir)
	} else {
		line = fmt.Sprintf("{\"path\":%q,\"name\":%q,\"size\":%d,\"disk_usage\":%d,\"mod_time\":%d,\"is_dir\":%t}\n",
			node.Path, node.Name, node.Size, node.DiskUsage, node.ModTime, node.IsDir)
	}
	_, err := s.outputFile.WriteString(line)
	if err != nil && s.options.ShowErrors {
		fmt.Fprintf(os.Stderr, "\n⚠️  写入文件失败: %v\n", err)
	}
}

// getOrCreateNode 获取或创建节点
func (s *Scanner) getOrCreateNode(path string) *FileNode {
	if path == s.options.RootPath {
		return s.root
	}

	if node, ok := s.nodeMap.Load(path); ok {
		return node.(*FileNode)
	}

	return nil
}

// Scan 开始扫描
func (s *Scanner) Scan() error {
	// 打开输出文件
	if s.options.OutputFile != "" {
		f, err := os.Create(s.options.OutputFile)
		if err != nil {
			return fmt.Errorf("无法创建输出文件: %v", err)
		}
		s.outputFile = f
		defer f.Close()

		// 写入文件头
		fmt.Fprintf(f, "# 文件扫描结果 - JSON Lines 格式\n")
		fmt.Fprintf(f, "# 扫描路径: %s\n", s.options.RootPath)
		fmt.Fprintf(f, "# 开始时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(f, "# 每行一个JSON对象: {\"path\":\"...\",\"name\":\"...\",\"size\":123,\"is_dir\":false}\n")
		fmt.Fprintln(f)

		fmt.Printf("📝 输出文件: %s\n", s.options.OutputFile)
	}

	if s.options.ShowErrors {
		fmt.Println("⚠️  错误显示: 已启用")
	}

	if len(s.options.ExcludePaths) > 0 {
		fmt.Println("🚫 排除路径:")
		for _, path := range s.options.ExcludePaths {
			fmt.Printf("   - %s\n", path)
		}
	}

	// 获取磁盘使用情况
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.options.RootPath, &stat); err == nil {
		// 总空间 = 总块数 * 块大小
		totalSize := int64(stat.Blocks) * int64(stat.Bsize)
		// 已使用空间 = (总块数 - 空闲块数) * 块大小
		s.diskUsedSize = (int64(stat.Blocks) - int64(stat.Bfree)) * int64(stat.Bsize)
		freeSize := int64(stat.Bfree) * int64(stat.Bsize)

		usagePercent := float64(s.diskUsedSize) / float64(totalSize) * 100

		fmt.Printf("💿 磁盘总空间: %s\n", formatSize(totalSize))
		fmt.Printf("📊 预估已使用: %s (%.1f%%) | 剩余: %s\n",
			formatSize(s.diskUsedSize), usagePercent, formatSize(freeSize))
	}

	fmt.Printf("开始扫描: %s\n", s.options.RootPath)
	fmt.Printf("工作协程数: %d\n", s.options.WorkerCount)
	if s.options.MinSize > 0 {
		fmt.Printf("最小文件大小: %s\n", formatSize(s.options.MinSize))
	}
	if s.options.MaxSize > 0 {
		fmt.Printf("最大文件大小: %s\n", formatSize(s.options.MaxSize))
	}
	if s.diskUsedSize > 0 {
		fmt.Printf("\n💡 将根据已使用空间显示扫描进度\n")
	} else {
		fmt.Printf("\n💡 提示: 无法获取磁盘使用信息，将显示实时扫描速度和统计信息\n")
	}
	fmt.Print("\n")

	startTime := time.Now()

	// 存储根节点
	s.nodeMap.Store(s.options.RootPath, s.root)

	// 启动工作协程
	for i := 0; i < s.options.WorkerCount; i++ {
		s.workerWg.Add(1)
		go s.worker(i)
	}

	// 启动进度显示
	done := make(chan bool)
	go s.showProgress(done)

	// 添加根目录到队列
	s.taskWg.Add(1)
	s.dirQueue <- s.options.RootPath

	// 等待所有任务完成
	s.taskWg.Wait()
	close(s.dirQueue)

	// 等待所有 worker 退出
	s.workerWg.Wait()
	close(done)

	// 清除进度显示
	if s.diskUsedSize > 0 {
		// 清除进度条和统计行，然后显示100%完成
		fmt.Print("\r\033[K\033[1B\r\033[K")

		// 显示100%完成进度条
		progressBar := "["
		for i := 0; i < 40; i++ {
			progressBar += "█"
		}
		progressBar += "] 100.0%"
		fmt.Println(progressBar)
	} else {
		fmt.Print("\r\033[K")
	}

	fmt.Println("所有扫描任务已完成")

	duration := time.Since(startTime)

	// 打印统计信息
	fmt.Print("\n")
	fmt.Println("════════════════════════════════════════")
	fmt.Println("✅ 扫描完成!")
	fmt.Println("════════════════════════════════════════")
	fmt.Printf("⏱️  用时: %v\n", duration)
	fmt.Printf("📁 目录数: %s\n", formatNumber(s.dirCount.Load()))
	fmt.Printf("📄 文件数: %s\n", formatNumber(s.fileCount.Load()))
	fmt.Printf("💿 磁盘占用: %s\n", formatSize(s.totalDisk.Load()))

	// 计算平均速度
	seconds := duration.Seconds()
	if seconds > 0 {
		fmt.Printf("⚡ 平均速度: %s 个文件/秒, %s/秒\n",
			formatNumber(int64(float64(s.fileCount.Load())/seconds)),
			formatSpeed(float64(s.totalDisk.Load())/seconds))
	}

	if s.symlinkCount.Load() > 0 {
		fmt.Printf("🔗 符号链接: %s (已跳过)\n", formatNumber(s.symlinkCount.Load()))
	}

	if s.hardlinkCount.Load() > 0 {
		fmt.Printf("🔗 硬链接: %s (已去重)\n", formatNumber(s.hardlinkCount.Load()))
	}

	if s.excludedCount.Load() > 0 {
		fmt.Printf("🚫 已排除: %s 个目录/文件\n", formatNumber(s.excludedCount.Load()))
	}

	if s.errorCount.Load() > 0 {
		fmt.Printf("⚠️  错误数: %d\n", s.errorCount.Load())
	}
	fmt.Println("════════════════════════════════════════")

	return nil
}

// showProgress 显示扫描进度
func (s *Scanner) showProgress(done chan bool) {
	ticker := time.NewTicker(500 * time.Millisecond) // 每0.5秒更新一次，更流畅
	defer ticker.Stop()

	startTime := time.Now()
	lastDirs := int64(0)
	lastFiles := int64(0)
	lastDisk := int64(0)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			elapsed := time.Since(startTime).Seconds()
			currentDirs := s.dirCount.Load()
			currentFiles := s.fileCount.Load()
			currentDisk := s.totalDisk.Load()
			errors := s.errorCount.Load()

			// 计算速度
			dirSpeed := float64(currentDirs-lastDirs) / 0.5
			fileSpeed := float64(currentFiles-lastFiles) / 0.5
			diskSpeed := float64(currentDisk-lastDisk) / 0.5

			lastDirs = currentDirs
			lastFiles = currentFiles
			lastDisk = currentDisk

			// 如果启用了进度文件输出，写入JSON格式的进度信息
			if s.options.ProgressFile != "" {
				percentage := 0.0
				if s.diskUsedSize > 0 && currentDisk > 0 {
					percentage = float64(currentDisk) / float64(s.diskUsedSize) * 100
					if percentage > 99.9 {
						percentage = 99.9
					}
				}
				progressJSON := fmt.Sprintf(`{"elapsed":%.1f,"dirCount":%d,"fileCount":%d,"totalDisk":%d,"diskUsedSize":%d,"percentage":%.1f,"dirSpeed":%.0f,"fileSpeed":%.0f,"diskSpeed":%.0f,"errorCount":%d}`,
					elapsed, currentDirs, currentFiles, currentDisk, s.diskUsedSize, percentage, dirSpeed*2, fileSpeed*2, diskSpeed*2, errors)

				// 追加写入进度文件
				if f, err := os.OpenFile(s.options.ProgressFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
					f.WriteString(progressJSON + "\n")
					f.Close()
				}
				continue // 不显示文本进度条
			}

			// 构建进度条
			var progressBar string
			if s.diskUsedSize > 0 && currentDisk > 0 {
				// 计算进度百分比（基于已使用空间）
				percentage := float64(currentDisk) / float64(s.diskUsedSize) * 100
				// 扫描过程中最多显示99.9%，只有完成时才显示100%
				if percentage > 99.9 {
					percentage = 99.9
				}

				// 生成进度条（40个字符宽）
				barWidth := 40
				filledWidth := int(percentage / 100 * float64(barWidth))
				if filledWidth > barWidth {
					filledWidth = barWidth
				}

				progressBar = "["
				for i := 0; i < barWidth; i++ {
					if i < filledWidth {
						progressBar += "█"
					} else {
						progressBar += "░"
					}
				}
				progressBar += fmt.Sprintf("] %.1f%%", percentage)
			}

			// 清除当前行并显示进度
			if progressBar != "" {
				// 显示进度条版本
				fmt.Printf("\r\033[K%s\n\r\033[K⏱️  %.0fs | 📁 %s (%s/s) | 📄 %s (%s/s) | 💿 %s (%s/s)",
					progressBar,
					elapsed,
					formatNumber(currentDirs),
					formatNumber(int64(dirSpeed*2)),
					formatNumber(currentFiles),
					formatNumber(int64(fileSpeed*2)),
					formatSize(currentDisk),
					formatSpeed(diskSpeed*2))
				// 上移一行以覆盖进度条
				fmt.Print("\033[1A")
			} else {
				// 没有磁盘总空间信息，显示原有格式
				fmt.Printf("\r\033[K⏱️  %.0fs | 📁 %s (%s/s) | 📄 %s (%s/s) | 💿 %s (%s/s)",
					elapsed,
					formatNumber(currentDirs),
					formatNumber(int64(dirSpeed*2)),
					formatNumber(currentFiles),
					formatNumber(int64(fileSpeed*2)),
					formatSize(currentDisk),
					formatSpeed(diskSpeed*2))
			}

			if errors > 0 {
				fmt.Printf(" | ⚠️  %d", errors)
			}
		}
	}
}

// GetFileTree 获取文件树
func (s *Scanner) GetFileTree() *FileNode {
	return s.root
}

// PrintTree 打印文件树（限制深度避免输出过多）
func (s *Scanner) PrintTree(maxDepth int) {
	if maxDepth > 0 {
		fmt.Printf("\n文件树结构 (显示深度: %d 层):\n", maxDepth)
	} else {
		fmt.Println("\n文件树结构 (完整):")
	}
	printNode(s.root, "", 0, maxDepth)
}

// printNode 递归打印节点
func printNode(node *FileNode, prefix string, depth, maxDepth int) {
	// maxDepth <= 0 表示不限制深度
	if maxDepth > 0 && depth > maxDepth {
		return
	}

	node.mu.RLock()
	defer node.mu.RUnlock()

	icon := "📄"
	if node.IsDir {
		icon = "📁"
	}

	sizeStr := ""
	if !node.IsDir {
		if node.IsSparse && node.DiskUsage < node.Size {
			// 稀疏文件显示两个大小
			sizeStr = fmt.Sprintf(" (💿 %s / 💾 %s)", formatSize(node.DiskUsage), formatSize(node.Size))
		} else {
			sizeStr = fmt.Sprintf(" (%s)", formatSize(node.Size))
		}
	}

	fmt.Printf("%s%s %s%s\n", prefix, icon, node.Name, sizeStr)

	if node.IsDir && len(node.Children) > 0 {
		childCount := len(node.Children)

		for i := 0; i < childCount; i++ {
			child := node.Children[i]
			isLast := i == childCount-1
			var newPrefix string
			if isLast {
				newPrefix = prefix + "└── "
			} else {
				newPrefix = prefix + "├── "
			}
			printNode(child, newPrefix, depth+1, maxDepth)
		}
	}
}

// formatSize 格式化文件大小
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	switch {
	case size >= TB:
		return fmt.Sprintf("%.2f TB", float64(size)/TB)
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// formatSpeed 格式化速度
func formatSpeed(bytesPerSec float64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytesPerSec >= GB:
		return fmt.Sprintf("%.1f GB", bytesPerSec/GB)
	case bytesPerSec >= MB:
		return fmt.Sprintf("%.1f MB", bytesPerSec/MB)
	case bytesPerSec >= KB:
		return fmt.Sprintf("%.1f KB", bytesPerSec/KB)
	default:
		return fmt.Sprintf("%.0f B", bytesPerSec)
	}
}

// formatNumber 格式化数字（添加千位分隔符）
func formatNumber(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprintf("%d,%03d,%03d", n/1000000, (n/1000)%1000, n%1000)
}

// parseSize 解析人性化的文件大小字符串 (支持 K/M/G/T 后缀)
// 例如: "100M" -> 104857600, "1.5G" -> 1610612736
func parseSize(sizeStr string) (int64, error) {
	if sizeStr == "" || sizeStr == "0" {
		return 0, nil
	}

	// 去除空格
	sizeStr = strings.TrimSpace(sizeStr)

	// 转换为大写以支持大小写
	upper := strings.ToUpper(sizeStr)

	// 定义单位
	multipliers := map[string]int64{
		"K": 1024,
		"M": 1024 * 1024,
		"G": 1024 * 1024 * 1024,
		"T": 1024 * 1024 * 1024 * 1024,
	}

	// 检查是否有单位后缀
	for suffix, multiplier := range multipliers {
		if strings.HasSuffix(upper, suffix) {
			// 去除后缀，解析数字
			numStr := strings.TrimSuffix(upper, suffix)
			numStr = strings.TrimSpace(numStr)

			// 解析数字（支持小数）
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("无效的数字: %s", numStr)
			}

			return int64(num * float64(multiplier)), nil
		}
	}

	// 没有单位后缀，直接解析为字节数
	num, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的大小格式: %s (支持格式: 100M, 1.5G, 1024)", sizeStr)
	}

	return num, nil
}

func main() {
	// 命令行参数
	rootPath := flag.String("path", ".", "扫描的根目录路径")
	minSizeStr := flag.String("min", "0", "最小文件大小 (支持: 100M, 1.5G, 1024 等)")
	maxSizeStr := flag.String("max", "0", "最大文件大小 (支持: 100M, 1.5G, 1024 等), 0表示不限制")
	workers := flag.Int("workers", runtime.NumCPU()*4, "并发工作协程数")
	showTree := flag.Bool("tree", false, "显示文件树结构")
	treeDepth := flag.Int("depth", 0, "文件树显示深度，0表示不限制（默认不限制）")
	outputFile := flag.String("output", "", "输出文件路径（JSON Lines格式），实时写入防止数据丢失")
	showErrors := flag.Bool("errors", false, "显示错误详情")
	excludePaths := flag.String("exclude", "", "要排除的路径，多个路径用逗号分隔（例如: /Volumes/ExtDisk,/private/tmp）")
	includeExts := flag.String("include-ext", "", "只包含的文件扩展名，多个用逗号分隔（例如: .txt,.log,.md）")
	excludeExts := flag.String("exclude-ext", "", "要排除的文件扩展名，多个用逗号分隔（例如: .tmp,.cache）")
	namePattern := flag.String("name", "", "文件名正则表达式过滤（例如: ^test.*\\.go$）")
	progressFile := flag.String("progress-file", "", "输出JSON格式的进度信息到指定文件（供APP调用）")

	flag.Parse()

	// 解析文件大小参数
	minSize, err := parseSize(*minSizeStr)
	if err != nil {
		log.Fatalf("最小文件大小参数错误: %v", err)
	}

	maxSize, err := parseSize(*maxSizeStr)
	if err != nil {
		log.Fatalf("最大文件大小参数错误: %v", err)
	}

	// 验证路径
	absPath, err := filepath.Abs(*rootPath)
	if err != nil {
		log.Fatalf("路径错误: %v", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		log.Fatalf("无法访问路径 %s: %v", absPath, err)
	}

	if !info.IsDir() {
		log.Fatalf("%s 不是一个目录", absPath)
	}

	// 解析排除路径
	var excludeList []string
	if *excludePaths != "" {
		paths := strings.Split(*excludePaths, ",")
		for _, p := range paths {
			p = strings.TrimSpace(p)
			if p != "" {
				// 转换为绝对路径
				absExclude, err := filepath.Abs(p)
				if err != nil {
					log.Printf("警告: 无法解析排除路径 %s: %v", p, err)
					continue
				}
				excludeList = append(excludeList, absExclude)

				// 同时获取真实路径（解析符号链接）
				// 这样可以同时排除 /Volumes/XXX 和 /System/Volumes/Data/Volumes/XXX
				realPath, err := filepath.EvalSymlinks(absExclude)
				if err == nil && realPath != absExclude {
					excludeList = append(excludeList, realPath)
					log.Printf("排除路径: %s (实际: %s)", absExclude, realPath)
				}
			}
		}
	}

	// 解析扩展名列表
	var includeExtList []string
	if *includeExts != "" {
		exts := strings.Split(*includeExts, ",")
		for _, ext := range exts {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				// 确保扩展名以 . 开头
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				includeExtList = append(includeExtList, ext)
			}
		}
	}

	var excludeExtList []string
	if *excludeExts != "" {
		exts := strings.Split(*excludeExts, ",")
		for _, ext := range exts {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				// 确保扩展名以 . 开头
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				excludeExtList = append(excludeExtList, ext)
			}
		}
	}

	// 创建扫描器
	scanner := NewScanner(ScanOptions{
		RootPath:     absPath,
		MinSize:      minSize,
		MaxSize:      maxSize,
		WorkerCount:  *workers,
		OutputFile:   *outputFile,
		ShowErrors:   *showErrors,
		ExcludePaths: excludeList,
		IncludeExts:  includeExtList,
		ExcludeExts:  excludeExtList,
		NamePattern:  *namePattern,
		ProgressFile: *progressFile,
	})

	// 执行扫描
	if err := scanner.Scan(); err != nil {
		log.Fatalf("扫描失败: %v", err)
	}

	// 显示文件树
	if *showTree {
		scanner.PrintTree(*treeDepth)
	}
}
