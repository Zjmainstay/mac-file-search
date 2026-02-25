# Mac 文件搜索工具

一款类似 Windows Everything 的 Mac 文件搜索桌面应用，提供毫秒级的文件搜索体验。

## 功能特性

### 核心功能

- **🚀 快速索引**: 使用 SQLite 构建文件索引，支持全盘扫描
- **⚡ 实时搜索**: 输入即搜索，毫秒级响应
- **📊 进度显示**: 实时显示索引进度、扫描速度和当前文件
- **🔄 增量更新**: 使用 fsnotify 监听文件系统变化，自动更新索引
- **🎯 多种搜索模式**:
  - 通配符搜索: 支持 `*` 和 `?` 通配符
  - 多关键词搜索: 空格分隔多个关键词（如: `业务线 代码 sleep_run.php`）
  - 正则表达式搜索: 支持高级正则表达式（如: `(jpg|png)$`）
- **📄 无限滚动**: 自动分页加载，滚动到底部加载更多结果
- **🎨 用户友好界面**:
  - 可调整列宽: 拖拽表头分割线调整列宽
  - 键盘导航: 支持上下箭头、Enter 打开文件、Cmd+C 复制路径
  - 右键菜单: 打开文件、在 Finder 中显示、复制路径
  - 结果计数: 实时显示搜索到的文件数量
  - 索引路径显示: 显示当前正在索引的目录路径

### 数据持久化

- **批量提交**: 每 1000 条记录提交一次，确保索引过程中意外退出也能保留数据
- **可恢复**: 重新打开应用后，之前的索引数据仍然可用

### 性能优化

- **分批扫描**: 避免一次性加载所有结果，减少内存占用
- **跳过系统目录**: 自动跳过 `/dev`、`/System/Volumes/*` 等系统目录
- **智能排序**: 优先显示文件名匹配、路径较短的结果

## 技术栈

- **后端**: Go + Wails v2
  - SQLite: 文件索引存储
  - fsnotify: 文件系统监听
  - 并发安全: 使用 atomic 和 sync.RWMutex
- **前端**: Svelte + Vite
  - 响应式设计
  - 事件驱动架构

## 开发指南

### 环境要求

- Go 1.18+
- Node.js 16+
- Wails CLI v2.11.0+

### 安装依赖

```bash
# 安装 Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# 安装前端依赖
cd frontend
npm install
```

### 开发模式

```bash
# 运行开发服务器（支持热重载）
wails dev
```

这将启动一个 Vite 开发服务器，提供快速的前端热重载。同时会在 http://localhost:34115 提供一个浏览器开发服务器，可以在浏览器中调用 Go 方法进行调试。

### 构建应用

```bash
# 构建生产版本
wails build

# 构建产物位置
# macOS: build/bin/mac-search-app.app
```

### 前端单独构建

```bash
cd frontend
npm run build
```

## 使用说明

### 首次启动

1. 首次启动会自动扫描根目录 `/` 建立索引
2. 可以点击"重建索引"按钮选择特定目录进行索引

### 搜索技巧

**基础搜索**:
```
example.txt          # 搜索包含 example.txt 的文件
```

**通配符搜索**:
```
*.log                # 搜索所有 .log 文件
test?.txt            # 搜索 test1.txt, testA.txt 等
```

**多关键词搜索**:
```
项目 文档 设计稿      # 路径包含所有关键词的文件
```

**正则表达式搜索**（勾选"正则"）:
```
(jpg|png|gif)$       # 搜索所有图片文件
^test.*\.js$         # 搜索以 test 开头的 js 文件
```

### 快捷键

- `↑` / `↓`: 上下选择文件
- `Enter`: 打开选中的文件
- `Cmd+C`: 复制选中文件的路径
- `ESC`: 关闭右键菜单

### 右键菜单

- **打开文件**: 使用默认应用打开文件
- **在 Finder 中显示**: 在 Finder 中定位并高亮文件
- **复制路径**: 将文件完整路径复制到剪贴板

## 项目结构

```
mac-search-app/
├── app.go              # 应用主逻辑，连接前后端
├── indexer.go          # 文件索引核心，SQLite 操作
├── watcher.go          # 文件系统监听
├── search.go           # 高级搜索功能
├── utils.go            # 工具函数
├── main.go             # 应用入口
└── frontend/           # Svelte 前端
    ├── src/
    │   └── App.svelte  # 主界面组件
    └── dist/           # 构建输出
```

## 数据库结构

```sql
CREATE TABLE files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,      -- 文件完整路径
    name TEXT NOT NULL,              -- 文件名
    size INTEGER NOT NULL,           -- 文件大小（字节）
    mod_time INTEGER NOT NULL,       -- 修改时间（Unix 时间戳）
    is_dir INTEGER NOT NULL,         -- 是否为目录（0/1）
    ext TEXT NOT NULL                -- 文件扩展名
);

-- 索引优化
CREATE INDEX idx_name ON files(name);
CREATE INDEX idx_ext ON files(ext);
CREATE INDEX idx_path ON files(path);
```

数据库位置: `~/.mac-search-app/index.db`

## 已知限制

- 正则表达式搜索：需要分批从数据库读取所有记录（每批 5000 条）并在应用层过滤，性能相对较慢，但支持无限滚动分页（每次 500 条）
- 普通搜索和通配符搜索：直接在数据库层过滤，性能较快，支持无限滚动分页（每次 500 条）
- 自动跳过特定系统目录以提升性能

## 更新日志

### v1.0.0 (2026-01-23)

**新增功能**:
- ✅ SQLite 文件索引系统
- ✅ 实时文件系统监听
- ✅ 通配符、多关键词、正则表达式搜索
- ✅ 可调整列宽
- ✅ 键盘导航和右键菜单
- ✅ 批量提交确保数据持久化
- ✅ 无限滚动分页
- ✅ 实时进度显示（文件数、速度、当前文件）
- ✅ 停止索引功能
- ✅ 重建索引（选择目录）

**用户体验优化**:
- ✅ 禁用输入框首字母自动大写
- ✅ 显示搜索结果计数（"找到 X 个结果"）
- ✅ 加载指示器（"加载中..."、"已显示全部结果"）
- ✅ 显示当前索引目录路径
- ✅ 表头分割线默认可见
- ✅ 正则复选框标签始终可见

## 许可证

MIT License

## 贡献

欢迎提交 Issue 和 Pull Request！

## 致谢

- [Wails](https://wails.io/) - Go + Web 桌面应用框架
- [Svelte](https://svelte.dev/) - 响应式前端框架
- [SQLite](https://www.sqlite.org/) - 嵌入式数据库
- [fsnotify](https://github.com/fsnotify/fsnotify) - 文件系统监听库
