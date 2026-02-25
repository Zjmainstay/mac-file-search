# 发布流程说明

## 🚀 自动发布功能

本项目已配置 GitHub Actions，可以在打 tag 时自动构建和发布应用。

## 📋 发布步骤

### 1. 确保代码已提交并推送

```bash
git add .
git commit -m "准备发布 v0.1.0"
git push origin main
```

### 2. 创建并推送 tag

```bash
# 创建 tag（遵循语义化版本）
git tag -a v0.1.0 -m "Release v0.1.0 - 初始版本"

# 推送 tag 到 GitHub
git push origin v0.1.0
```

### 3. 自动构建和发布

推送 tag 后，GitHub Actions 会自动：
1. ✅ 检出代码
2. ✅ 安装 Go 和 Wails
3. ✅ 构建命令行工具（ARM64 + AMD64）
4. ✅ 构建 GUI 应用
5. ✅ 打包为 .tar.gz 和 .zip
6. ✅ 生成 SHA256 校验和
7. ✅ 创建 GitHub Release
8. ✅ 上传所有构建产物

### 4. 验证发布

访问：`https://github.com/Zjmainstay/mac-file-search/releases`

你会看到：
- 📦 GUI 应用 ZIP 包
- 📦 命令行工具（ARM64）
- 📦 命令行工具（AMD64）
- 🔐 校验和文件
- 📝 自动生成的 Release Notes

## 🔖 版本号规范

遵循 [语义化版本](https://semver.org/lang/zh-CN/)：

- **主版本号（Major）**：不兼容的 API 修改
  - 例如：`v1.0.0` → `v2.0.0`

- **次版本号（Minor）**：向下兼容的功能性新增
  - 例如：`v1.0.0` → `v1.1.0`

- **修订号（Patch）**：向下兼容的问题修正
  - 例如：`v1.0.0` → `v1.0.1`

### 示例版本进化

```
v0.1.0 - 初始发布
v0.1.1 - 修复扫描速度问题
v0.2.0 - 添加正则表达式过滤功能
v0.2.1 - 修复内存泄漏
v1.0.0 - 第一个稳定版本
```

## 📝 Release Notes 模板

自动生成的 Release Notes 包含：

- 🎉 版本标题
- ⚡ 下载链接（GUI + CLI）
- 📝 安装说明
- ✨ 主要功能
- 📖 文档链接
- 🔐 校验和验证方法

## 🔄 更新现有 Release

如果需要修改已发布的 Release：

```bash
# 删除本地 tag
git tag -d v0.1.0

# 删除远程 tag
git push origin :refs/tags/v0.1.0

# 重新创建 tag
git tag -a v0.1.0 -m "Release v0.1.0 - 更新说明"

# 重新推送
git push origin v0.1.0
```

**注意**：这会触发新的构建，覆盖原有的 Release。

## 🛠️ 本地测试构建

在创建 tag 之前，建议先在本地测试构建：

```bash
# 测试命令行工具
make clean
make scanner
./mac-file-search --help

# 测试 GUI 应用
make app
open mac-search-app/build/bin/Mac文件搜索.app
```

## 🎯 发布检查清单

在发布前确认：

- [ ] 所有测试通过
- [ ] 文档已更新
- [ ] CHANGELOG.md 已更新（如果有）
- [ ] 版本号已更新（如果需要）
- [ ] 本地构建成功
- [ ] 提交信息清晰

## 🚨 注意事项

1. **首次发布**：第一次推送 tag 时，GitHub Actions 可能需要手动启用
2. **构建时间**：完整构建大约需要 5-10 分钟
3. **权限问题**：确保仓库设置中启用了 "Read and write permissions" for workflows
4. **macOS 签名**：当前构建未签名，用户首次打开需要允许
5. **前端构建**：当前使用预编译的 dist，如需重新编译前端需要修改 workflow

## 💡 进阶功能

### 添加 DMG 打包

编辑 `.github/workflows/release.yml`，取消注释 DMG 相关部分：

```yaml
- name: Create DMG
  run: |
    brew install create-dmg
    create-dmg --volname "Mac文件搜索" ...
```

### 添加代码签名

需要配置 Apple Developer 证书：

```yaml
- name: Import certificates
  uses: apple-actions/import-codesign-certs@v2
  with:
    p12-file-base64: ${{ secrets.CERTIFICATES_P12 }}
    p12-password: ${{ secrets.CERTIFICATES_PASSWORD }}
```

### 添加自动测试

在 Release 之前运行测试：

```yaml
- name: Run tests
  run: go test ./...
```

## 📞 获取帮助

如有问题，请：
- 查看 GitHub Actions 日志
- 提交 Issue
- 查阅 [Wails 文档](https://wails.io)
