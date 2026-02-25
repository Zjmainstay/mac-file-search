# 贡献指南

感谢您对 Mac File Search 项目的关注！我们欢迎各种形式的贡献。

## 📋 行为准则

我们致力于为所有人提供一个友好、安全和热情的环境。请：

- 使用友好和包容的语言
- 尊重不同的观点和经验
- 优雅地接受建设性的批评
- 专注于对社区最有利的事情
- 对其他社区成员表现出同理心

## 🐛 报告 Bug

在提交 Bug 报告之前，请：

1. **检查是否已有相同的 Issue**：搜索现有的 Issues，避免重复报告
2. **使用最新版本**：确保在最新版本上复现问题
3. **提供详细信息**：
   - 清晰的标题
   - 复现步骤
   - 预期行为
   - 实际行为
   - 系统环境（macOS 版本、Go 版本等）
   - 相关的日志或错误信息

### Bug 报告模板

```markdown
**描述问题**
简要描述遇到的问题

**复现步骤**
1. 执行 '...'
2. 点击 '....'
3. 查看错误

**预期行为**
描述期望看到的结果

**实际行为**
描述实际看到的结果

**环境信息**
- macOS 版本：
- Go 版本：
- 项目版本：

**附加信息**
添加任何其他相关信息、截图或日志
```

## ✨ 提出新功能

我们欢迎新功能的建议！在提交之前：

1. **检查是否已有相同的建议**
2. **明确描述功能**：清楚地说明功能的目的和使用场景
3. **考虑实现方案**：如果可以，提供可能的实现思路

### 功能请求模板

```markdown
**功能描述**
简要描述提议的功能

**使用场景**
描述这个功能在什么情况下有用

**可能的实现**
如果有想法，描述可能的实现方式

**替代方案**
描述考虑过的其他方案
```

## 🔧 开发流程

### 设置开发环境

```bash
# 1. Fork 并克隆仓库
git clone https://github.com/your-username/mac-file-search.git
cd mac-file-search

# 2. 添加上游仓库
git remote add upstream https://github.com/original/mac-file-search.git

# 3. 安装依赖
go mod tidy

# 4. 构建项目
make
```

### 创建分支

```bash
# 从最新的 main 分支创建新分支
git checkout main
git pull upstream main
git checkout -b feature/your-feature-name
```

### 编写代码

1. **遵循代码规范**：
   - 使用 `gofmt` 格式化代码
   - 遵循 Go 的最佳实践
   - 保持代码简洁和可读

2. **编写测试**：
   - 为新功能添加测试
   - 确保现有测试通过
   - 测试覆盖边界情况

3. **更新文档**：
   - 更新 README.md（如果需要）
   - 更新内联注释
   - 更新 BUILD.md（如果涉及构建流程）

### 提交代码

```bash
# 1. 添加修改的文件
git add .

# 2. 提交（使用清晰的提交信息）
git commit -m "feat: 添加新功能描述"

# 提交信息格式：
# feat: 新功能
# fix: 修复 Bug
# docs: 文档更新
# style: 代码格式调整
# refactor: 重构
# test: 测试相关
# chore: 构建/工具相关
```

### 提交 Pull Request

1. **推送到你的 Fork**：
   ```bash
   git push origin feature/your-feature-name
   ```

2. **创建 Pull Request**：
   - 访问你的 Fork 页面
   - 点击 "New Pull Request"
   - 填写 PR 描述

3. **PR 描述模板**：
   ```markdown
   ## 变更说明
   描述这个 PR 做了什么改变

   ## 相关 Issue
   关闭 #issue_number

   ## 测试
   描述如何测试这些变更

   ## 检查清单
   - [ ] 代码已格式化（gofmt）
   - [ ] 添加了必要的测试
   - [ ] 更新了相关文档
   - [ ] 所有测试通过
   ```

## 🧪 运行测试

```bash
# 运行所有测试
go test ./...

# 运行测试并查看覆盖率
go test -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 📝 代码规范

### Go 代码风格

- 遵循 [Effective Go](https://golang.org/doc/effective_go.html)
- 使用 `gofmt` 格式化代码
- 变量名使用驼峰命名法
- 导出的函数和变量使用大写字母开头
- 为导出的函数和类型添加注释

### 提交信息规范

使用语义化提交信息：

- `feat:` 新功能
- `fix:` 修复 Bug
- `docs:` 文档更新
- `style:` 代码格式调整（不影响功能）
- `refactor:` 重构
- `test:` 测试相关
- `chore:` 构建/工具相关

示例：
```
feat: 添加文件类型过滤功能
fix: 修复并发扫描时的竞态条件
docs: 更新 README 中的使用示例
```

## 🔍 代码审查

所有提交的代码都会经过审查。审查者可能会：

- 提出问题或建议
- 要求进行修改
- 批准 PR

请：
- 及时回应审查意见
- 保持友好和专业
- 不要对批评性意见感到沮丧

## 📄 许可证

提交代码即表示您同意您的贡献将以 MIT 许可证发布。

## 💬 联系方式

如有任何问题，可以通过以下方式联系：

- 提交 [Issue](https://github.com/Zjmainstay/mac-file-search/issues)
- 在 Pull Request 中讨论

---

再次感谢您的贡献！🎉
