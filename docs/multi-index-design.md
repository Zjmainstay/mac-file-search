# 多目录索引设计文档

## 概述

实现了支持多目录索引的功能，允许用户同时索引多个目录（如 `/Applications`、`/Users`、`/System` 等），并可以单独管理每个索引。

## 核心设计

### 1. 数据库结构变更

在 `files` 表中添加了 `indexed_path` 字段来标识每个文件属于哪个索引路径：

```sql
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

-- 为indexed_path创建索引，优化按路径查询和删除
CREATE INDEX IF NOT EXISTS idx_indexed_path ON files(indexed_path);
```

### 2. 路径重叠处理策略

**问题场景**：
- 用户先扫描全盘 `/`，索引了 `/Applications/Chrome.app`
- 然后又扫描 `/Applications`，同样要索引 `/Applications/Chrome.app`
- 因为 `path` 字段有 UNIQUE 约束，会导致冲突

**解决方案 - 智能覆盖**：

#### 扫描新路径时
```go
// 规范化路径
normalizedPath := rootPath
if !strings.HasSuffix(normalizedPath, "/") {
    normalizedPath += "/"
}

// 删除所有与新路径重叠的文件（无论它们的indexed_path是什么）
DELETE FROM files WHERE path = ? OR path LIKE ?
// 参数：rootPath, normalizedPath + "%"
```

这样可以：
- 避免UNIQUE约束冲突
- 确保新索引是最新的数据
- 防止搜索结果重复

#### 删除索引时
```go
// 只删除属于该indexed_path的记录
DELETE FROM files WHERE indexed_path = ?
```

这样可以：
- 精确删除指定路径的索引
- 不影响其他路径的索引

### 3. 使用场景示例

#### 场景1：先全盘后局部
1. 用户扫描 `/`（全盘），所有文件的 `indexed_path = '/'`
2. 用户又扫描 `/Applications`
   - 删除所有 `path LIKE '/Applications/%'` 的记录（无论indexed_path）
   - 重新索引，新记录的 `indexed_path = '/Applications'`
3. 结果：
   - `/Applications` 下的文件属于 `/Applications` 索引
   - 其他文件属于 `/` 索引
   - 搜索时不会有重复

#### 场景2：先局部后全盘
1. 用户扫描 `/Applications`，所有文件的 `indexed_path = '/Applications'`
2. 用户又扫描 `/`（全盘）
   - 删除所有记录（因为全盘包含所有子路径）
   - 重新索引，所有记录的 `indexed_path = '/'`
3. 结果：
   - 所有文件属于 `/` 索引
   - 原来的 `/Applications` 索引被覆盖

#### 场景3：多个独立目录
1. 用户扫描 `/Applications`，`indexed_path = '/Applications'`
2. 用户扫描 `/Users`，`indexed_path = '/Users'`
3. 用户删除 `/Applications` 索引
   - 只删除 `indexed_path = '/Applications'` 的记录
   - `/Users` 的索引不受影响

## 数据迁移

### 自动迁移逻辑

```go
// 检查indexed_path列是否存在
var colCount int
db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('files') WHERE name='indexed_path'").Scan(&colCount)

if colCount == 0 {
    // 添加列
    db.Exec("ALTER TABLE files ADD COLUMN indexed_path TEXT NOT NULL DEFAULT ''")

    // 创建索引
    db.Exec("CREATE INDEX IF NOT EXISTS idx_indexed_path ON files(indexed_path)")

    // 为旧数据填充indexed_path
    var oldIndexPath string
    db.QueryRow("SELECT value FROM config WHERE key = 'index_path'").Scan(&oldIndexPath)
    if oldIndexPath != "" {
        db.Exec("UPDATE files SET indexed_path = ? WHERE indexed_path = ''", oldIndexPath)
    }
}
```

这样可以确保：
- 旧版本升级到新版本时自动添加字段
- 旧数据不会丢失，自动填充indexed_path

## API 设计

### 后端 API

#### 1. GetIndexedPaths()
```go
type IndexedPath struct {
    Path      string `json:"path"`
    FileCount int64  `json:"file_count"`
    DirCount  int64  `json:"dir_count"`
}

func (idx *Indexer) GetIndexedPaths() ([]IndexedPath, error)
```

返回所有已索引的路径及统计信息。

#### 2. DeleteIndexedPath(path string)
```go
func (idx *Indexer) DeleteIndexedPath(path string) error
```

删除指定路径的索引，并自动执行VACUUM回收空间。

### 前端 API

