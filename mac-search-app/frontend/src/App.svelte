<script>
  import { Search, GetIndexStats, OpenInFinder, OpenFile, CopyToClipboard, SelectFolder, RebuildIndex, StopIndexing, SetExcludePaths, GetExcludePaths, SetSudoPassword, HasSudoPassword, GetIndexedPaths, DeleteIndexedPath, ShowWindow, HideWindow, GetFileIcon } from '../wailsjs/go/main/App.js'
  import { onMount } from 'svelte'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'

  let searchQuery = ''
  let searchResults = []
  let stats = { fileCount: 0, dirCount: 0, total: 0 }
  let selectedIndex = -1
  let useRegex = false
  let isIndexing = false  // 初始为 false，启动时检查是否有缓存索引
  let currentScanningFile = ''
  let currentIndexingPath = ''
  let lastIndexedPath = ''  // 保存最后索引的路径
  let indexingElapsed = 0  // 索引耗时（秒）
  let scanSpeed = 0
  let lastUpdateTime = Date.now()
  let lastTotal = 0
  // 进度条相关（参考 main.go）
  let totalDisk = 0  // 已扫描的磁盘占用
  let diskUsedSize = 0  // 磁盘总使用空间（用于计算进度百分比）
  let lastDirs = 0
  let lastFiles = 0
  let lastDisk = 0
  let dirSpeed = 0  // 目录扫描速度
  let fileSpeed = 0  // 文件扫描速度
  let diskSpeed = 0  // 磁盘扫描速度
  let showMenu = false
  let menuPosition = { x: 0, y: 0 }
  let menuTarget = null

  // 搜索框引用
  let searchInputElement = null

  // 文件图标缓存
  let iconCache = {}

  // 搜索防抖
  let searchDebounceTimer = null
  let lastSearchedQuery = ''  // 记录最后搜索的query

  // 获取文件图标
  async function getIcon(path, isDir) {
    // 目录使用固定图标
    if (isDir) {
      return null // 使用emoji
    }

    // 检查缓存
    if (iconCache[path]) {
      return iconCache[path]
    }

    // 获取图标
    try {
      const icon = await GetFileIcon(path)
      if (icon) {
        iconCache[path] = icon
        return icon
      }
    } catch (err) {
      console.error('获取图标失败:', err)
    }

    return null
  }
  
  // 设置相关
  let showSettings = false
  let excludePaths = []
  let newExcludePath = ''
  let indexedPaths = []  // 已索引的路径列表

  // 删除确认对话框
  let showDeleteConfirm = false
  let deleteTargetPath = ''

  // sudo 密码输入相关
  let showSudoPasswordDialog = false
  let sudoPassword = ''
  let pendingRebuildPath = null

  // 分页相关
  let currentOffset = 0
  let isLoadingMore = false
  let hasMore = true
  let resultsContainer = null
  let totalCount = 0
  let isSearching = false  // 搜索中状态

  // IME 输入法相关
  let isComposing = false  // 是否正在输入法组合输入中

  const SEARCH_DEBOUNCE_DELAY = 500  // 500ms 延迟

  // 列宽调整 - 时间格式固定，不需要太宽，留更多空间给路径
  let columnWidths = { name: 25, path: 50, size: 10, modTime: 15 }
  let resizing = null
  let startX = 0
  let startWidth = 0
  let startAdjacentWidth = 0 // 相邻列的初始宽度

  function startResize(e, column) {
    resizing = column
    startX = e.clientX
    startWidth = columnWidths[column]

    // 记录相邻列的初始宽度
    if (column === 'name') {
      startAdjacentWidth = columnWidths.path
    } else if (column === 'path') {
      startAdjacentWidth = columnWidths.size
    } else if (column === 'size') {
      startAdjacentWidth = columnWidths.modTime
    }

    e.preventDefault()
  }

  function handleMouseMove(e) {
    if (!resizing) return

    const diffPercent = ((e.clientX - startX) / window.innerWidth) * 100

    if (resizing === 'name') {
      // 拖动"名称"右边：名称+diff，路径-diff
      const newNameWidth = startWidth + diffPercent
      const newPathWidth = startAdjacentWidth - diffPercent

      // 限制范围，防止列宽过小
      if (newNameWidth >= 10 && newPathWidth >= 10) {
        columnWidths.name = newNameWidth
        columnWidths.path = newPathWidth
      }
    } else if (resizing === 'path') {
      // 拖动"路径"右边：路径+diff，大小-diff
      const newPathWidth = startWidth + diffPercent
      const newSizeWidth = startAdjacentWidth - diffPercent

      // 限制范围，防止列宽过小
      if (newPathWidth >= 10 && newSizeWidth >= 5) {
        columnWidths.path = newPathWidth
        columnWidths.size = newSizeWidth
      }
    } else if (resizing === 'size') {
      // 拖动"大小"右边：大小+diff，修改时间-diff
      const newSizeWidth = startWidth + diffPercent
      const newModTimeWidth = startAdjacentWidth - diffPercent

      // 限制范围，防止列宽过小
      if (newSizeWidth >= 5 && newModTimeWidth >= 10) {
        columnWidths.size = newSizeWidth
        columnWidths.modTime = newModTimeWidth
      }
    }
  }

  function stopResize() {
    resizing = null
  }

  // 格式化文件大小（用于进度条，参考 main.go）
  function formatSizeForProgress(bytes) {
    const KB = 1024
    const MB = KB * 1024
    const GB = MB * 1024
    const TB = GB * 1024

    if (bytes >= TB) return `${(bytes / TB).toFixed(2)} TB`
    if (bytes >= GB) return `${(bytes / GB).toFixed(2)} GB`
    if (bytes >= MB) return `${(bytes / MB).toFixed(2)} MB`
    if (bytes >= KB) return `${(bytes / KB).toFixed(2)} KB`
    return `${bytes} B`
  }

  // 格式化速度（参考 main.go）
  function formatSpeed(bytesPerSec) {
    const KB = 1024
    const MB = KB * 1024
    const GB = MB * 1024

    if (bytesPerSec >= GB) return `${(bytesPerSec / GB).toFixed(1)} GB`
    if (bytesPerSec >= MB) return `${(bytesPerSec / MB).toFixed(1)} MB`
    if (bytesPerSec >= KB) return `${(bytesPerSec / KB).toFixed(1)} KB`
    return `${Math.round(bytesPerSec)} B`
  }

  // 加载统计信息
  async function loadStats() {
    try {
      const result = await GetIndexStats()
      console.log('loadStats 返回:', result)
      if (result) {
        stats = {
          fileCount: result.fileCount || 0,
          dirCount: result.dirCount || 0,
          total: result.total || 0,
          indexPath: result.indexPath || ''
        }
        if (stats.total > 0) {
          // 有缓存索引，设置为非索引状态
          isIndexing = false
          // 从数据库加载索引路径
          if (stats.indexPath) {
            lastIndexedPath = stats.indexPath
          }
          console.log('检测到缓存索引:', stats)
        } else {
          // 没有索引，也设置为非索引状态（不会显示"正在构建索引"）
          isIndexing = false
          console.log('没有缓存索引')
        }
      } else {
        isIndexing = false
        console.log('GetIndexStats 返回 null')
      }
    } catch (err) {
      console.error('获取统计失败:', err)
      // 出错时也设置为非索引状态
      isIndexing = false
    }
  }

  // 搜索文件（初次搜索）
  async function performSearch() {
    const query = searchQuery.trim()

    if (!query) {
      searchResults = []
      currentOffset = 0
      hasMore = true
      totalCount = 0
      isSearching = false
      lastSearchedQuery = ''
      return
    }

    try {
      isSearching = true
      currentOffset = 0
      const results = await Search(query, useRegex, 0)
      searchResults = results || []
      selectedIndex = -1
      hasMore = results && results.length >= 500
      totalCount = searchResults.length

      lastSearchedQuery = query  // 记录已搜索的query

      // 搜索完成后，检查是否有新的输入
      if (searchQuery.trim() !== query) {
        console.log('搜索期间输入已变更，重新搜索')
        performSearch()  // 重新搜索
      }
    } catch (err) {
      console.error('搜索失败:', err)
      searchResults = []
      hasMore = false
      totalCount = 0
    } finally {
      isSearching = false
    }
  }

  // 处理输入变化
  function handleSearchInput() {
    // 清除之前的定时器
    if (searchDebounceTimer) {
      clearTimeout(searchDebounceTimer)
    }

    // 如果正在输入法组合输入中，不设置新的定时器
    if (!isComposing) {
      // 设置新的定时器
      searchDebounceTimer = setTimeout(() => {
        performSearch()
      }, SEARCH_DEBOUNCE_DELAY)
    }
  }

  // 加载更多结果
  async function loadMore() {
    if (isLoadingMore || !hasMore || !searchQuery.trim()) {
      return
    }

    isLoadingMore = true
    try {
      currentOffset += 500
      const results = await Search(searchQuery, useRegex, currentOffset)
      if (results && results.length > 0) {
        searchResults = [...searchResults, ...results]
        hasMore = results.length >= 500
        totalCount = searchResults.length
      } else {
        hasMore = false
      }
    } catch (err) {
      console.error('加载更多失败:', err)
      hasMore = false
    } finally {
      isLoadingMore = false
    }
  }

  // IME 组合输入开始
  function handleCompositionStart() {
    isComposing = true
  }

  // IME 组合输入结束
  function handleCompositionEnd() {
    isComposing = false
    // 组合输入结束后，立即触发搜索（确保搜索最终输入的完整内容）
    // 清除可能存在的定时器
    if (searchDebounceTimer) {
      clearTimeout(searchDebounceTimer)
    }
    // 设置新的定时器执行搜索
    searchDebounceTimer = setTimeout(() => {
      performSearch()
    }, SEARCH_DEBOUNCE_DELAY)
  }

  // 清空搜索框
  function clearSearch() {
    searchQuery = ''
    selectedIndex = -1
  }

  // 监听滚动事件
  function handleScroll(e) {
    const container = e.target
    const scrollBottom = container.scrollHeight - container.scrollTop - container.clientHeight

    // 距离底部100px时加载更多
    if (scrollBottom < 100 && hasMore && !isLoadingMore) {
      loadMore()
    }
  }

  // 监听正则模式变化，触发重新搜索
  $: if (useRegex !== undefined) {
    useRegex;  // 监听useRegex变化
    handleSearchInput()  // 触发搜索
  }

  // 打开文件
  async function openFile(path) {
    try {
      await OpenFile(path)
    } catch (err) {
      console.error('打开文件失败:', err)
    }
  }

  // 在 Finder 中显示
  async function showInFinder(path) {
    try {
      await OpenInFinder(path)
    } catch (err) {
      console.error('打开 Finder 失败:', err)
    }
  }

  // 复制路径
  async function copyPath(path) {
    try {
      await CopyToClipboard(path)
      console.log('已复制:', path)
    } catch (err) {
      console.error('复制失败:', err)
    }
  }

  // 选择文件夹并重建索引
  async function selectAndRebuild() {
    // 如果正在索引中，不允许再次选择
    if (isIndexing) {
      console.log('已有索引任务在运行，忽略本次请求')
      return
    }

    console.log('selectAndRebuild 被调用')

    try {
      console.log('调用 SelectFolder()...')
      const folder = await SelectFolder()
      console.log('SelectFolder() 返回:', folder)

      if (folder) {
        // 检查是否已设置密码
        console.log('检查sudo密码...')
        const hasPassword = await HasSudoPassword()
        console.log('HasSudoPassword() 返回:', hasPassword)

        if (hasPassword) {
          // 如果已设置密码，直接开始构建索引
          // 不在这里设置 isIndexing，等待后端的 indexing-start 事件
          stats = { fileCount: 0, dirCount: 0, total: 0 }
          indexingElapsed = 0
          console.log('调用 RebuildIndex:', folder)
          await RebuildIndex(folder)
          console.log('RebuildIndex 调用完成')
          // RebuildIndex 调用后，后端会异步发送 indexing-start 事件，前端监听到后设置 isIndexing = true
        } else {
          // 如果未设置密码，弹出密码输入对话框
          console.log('未设置密码，显示密码输入对话框')
          pendingRebuildPath = folder
          showSudoPasswordDialog = true
          sudoPassword = ''
        }
      } else {
        console.log('用户取消了文件夹选择')
      }
    } catch (err) {
      console.error('选择文件夹失败:', err)
      alert('选择文件夹失败: ' + (err.message || err))
    }
  }
  
  async function confirmSudoPassword() {
    if (!sudoPassword) {
      alert('请输入密码')
      return
    }

    try {
      // 设置密码
      await SetSudoPassword(sudoPassword)
      // 关闭对话框
      showSudoPasswordDialog = false
      const folder = pendingRebuildPath
      pendingRebuildPath = null
      sudoPassword = ''

      // 开始构建索引
      if (folder) {
        // 不在这里设置 isIndexing，等待后端的 indexing-start 事件
        stats = { fileCount: 0, dirCount: 0, total: 0 }
        indexingElapsed = 0
        await RebuildIndex(folder)
        // RebuildIndex 调用后，后端会异步发送 indexing-start 事件，前端监听到后设置 isIndexing = true
      }
    } catch (err) {
      console.error('设置密码失败:', err)
      alert('设置密码失败: ' + err)
    }
  }
  
  function cancelSudoPassword() {
    showSudoPasswordDialog = false
    pendingRebuildPath = null
    sudoPassword = ''
  }

  // 停止索引
  async function stopIndexing() {
    try {
      await StopIndexing()
      isIndexing = false
    } catch (err) {
      console.error('停止索引失败:', err)
    }
  }

  // 刷新索引路径列表
  async function refreshIndexedPaths() {
    try {
      const indexed = await GetIndexedPaths()
      indexedPaths = indexed || []
      console.log('刷新索引列表:', indexedPaths)
    } catch (err) {
      console.error('刷新索引列表失败:', err)
    }
  }

  // 打开设置
  async function openSettings() {
    // 先显示弹窗
    showSettings = true

    // 然后加载数据
    try {
      // 加载已保存的排除路径
      const savedPaths = await GetExcludePaths()
      excludePaths = savedPaths || []
      console.log('加载已保存的排除路径:', excludePaths)

      // 加载已索引的路径列表
      try {
        const indexed = await GetIndexedPaths()
        indexedPaths = indexed || []
        console.log('加载已索引的路径:', indexedPaths)
      } catch (indexErr) {
        console.error('加载已索引路径失败:', indexErr)
        indexedPaths = []
      }
    } catch (err) {
      console.error('加载排除路径失败:', err)
      excludePaths = []
      indexedPaths = []
    }
  }

  // 删除索引路径 - 显示确认对话框
  function deleteIndexedPath(path) {
    deleteTargetPath = path
    showDeleteConfirm = true
  }

  // 确认删除
  async function confirmDelete() {
    showDeleteConfirm = false
    const path = deleteTargetPath

    try {
      // 先从前端列表中移除（立即反馈）
      indexedPaths = indexedPaths.filter(item => item.path !== path)

      // 后台异步删除
      await DeleteIndexedPath(path)

      // 刷新统计
      loadStats()
    } catch (err) {
      console.error('删除索引失败:', err)
      // 删除失败，重新加载列表
      refreshIndexedPaths()
    }
  }

  // 取消删除
  function cancelDelete() {
    showDeleteConfirm = false
    deleteTargetPath = ''
  }

  // 关闭设置
  function closeSettings() {
    showSettings = false
  }

  // 添加排除路径（自动保存）
  async function addExcludePath() {
    if (newExcludePath.trim() && !excludePaths.includes(newExcludePath.trim())) {
      excludePaths = [...excludePaths, newExcludePath.trim()]
      newExcludePath = ''

      // 自动保存到后端
      try {
        await SetExcludePaths(excludePaths)
        console.log('排除路径已保存:', excludePaths)
      } catch (err) {
        console.error('保存排除路径失败:', err)
        alert('保存排除路径失败: ' + (err.message || err))
      }
    }
  }

  // 删除排除路径（自动保存）
  async function removeExcludePath(index) {
    excludePaths = excludePaths.filter((_, i) => i !== index)

    // 自动保存到后端
    try {
      await SetExcludePaths(excludePaths)
      console.log('排除路径已保存:', excludePaths)
    } catch (err) {
      console.error('保存排除路径失败:', err)
      alert('保存排除路径失败: ' + (err.message || err))
    }
  }


  // 显示右键菜单
  function handleContextMenu(e, result) {
    e.preventDefault()
    e.stopPropagation()

    // 清除任何文本选择
    if (window.getSelection) {
      window.getSelection().removeAllRanges()
    }

    menuTarget = result
    menuPosition = { x: e.clientX, y: e.clientY }
    showMenu = true
  }

  // 隐藏菜单
  function hideMenu() {
    showMenu = false
    menuTarget = null
  }

  // 处理键盘事件
  function handleKeydown(e) {
    // 检查是否在输入框或可编辑元素中
    const target = e.target
    const isInInput = target.tagName === 'INPUT' ||
                      target.tagName === 'TEXTAREA' ||
                      target.isContentEditable

    // Cmd+W 隐藏窗口（macOS）
    if ((e.metaKey || e.ctrlKey) && e.key === 'w') {
      e.preventDefault()
      HideWindow()
      return
    }

    // 如果在输入框中按 Cmd+C/Ctrl+C，不拦截，允许浏览器默认复制行为
    if (isInInput && (e.metaKey || e.ctrlKey) && e.key === 'c') {
      return
    }

    // 如果在输入框中，不处理空格键和方向键（让用户正常输入）
    if (isInInput && (e.key === ' ' || e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      return
    }

    // ESC 关闭菜单
    if (e.key === 'Escape') {
      hideMenu()
      return
    }

    if (searchResults.length === 0) return

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      selectedIndex = Math.min(selectedIndex + 1, searchResults.length - 1)
      scrollToSelected()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      selectedIndex = Math.max(selectedIndex - 1, 0)
      scrollToSelected()
    } else if (e.key === 'Enter' && selectedIndex >= 0) {
      e.preventDefault()
      openFile(searchResults[selectedIndex].path)
    } else if ((e.metaKey || e.ctrlKey) && e.key === 'c' && selectedIndex >= 0) {
      // Cmd+C 复制路径（仅在不是输入框时）
      e.preventDefault()
      copyPath(searchResults[selectedIndex].path)
    }
  }

  // 滚动到选中项
  function scrollToSelected() {
    const element = document.querySelector('.result-item.selected')
    if (element) {
      element.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }

  // 格式化文件大小
  function formatSize(size) {
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let i = 0
    while (size >= 1024 && i < units.length - 1) {
      size /= 1024
      i++
    }
    return `${size.toFixed(1)} ${units[i]}`
  }

  // 格式化修改时间
  function formatModTime(timestamp) {
    if (!timestamp) return ''
    const date = new Date(timestamp * 1000) // Unix timestamp转为毫秒
    return date.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }) + ' ' +
           date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }

  // 组件挂载时加载统计
  onMount(() => {
    loadStats()

    // 监听缓存索引事件（启动时如果有缓存索引会触发）
    EventsOn('index-cached', (data) => {
      console.log('收到 index-cached 事件:', data)
      if (data) {
        stats = {
          fileCount: data.fileCount || 0,
          dirCount: data.dirCount || 0,
          total: data.total || 0,
          indexPath: data.indexPath || ''
        }
        if (stats.total > 0) {
          isIndexing = false
          if (stats.indexPath) {
            lastIndexedPath = stats.indexPath
          }
          console.log('更新缓存索引信息:', stats)
        }
      }
    })

    // 监听索引开始事件
    EventsOn('indexing-start', (data) => {
      isIndexing = true
      stats = { fileCount: 0, dirCount: 0, total: 0 }
      currentScanningFile = ''
      currentIndexingPath = data.path || ''
      indexingElapsed = 0  // 重置索引耗时
      scanSpeed = 0
      lastTotal = 0
      lastUpdateTime = Date.now()
      // 重置进度条相关变量
      totalDisk = 0
      diskUsedSize = 0
      lastDirs = 0
      lastFiles = 0
      lastDisk = 0
      dirSpeed = 0
      fileSpeed = 0
      diskSpeed = 0
      // 清空搜索结果表格，还原到初始状态
      searchResults = []
      totalCount = 0
      query = ''
    })

    // 监听索引进度事件（参考 main.go 的进度显示逻辑）
    EventsOn('indexing-progress', (data) => {
      stats = {
        fileCount: data.fileCount || 0,
        dirCount: data.dirCount || 0,
        total: data.total || 0
      }
      totalDisk = data.totalDisk || 0
      diskUsedSize = data.diskUsedSize || 0
      // 确保 elapsed 是有效的数字，且不会太大（防止显示异常值）
      const elapsed = data.elapsed
      if (elapsed !== undefined && elapsed !== null && !isNaN(elapsed) && elapsed >= 0 && elapsed < 86400) {
        indexingElapsed = elapsed  // 86400秒 = 24小时，防止异常值
      }

      // 计算速度（参考 main.go，每0.5秒更新一次）
      const now = Date.now()
      const timeDiff = (now - lastUpdateTime) / 1000 // 秒
      
      if (timeDiff >= 0.5) { // 至少0.5秒才更新速度
        const countDiff = data.total - lastTotal
        scanSpeed = Math.round(countDiff / timeDiff)
        
        // 计算目录、文件、磁盘速度（参考 main.go）
        dirSpeed = Math.round((data.dirCount - lastDirs) / timeDiff)
        fileSpeed = Math.round((data.fileCount - lastFiles) / timeDiff)
        diskSpeed = Math.round((totalDisk - lastDisk) / timeDiff)

        lastDirs = data.dirCount
        lastFiles = data.fileCount
        lastDisk = totalDisk
        lastUpdateTime = now
        lastTotal = data.total
      }
    })

    // 监听当前扫描的文件
    EventsOn('scanning-file', (filePath) => {
      currentScanningFile = filePath
    })

    // 监听索引完成事件
    EventsOn('indexing-complete', (data) => {
      isIndexing = false
      stats = { ...data, total: data.fileCount + data.dirCount }
      lastIndexedPath = currentIndexingPath  // 保存索引路径
      indexingElapsed = data.elapsed || 0  // 保存耗时
      currentScanningFile = ''
      currentIndexingPath = ''
      // 重置所有速度和进度相关变量
      scanSpeed = 0
      dirSpeed = 0
      fileSpeed = 0
      diskSpeed = 0
      totalDisk = 0
      diskUsedSize = 0
      lastDirs = 0
      lastFiles = 0
      lastDisk = 0
      lastTotal = 0
      lastUpdateTime = Date.now()

      // 如果设置对话框是打开的，刷新索引列表
      if (showSettings) {
        refreshIndexedPaths()
      }
    })

    // 监听索引停止事件
    EventsOn('indexing-stopped', () => {
      isIndexing = false
      lastIndexedPath = currentIndexingPath  // 保存索引路径
      indexingElapsed = 0
      currentScanningFile = ''
      currentIndexingPath = ''
      // 重置所有速度和进度相关变量
      scanSpeed = 0
      dirSpeed = 0
      fileSpeed = 0
      diskSpeed = 0
      totalDisk = 0
      diskUsedSize = 0
      lastDirs = 0
      lastFiles = 0
      lastDisk = 0
      lastTotal = 0
      lastUpdateTime = Date.now()

      // 如果设置对话框是打开的，刷新索引列表
      if (showSettings) {
        refreshIndexedPaths()
      }
    })

    // 监听目录大小计算完成事件
    EventsOn('disk-size-calculated', (data) => {
      console.log('目录大小已计算:', data.diskUsedSize)
      diskUsedSize = data.diskUsedSize
    })

    // 监听窗口显示事件（cmd+w 隐藏后从程序坞打开时，后端发 window-shown，此处聚焦搜索框）
    EventsOn('window-shown', () => {
      if (searchInputElement) {
        setTimeout(() => searchInputElement.focus(), 100)
      }
    })


    // 每5秒刷新一次统计（检查索引是否完成）
    const interval = setInterval(() => {
      if (!isIndexing) {
        loadStats()
      }
    }, 5000)

    return () => clearInterval(interval)
  })
