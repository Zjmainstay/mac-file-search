# 发布前检查清单

## 🔧 必须完成的任务

### 1. 更新项目配置

- [x] 更新 `go.mod` 中的模块路径为实际的 GitHub 路径
  ```go
  module github.com/Zjmainstay/mac-file-search
  ```

- [x] 更新 README.md 中的占位符：
  - [x] 替换 `yourusername` 为实际的 GitHub 用户名
  - [x] 替换邮箱地址 `your.email@example.com`
  - [x] 更新克隆仓库的 URL

- [x] 更新 CONTRIBUTING.md 中的 GitHub 链接

### 2. 测试构建

- [x] 测试命令行工具构建
  ```bash
  make scanner
  ./mac-file-search -path /tmp -output test.json
  ```

- [x] 测试 GUI 应用构建
  ```bash
  make app
  # 检查 mac-search-app/build/bin/ 目录
  ```

- [x] 测试在干净环境中构建
  ```bash
  make clean
  make all
  ```

### 3. 代码检查

- [ ] 运行 `go fmt ./...` 格式化代码
- [ ] 运行 `go vet ./...` 检查代码问题
- [ ] 检查是否有敏感信息（密码、API key 等）
- [x] 确保所有脚本有正确的权限（chmod +x）

### 4. 文档完善

- [ ] 检查 README.md 中的所有链接
- [ ] 确保所有代码示例可以运行
- [ ] 添加项目截图（可选但推荐）
- [ ] 检查文档中的拼写错误

### 5. Git 配置

- [x] 确保 `.gitignore` 包含所有必要的忽略规则
- [x] 检查是否有不应提交的大文件
- [x] 确认所有提交信息清晰明了

### 6. GitHub 设置

- [ ] 在 GitHub 上创建新仓库 `mac-file-search`（已存在：https://github.com/Zjmainstay/mac-file-search）
- [ ] 添加仓库描述和标签
- [ ] 设置默认分支为 `main`
- [ ] 启用 Issues 和 Discussions（可选）
- [ ] 添加仓库主题（topics）：
  - `go`
  - `macos`
  - `file-search`
  - `filesystem`
  - `cli`
  - `gui`
  - `wails`

### 7. 发布准备

- [ ] 创建 Release Notes
- [ ] 打包可执行文件（如果需要提供预编译版本）
- [ ] 准备使用说明视频或 GIF 演示（可选）

---

## ✅ 已完成的工作

### 项目迁移和配置
- [x] 从源项目迁移核心代码
- [x] 更新 Go 模块路径
- [x] 统一项目命名（mac-file-search）
- [x] 添加应用图标和资源文件
- [x] 配置 .gitignore
- [x] 创建项目文档（README、BUILD、CONTRIBUTING、PROJECT）

### 构建系统
- [x] 创建 Makefile
- [x] 修复前端构建问题
- [x] 验证命令行工具构建
- [x] 验证 GUI 应用构建

### 文档和资源
- [x] 添加 Issue 和 PR 模板
- [x] 添加技术文档
- [x] 添加 MIT 许可证
- [x] 添加图标处理工具

### Git 管理
- [x] 初始化 Git 仓库
- [x] 10 次有意义的提交
- [x] 所有提交标记为 [claude]

## 📝 推荐任务（可选）

### 增强功能

- [ ] 添加单元测试
- [ ] 添加集成测试
- [ ] 设置 CI/CD（GitHub Actions）
- [ ] 添加代码覆盖率报告
- [ ] 添加性能基准测试

### 文档增强

- [ ] 添加中文和英文双语文档
- [ ] 创建 Wiki 页面
- [ ] 添加常见问题（FAQ）
- [ ] 添加故障排除指南
- [ ] 创建视频教程

### 社区建设

- [ ] 创建 Discord/Slack 频道
- [ ] 设置 Discussions 论坛
- [ ] 添加 CODE_OF_CONDUCT.md
- [ ] 添加 SECURITY.md（安全政策）
- [ ] 设置 Issue 标签

## 🚀 发布步骤

### 1. 推送到 GitHub

```bash
# 添加远程仓库（如果尚未添加）
git remote add origin https://github.com/Zjmainstay/mac-file-search.git

# 推送代码
git push -u origin main
```

### 2. 创建第一个 Release

1. 访问 GitHub 仓库
2. 点击 "Releases" → "Create a new release"
3. 标签版本：`v0.1.0`
4. Release 标题：`v0.1.0 - 初始发布`
5. 描述发布内容：
   ```markdown
   ## 🎉 初始发布

   Mac File Search 是一个高性能的 macOS 文件扫描与搜索工具。

   ### ✨ 主要功能
   - ⚡ 极速扫描：2分钟扫描200万+文件
   - 🔍 强大搜索：支持模糊搜索、正则表达式
   - 💾 离线使用：一次扫描，永久搜索
   - 📦 双模式：命令行工具 + GUI应用

   ### 📦 下载
   - 命令行工具：见构建说明
   - GUI应用：见构建说明

   ### 📖 文档
   - [README](README.md)
   - [构建说明](BUILD.md)
   - [贡献指南](CONTRIBUTING.md)
   ```

### 3. 推广

- [ ] 在相关社区分享（Reddit、Hacker News 等）
- [ ] 发推文/微博
- [ ] 在 Go 社区论坛发布
- [ ] 提交到 awesome-go 列表

## ✅ 最终检查

发布前最后确认：

- [ ] 所有链接可用
- [ ] 文档完整且准确
- [ ] 代码可以成功构建
- [ ] LICENSE 文件存在
- [ ] README 包含快速开始指南
- [ ] GitHub 仓库设置正确
- [ ] 提交历史清晰

---

**准备好了吗？** 完成上述检查后，就可以正式发布了！🚀