从 `wailsjs/go/main/App.js` 导入：
```javascript
import { GetIndexedPaths, DeleteIndexedPath } from '../wailsjs/go/main/App.js'
```

## 前端界面

### 设置对话框新增内容

```svelte
<div class="settings-section">
  <h3>已索引路径</h3>
  <p class="settings-hint">管理已建立的索引，可以删除不需要的索引以释放空间</p>
  <div class="indexed-list">
    {#each indexedPaths as item}
      <div class="indexed-item">
        <div class="indexed-info">
          <span class="indexed-path">{item.path}</span>
          <span class="indexed-stats">
            {item.file_count.toLocaleString()} 文件 |
            {item.dir_count.toLocaleString()} 目录
          </span>
        </div>
        <button on:click={() => deleteIndexedPath(item.path)}>删除</button>
      </div>
    {/each}
  </div>
</div>
```

### 功能说明

1. **打开设置时**：自动加载所有已索引的路径列表
2. **显示统计**：显示每个索引路径包含的文件数和目录数
3. **删除索引**：点击删除按钮，弹出确认对话框后删除
4. **自动刷新**：删除后自动刷新列表和首页统计

## 性能优化

### 1. 索引优化
- 为 `indexed_path` 创建索引，加速按路径删除和统计
- 保留 `idx_name` 索引用于搜索
- 删除多余的 `idx_ext` 和 `idx_path` 索引

### 2. 删除优化
- DELETE 操作使用 `WHERE indexed_path = ?`，利用索引快速定位
- 删除后自动执行 VACUUM 回收空间

### 3. 统计优化
- 使用 GROUP BY 和聚合函数一次性获取所有路径统计
- 避免多次查询数据库

## 注意事项

### 1. 路径规范化
所有路径比较前都要规范化，确保以 `/` 结尾：
```go
normalizedPath := rootPath
if !strings.HasSuffix(normalizedPath, "/") {
    normalizedPath += "/"
}
```

### 2. UNIQUE 约束
`path` 字段仍然保持 UNIQUE 约束，确保同一个文件路径在数据库中只有一条记录。

### 3. 数据一致性
- 扫描新路径前，先删除所有重叠的文件
- 删除索引时，只删除该 indexed_path 的记录
- 这样可以保证数据一致性，避免重复和冲突

## 使用流程

### 用户操作流程

1. **首次使用**：
   - 选择要索引的路径（如 `/Applications`）
   - 系统扫描并建立索引，`indexed_path = '/Applications'`
   - 可以搜索该路径下的文件

2. **添加新索引**：
   - 再次点击"重建索引"，选择新路径（如 `/Users`）
   - 系统扫描并建立索引，`indexed_path = '/Users'`
   - 现在可以同时搜索两个路径下的文件

3. **管理索引**：
   - 打开设置，查看"已索引路径"列表
   - 看到 `/Applications` 和 `/Users` 两个索引及其统计
   - 可以删除不需要的索引释放空间

4. **重新扫描**：
   - 再次扫描 `/Applications`，会先删除该路径下的旧数据
   - 然后重新索引，数据更新为最新状态
   - 其他索引（如 `/Users`）不受影响

## 技术优势

1. **灵活性**：支持同时索引多个目录，不局限于单一路径
2. **一致性**：智能处理路径重叠，避免数据重复和冲突
3. **性能**：使用索引优化查询和删除，支持大量文件
4. **可维护性**：清晰的数据模型，易于理解和扩展
5. **用户体验**：直观的界面，显示统计信息，方便管理

## 测试建议

### 测试用例

1. **基本功能**：
   - 索引单个目录
   - 索引多个独立目录
   - 删除单个索引
   - 删除所有索引

2. **路径重叠**：
   - 先索引 `/`，再索引 `/Applications`
   - 先索引 `/Applications`，再索引 `/`
   - 验证数据不重复，可以正常搜索

3. **数据迁移**：
   - 使用旧版本建立索引
   - 升级到新版本
   - 验证旧数据自动填充 indexed_path
   - 验证功能正常

4. **边界情况**：
   - 空数据库
   - 只有一个索引
   - 索引路径包含特殊字符
   - 非常长的路径

## 未来改进方向

1. **索引优先级**：如果多个索引包含同一文件，可以显示来源
2. **增量更新**：监控文件系统变化，自动更新索引
3. **索引合并**：提供选项将多个小索引合并为一个大索引
4. **空间统计**：显示每个索引占用的磁盘空间
5. **导入导出**：支持导出索引数据，在不同机器间共享