</script>

<svelte:window on:keydown={handleKeydown} on:click={hideMenu} on:mousemove={handleMouseMove} on:mouseup={stopResize} />

<main on:click={hideMenu}>
  <div class="header">
    <div class="search-box">
      <div class="search-input-wrapper">
        <input
          type="text"
          class="search-input"
          placeholder="搜索文件..."
          bind:value={searchQuery}
          bind:this={searchInputElement}
          on:input={handleSearchInput}
          on:compositionstart={handleCompositionStart}
          on:compositionend={handleCompositionEnd}
          autofocus
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
        {#if searchQuery}
          <button class="clear-btn" on:click={clearSearch} title="清空搜索">
            ✕
          </button>
        {/if}
      </div>
      <label class="regex-label" title="支持正则表达式搜索（高级用户）">
        <input type="checkbox" bind:checked={useRegex} />
        <span>正则</span>
      </label>
      {#if isIndexing}
        <button class="stop-btn" on:click={stopIndexing} title="停止索引">
          ⏹ 停止索引
        </button>
      {:else}
        <button class="rebuild-btn" on:click={selectAndRebuild} title="重新选择索引路径">
          🔄 重建索引
        </button>
      {/if}
      <button class="rebuild-btn" on:click={openSettings} title="设置">
        ⚙️ 设置
      </button>
    </div>
    <div class="stats">
      {#if isIndexing}
        <div class="indexing-info">
          <!-- 进度条（参考 main.go） -->
          {#if diskUsedSize > 0 && totalDisk > 0}
            {@const percentage = Math.min(99.9, (totalDisk / diskUsedSize) * 100)}
            {@const barWidth = 40}
            {@const filledWidth = Math.min(barWidth, Math.floor(percentage / 100 * barWidth))}
            <div class="progress-bar">
              <div class="progress-bar-container">
                <div class="progress-bar-fill" style="width: {filledWidth * 100 / barWidth}%"></div>
              </div>
              <span class="progress-percentage">{percentage.toFixed(1)}%</span>
            </div>
          {/if}
          
          <!-- 详细信息（参考 main.go） -->
          <div class="progress-details">
            <span class="indexing">🔄 正在构建索引...</span>
            {#if currentIndexingPath}
              <span class="indexing-path">({currentIndexingPath})</span>
            {/if}
            <span class="scan-stats">
              ⏱️ {Math.floor(indexingElapsed)}s | 
              📁 {stats.dirCount.toLocaleString()}
              {#if dirSpeed > 0}
                <span class="speed">({dirSpeed.toLocaleString()}/s)</span>
              {/if}
              | 📄 {stats.fileCount.toLocaleString()}
              {#if fileSpeed > 0}
                <span class="speed">({fileSpeed.toLocaleString()}/s)</span>
              {/if}
              {#if totalDisk > 0}
                | 💿 {formatSizeForProgress(totalDisk)}
                {#if diskSpeed > 0}
                  <span class="speed">({formatSpeed(diskSpeed)}/s)</span>
                {/if}
              {/if}
            </span>
          </div>
        </div>
        {#if currentScanningFile}
          <div class="current-file">📄 {currentScanningFile}</div>
        {/if}
      {:else}
        <span>
          {#if lastIndexedPath}
            <span class="indexed-path-label">索引：{lastIndexedPath}</span>
            <span class="separator">·</span>
          {/if}
          {stats.fileCount.toLocaleString()} 文件 | {stats.dirCount.toLocaleString()} 目录
          {#if indexingElapsed > 0}
            <span class="elapsed-time">· 耗时 {indexingElapsed.toFixed(2)}秒</span>
          {/if}
        </span>
      {/if}

      <!-- 搜索结果计数 -->
      {#if searchQuery && totalCount > 0}
        <span class="result-count">
          · 找到 {totalCount.toLocaleString()} 个结果{hasMore ? '+' : ''}
        </span>
      {/if}
    </div>
  </div>

  <div class="results-container" on:scroll={handleScroll} bind:this={resultsContainer}>
    {#if searchResults.length > 0}
      <table class="results-table" on:mouseleave={() => selectedIndex = -1}>
        <thead>
          <tr>
            <th class="col-name" style="width: {columnWidths.name}%">
              名称
              <div class="resize-handle" on:mousedown={(e) => startResize(e, 'name')}></div>
            </th>
            <th class="col-path" style="width: {columnWidths.path}%">
              路径
              <div class="resize-handle" on:mousedown={(e) => startResize(e, 'path')}></div>
            </th>
            <th class="col-size" style="width: {columnWidths.size}%">
              大小
              <div class="resize-handle" on:mousedown={(e) => startResize(e, 'size')}></div>
            </th>
            <th class="col-modtime" style="width: {columnWidths.modTime}%">修改时间</th>
          </tr>
        </thead>
        <tbody>
          {#each searchResults as result, i}
            <tr
              class="result-item {i === selectedIndex ? 'selected' : ''}"
              on:click={() => selectedIndex = i}
              on:dblclick={() => openFile(result.path)}
              on:contextmenu|preventDefault={(e) => handleContextMenu(e, result)}
            >
              <td class="col-name" style="width: {columnWidths.name}%">
                <div class="file-name-cell">
                  {#if result.is_dir}
                    <span class="file-icon">📁</span>
                    <span>{result.name}</span>
                  {:else}
                    {#await getIcon(result.path, false)}
                      <span class="file-icon">📄</span>
                    {:then icon}
                      {#if icon}
                        <img src={icon} alt="" class="file-icon-img" />
                      {:else}
                        <span class="file-icon">📄</span>
                      {/if}
                    {/await}
                    <span>{result.name}</span>
                  {/if}
                </div>
              </td>
              <td class="col-path" style="width: {columnWidths.path}%">
                {result.path}
              </td>
              <td class="col-size" style="width: {columnWidths.size}%">{result.is_dir ? '' : formatSize(result.size)}</td>
              <td class="col-modtime" style="width: {columnWidths.modTime}%">{formatModTime(result.mod_time)}</td>
            </tr>
          {/each}
        </tbody>
      </table>

      <!-- 加载更多指示器 -->
      {#if isLoadingMore}
        <div class="loading-more">加载中...</div>
      {/if}
      {#if !hasMore && searchResults.length > 0}
        <div class="no-more">已显示全部结果</div>
      {/if}
    {:else if searchQuery}
      <div class="no-results">
        {#if isSearching}
          搜索中...
        {:else}
          未找到匹配的文件
        {/if}
      </div>
    {:else}
      <div class="welcome">
        <h2>Mac 文件搜索</h2>
        <p>输入文件名开始搜索</p>
        <ul>
          <li>支持通配符：*.txt</li>
          <li>实时搜索，毫秒级响应</li>
          <li>单击打开文件</li>
          <li>右键在 Finder 中显示</li>
        </ul>
      </div>
    {/if}
  </div>

  <!-- 右键菜单 -->
  {#if showMenu && menuTarget}
    <div
      class="context-menu"
      style="left: {menuPosition.x}px; top: {menuPosition.y}px;"
      on:click|stopPropagation
    >
      <div class="menu-item" on:click={() => { openFile(menuTarget.path); hideMenu(); }}>
        打开文件
      </div>
      <div class="menu-item" on:click={() => { showInFinder(menuTarget.path); hideMenu(); }}>
        在 Finder 中显示
      </div>
      <div class="menu-item" on:click={() => { copyPath(menuTarget.path); hideMenu(); }}>
        复制路径
      </div>
    </div>
  {/if}

  <!-- 设置对话框 -->
  {#if showSettings}
    <div class="settings-overlay" on:click={closeSettings}>
      <div class="settings-dialog" on:click|stopPropagation>
        <div class="settings-header">
          <h2>设置</h2>
          <button class="close-btn" on:click={closeSettings}>×</button>
        </div>
        <div class="settings-content">
          <div class="settings-section">
            <h3>已索引路径 (共 {indexedPaths.length} 个)</h3>
            <p class="settings-hint">管理已建立的索引，可以删除不需要的索引以释放空间</p>
            <div class="indexed-list">
              {#each indexedPaths as item}
                <div class="indexed-item">
                  <div class="indexed-info">
                    <span class="indexed-path">{item.path}</span>
                    <span class="indexed-stats">
                      {item.file_count.toLocaleString()} 文件 | {item.dir_count.toLocaleString()} 目录
                    </span>
                  </div>
                  <button class="remove-btn" on:click={() => deleteIndexedPath(item.path)}>删除</button>
                </div>
              {:else}
                <div class="empty-list">暂无索引（数据加载中...）</div>
              {/each}
            </div>
          </div>

          <div class="settings-section">
            <h3>排除路径</h3>
            <p class="settings-hint">排除的路径及其子目录将不会被索引</p>
            <div class="exclude-input-group">
              <input
                type="text"
                class="exclude-input"
                placeholder="输入要排除的路径，例如: /Volumes/ExtDisk"
                bind:value={newExcludePath}
                on:keydown={(e) => e.key === 'Enter' && addExcludePath()}
              />
              <button class="add-btn" on:click={addExcludePath}>添加</button>
            </div>
            <div class="exclude-list">
              {#each excludePaths as path, i}
                <div class="exclude-item">
                  <span class="exclude-path">{path}</span>
                  <button class="remove-btn" on:click={() => removeExcludePath(i)}>删除</button>
                </div>
              {:else}
                <div class="empty-list">暂无排除路径</div>
              {/each}
            </div>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- sudo 密码输入对话框 -->
  {#if showSudoPasswordDialog}
    <div class="sudo-overlay" on:click={cancelSudoPassword}>
      <div class="sudo-dialog" on:click|stopPropagation>
        <div class="sudo-header">
          <h3>请输入你的登录密码 (sudo 密码)</h3>
          <button class="sudo-close" on:click={cancelSudoPassword}>×</button>
        </div>
        <div class="sudo-body">
          <input
            type="password"
            class="sudo-input"
            placeholder="输入密码"
            bind:value={sudoPassword}
            autofocus
            on:keydown={(e) => {
              if (e.key === 'Enter') {
                confirmSudoPassword()
              } else if (e.key === 'Escape') {
                cancelSudoPassword()
              }
            }}
          />
        </div>
        <div class="sudo-footer">
          <button class="sudo-cancel-btn" on:click={cancelSudoPassword}>取消</button>
          <button class="sudo-ok-btn" on:click={confirmSudoPassword}>确定</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- 删除确认对话框 -->
  {#if showDeleteConfirm}
    <div class="sudo-overlay" on:click={cancelDelete}>
      <div class="sudo-dialog" on:click|stopPropagation>
        <div class="sudo-header">
          <h3>确认删除</h3>
          <button class="sudo-close" on:click={cancelDelete}>×</button>
        </div>
        <div class="sudo-body" style="text-align: left; padding: 20px;">
          <p style="margin: 0 0 12px 0; font-size: 14px; color: #000; line-height: 1.6;">
            确定要删除索引路径 <strong style="color: #d32f2f;">"{deleteTargetPath}"</strong> 吗？
          </p>
          <p style="margin: 0; font-size: 13px; color: #555; line-height: 1.5;">
            这将删除该路径下的所有索引数据
          </p>
        </div>
        <div class="sudo-footer">
          <button class="sudo-cancel-btn" on:click={cancelDelete}>取消</button>
          <button class="sudo-ok-btn" on:click={confirmDelete}>确定删除</button>
        </div>
      </div>
    </div>
  {/if}

</main>

<style>
  :global(body) {
    margin: 0;
    padding: 0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
    background: #f5f5f5;
  }

  main {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }

  .header {
    background: white;
    border-bottom: 1px solid #e0e0e0;
    padding: 12px 16px;
    box-shadow: 0 2px 4px rgba(0,0,0,0.05);
  }

  .search-box {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 8px;
  }

  .search-input-wrapper {
    flex: 1;
    position: relative;
    display: flex;
    align-items: center;
  }

  .search-input {
    flex: 1;
    width: 100%;
    padding: 8px 12px;
    padding-right: 32px; /* 为清空按钮留出空间 */
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 14px;
    outline: none;
    transition: border-color 0.2s;
  }

  .search-input:focus {
    border-color: #007bff;
  }

  .clear-btn {
    position: absolute;
    right: 8px;
    background: none;
    border: none;
    color: #999;
    cursor: pointer;
    font-size: 16px;
    width: 20px;
    height: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    border-radius: 50%;
    transition: all 0.2s;
  }

  .clear-btn:hover {
    background-color: #e0e0e0;
    color: #333;
  }

  .regex-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
    color: #555;
    font-weight: 500;
  }

  .regex-label span {
    color: #555;
  }

  .regex-label:hover {
    color: #007bff;
  }

  .regex-label:hover span {
    color: #007bff;
  }

  .stats {
    font-size: 12px;
    color: #666;
  }

  .result-count {
    color: #007bff;
    font-weight: 500;
    margin-left: 4px;
  }

  .indexing-info {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-bottom: 4px;
  }

  .progress-bar {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .progress-bar-container {
    flex: 1;
    height: 8px;
    background-color: #e0e0e0;
    border-radius: 4px;
    overflow: hidden;
  }

  .progress-bar-fill {
    height: 100%;
    background-color: #007AFF;
    transition: width 0.3s ease;
  }

  .progress-percentage {
    font-size: 12px;
    color: #666;
    min-width: 50px;
    text-align: right;
  }

  .progress-details {
    display: flex;
    gap: 12px;
    align-items: center;
    flex-wrap: wrap;
  }

  .scan-stats {
    color: #999;
    font-size: 13px;
  }

  .speed {
    color: #4caf50;
    font-weight: 500;
  }

  .current-file {
    font-size: 11px;
    color: #999;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 100%;
    animation: fadeIn 0.1s ease-in;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(-2px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .indexing {
    color: #007bff;
    font-weight: 500;
  }

  .indexing-path {
    color: #666;
    font-size: 11px;
    font-weight: normal;
    margin-left: 4px;
  }

  .indexed-path-label {
    color: #666;
    font-weight: 500;
    margin-right: 4px;
  }

  .separator {
    color: #ccc;
    margin: 0 4px;
  }

  .elapsed-time {
    color: #4caf50;
    font-weight: 500;
  }

  .results-container {
    flex: 1;
    overflow: auto;
    background: white;
  }

  .results-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
    table-layout: fixed; /* 强制使用固定列宽，不被内容撑开 */
  }

  .results-table thead {
    position: sticky;
    top: 0;
    background: #f9f9f9;
    border-bottom: 1px solid #e0e0e0;
    z-index: 10;
  }

  .results-table th {
    text-align: left;
    padding: 8px 12px;
    font-weight: 600;
    color: #333;
    position: relative;
    user-select: none;
    border-right: 2px solid #bbb;
  }

  .results-table th:last-child {
    border-right: none;
  }

  /* 列宽调整手柄 */
  .resize-handle {
    position: absolute;
    right: -1px;
    top: 0;
    bottom: 0;
    width: 2px; /* 分隔线宽度 */
    cursor: col-resize;
    z-index: 20; /* 提升 z-index，确保在 thead (z-index:10) 之上 */
    background: rgba(0, 0, 0, 0.15); /* 默认显示淡灰色 */
  }

  .resize-handle:hover {
    background: rgba(0, 123, 255, 0.4);
  }

  .result-item {
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .result-item:hover {
    background-color: #f5f5f5;
  }

  .result-item.selected {
    background-color: #0066cc;
    color: white;
  }

  .result-item.selected td {
    color: white;
  }

  .result-item.selected .col-path {
    color: rgba(255, 255, 255, 0.85);
  }

  .result-item td {
    padding: 6px 12px;
    border-bottom: 1px solid #f0f0f0;
    color: #333;
    text-align: left;
    vertical-align: middle;
    overflow: hidden; /* 隐藏溢出内容 */
    text-overflow: ellipsis; /* 用省略号表示截断 */
    white-space: nowrap; /* 不换行 */
  }

  .col-name {
    color: #333;
    font-weight: 500;
    text-align: left;
  }

  .file-name-cell {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .file-icon {
    font-size: 16px;
    flex-shrink: 0;
  }

  .file-icon-img {
    width: 20px;
    height: 20px;
    flex-shrink: 0;
    object-fit: contain;
  }

  .col-path {
    color: #666;
    font-size: 12px;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .col-size {
    text-align: right;
    color: #999;
    font-size: 12px;
  }

  .no-results {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 200px;
    color: #999;
    font-size: 14px;
  }

  .welcome {
    padding: 60px 40px;
    text-align: center;
    color: #666;
  }

  .welcome h2 {
    margin: 0 0 12px 0;
    color: #333;
  }

  .welcome p {
    margin: 0 0 24px 0;
    font-size: 14px;
  }

  .welcome ul {
    list-style: none;
    padding: 0;
    margin: 0;
    display: inline-block;
    text-align: left;
  }

  .welcome li {
    margin: 8px 0;
    font-size: 13px;
  }

  .welcome li:before {
    content: "✓ ";
    color: #4caf50;
    font-weight: bold;
    margin-right: 8px;
  }

  /* 右键菜单 */
  .context-menu {
    position: fixed;
    background: white;
    border: 1px solid #ddd;
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.15);
    padding: 4px 0;
    min-width: 160px;
    z-index: 1000;
  }

  .menu-item {
    padding: 8px 16px;
    cursor: pointer;
    font-size: 13px;
    color: #333;
  }

  .menu-item:hover {
    background: #f0f0f0;
  }

  /* 重建索引按钮 */
  .rebuild-btn {
    padding: 6px 12px;
    background: #f5f5f5;
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    transition: all 0.2s;
  }

  .rebuild-btn:hover {
    background: #007bff;
    color: white;
    border-color: #007bff;
  }

  /* 停止按钮 */
  .stop-btn {
    padding: 6px 12px;
    background: #dc3545;
    color: white;
    border: 1px solid #dc3545;
    border-radius: 4px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    transition: all 0.2s;
  }

  .stop-btn:hover {
    background: #c82333;
    border-color: #bd2130;
  }

  /* 加载更多和已加载完毕提示 */
  .loading-more,
  .no-more {
    text-align: center;
    padding: 12px;
    font-size: 13px;
    color: #999;
  }

  .loading-more {
    color: #007bff;
  }

  /* 设置对话框 */
  .settings-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2000;
  }

  .settings-dialog {
    background: white;
    border-radius: 8px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
    width: 600px;
    max-width: 90vw;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
  }

  .settings-header {
    padding: 20px 24px;
    border-bottom: 1px solid #eee;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .settings-header h2 {
    margin: 0;
    font-size: 20px;
    color: #333;
  }

  .close-btn {
    background: none;
    border: none;
    font-size: 28px;
    color: #999;
    cursor: pointer;
    padding: 0;
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 4px;
    transition: all 0.2s;
  }

  .close-btn:hover {
    background: #f0f0f0;
    color: #333;
  }

  .settings-content {
    padding: 24px;
    overflow-y: auto;
    flex: 1;
  }

  .settings-section {
    margin-bottom: 24px;
  }

  .settings-section:last-child {
    margin-bottom: 0;
  }

  .settings-section h3 {
    margin: 0 0 8px 0;
    font-size: 16px;
    color: #333;
  }

  .settings-hint {
    margin: 0 0 16px 0;
    font-size: 13px;
    color: #666;
  }

  .exclude-input-group {
    display: flex;
    gap: 8px;
    margin-bottom: 16px;
  }

  .exclude-input {
    flex: 1;
    padding: 8px 12px;
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 14px;
  }

  .exclude-input:focus {
    outline: none;
    border-color: #007bff;
  }

  .add-btn {
    padding: 8px 16px;
    background: #007bff;
    color: white;
    border: none;
    border-radius: 4px;
    font-size: 14px;
    cursor: pointer;
    transition: all 0.2s;
  }

  .add-btn:hover {
    background: #0056b3;
  }

  .exclude-list {
    border: 1px solid #eee;
    border-radius: 4px;
    max-height: 200px;
    overflow-y: auto;
  }

  .exclude-item {
    padding: 12px;
    border-bottom: 1px solid #f0f0f0;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .exclude-item:last-child {
    border-bottom: none;
  }

  .exclude-path {
    flex: 1;
    font-size: 14px;
    color: #333;
    word-break: break-all;
  }

  .remove-btn {
    padding: 4px 12px;
    background: #dc3545;
    color: white;
    border: none;
    border-radius: 4px;
    font-size: 12px;
    cursor: pointer;
    transition: all 0.2s;
    margin-left: 12px;
  }

  .remove-btn:hover {
    background: #c82333;
  }

  .empty-list {
    padding: 24px;
    text-align: center;
    color: #999;
    font-size: 14px;
  }

  /* 索引列表样式 */
  .indexed-list {
    border: 1px solid #eee;
    border-radius: 4px;
    max-height: 200px;
    overflow-y: auto;
    margin-bottom: 24px;
  }

  .indexed-item {
    padding: 12px;
    border-bottom: 1px solid #f0f0f0;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .indexed-item:last-child {
    border-bottom: none;
  }

  .indexed-info {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .indexed-path {
    font-size: 14px;
    color: #333;
    font-weight: 500;
    word-break: break-all;
  }

  .indexed-stats {
    font-size: 12px;
    color: #666;
  }


  /* sudo 密码输入对话框样式 */
  .sudo-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    justify-content: center;
    align-items: center;
    z-index: 10000;
  }

  .sudo-dialog {
    background: white;
    border-radius: 8px;
    box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
    width: 400px;
    max-width: 90vw;
    overflow: hidden;
  }

  .sudo-header {
    padding: 16px 20px;
    border-bottom: 1px solid #e0e0e0;
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .sudo-header h3 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
    color: #333;
  }

  .sudo-close {
    background: none;
    border: none;
    font-size: 24px;
    color: #999;
    cursor: pointer;
    padding: 0;
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }

  .sudo-close:hover {
    color: #333;
  }

  .sudo-body {
    padding: 20px;
  }

  .sudo-input {
    width: 100%;
    padding: 10px 12px;
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 14px;
    box-sizing: border-box;
  }

  .sudo-input:focus {
    outline: none;
    border-color: #007bff;
  }

  .sudo-footer {
    padding: 12px 20px;
    border-top: 1px solid #e0e0e0;
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }

  .sudo-cancel-btn {
    padding: 8px 16px;
    background: white;
    color: #333;
    border: 1px solid #ddd;
    border-radius: 4px;
    font-size: 14px;
    cursor: pointer;
    transition: all 0.2s;
  }

  .sudo-cancel-btn:hover {
    background: #f5f5f5;
  }

  .sudo-ok-btn {
    padding: 8px 16px;
    background: #007bff;
    color: white;
    border: none;
    border-radius: 4px;
    font-size: 14px;
    cursor: pointer;
    transition: all 0.2s;
  }

  .sudo-ok-btn:hover {
    background: #0056b3;
  }

  .settings-footer {
    padding: 16px 24px;
    border-top: 1px solid #eee;
    display: flex;
    justify-content: flex-end;
    gap: 12px;
    background: white;
    position: sticky;
    bottom: 0;
    z-index: 10;
  }

  .cancel-btn,
  .save-btn {
    padding: 8px 20px;
    border: none;
    border-radius: 4px;
    font-size: 14px;
    cursor: pointer;
    transition: all 0.2s;
  }

  .cancel-btn {
    background: #f5f5f5;
    color: #333;
  }

  .cancel-btn:hover {
    background: #e0e0e0;
  }

  .save-btn {
    background: #007bff;
    color: white;
  }

  .save-btn:hover {
    background: #0056b3;
  }
</style>
