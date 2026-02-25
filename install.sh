#!/bin/bash
# Mac File Search 安装脚本
# 自动解压、移除隔离属性、安装到应用程序文件夹

set -e

echo "🚀 Mac File Search 安装脚本"
echo ""

# 检查是否提供了 ZIP 文件参数
if [ $# -eq 0 ]; then
    echo "用法: ./install.sh <下载的-app.zip文件>"
    echo ""
    echo "示例:"
    echo "  ./install.sh ~/Downloads/mac-file-search-v1.0.2-app.zip"
    exit 1
fi

ZIP_FILE="$1"

# 检查文件是否存在
if [ ! -f "$ZIP_FILE" ]; then
    echo "❌ 错误: 文件不存在: $ZIP_FILE"
    exit 1
fi

echo "📦 正在解压: $ZIP_FILE"
TEMP_DIR=$(mktemp -d)
unzip -q "$ZIP_FILE" -d "$TEMP_DIR"

echo "🔓 正在移除隔离属性..."
xattr -cr "$TEMP_DIR/Mac文件搜索.app"

echo "📂 正在安装到应用程序文件夹..."
if [ -d "/Applications/Mac文件搜索.app" ]; then
    echo "⚠️  应用已存在，正在替换..."
    rm -rf "/Applications/Mac文件搜索.app"
fi

mv "$TEMP_DIR/Mac文件搜索.app" "/Applications/"
rm -rf "$TEMP_DIR"

echo ""
echo "✅ 安装成功！"
echo ""
echo "🎉 现在可以从「启动台」或「应用程序」文件夹打开「Mac文件搜索」了"
echo ""
