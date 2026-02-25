#!/bin/bash
# Mac File Search 自动安装脚本
# 双击此文件即可自动安装应用

set -e

# 获取脚本所在目录（ZIP 解压后的目录）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_PATH="$SCRIPT_DIR/Mac文件搜索.app"

echo "========================================="
echo "  Mac File Search 自动安装程序"
echo "========================================="
echo ""

# 检查应用是否存在
if [ ! -d "$APP_PATH" ]; then
    echo "❌ 错误: 找不到 Mac文件搜索.app"
    echo "   请确保 install.command 和 Mac文件搜索.app 在同一目录"
    echo ""
    read -p "按回车键退出..."
    exit 1
fi

echo "📍 找到应用: $APP_PATH"
echo ""

# 移除隔离属性
echo "🔓 正在移除 macOS Gatekeeper 隔离属性..."
xattr -cr "$APP_PATH"
echo "   ✅ 隔离属性已移除"
echo ""

# 安装到应用程序文件夹
echo "📂 正在安装到应用程序文件夹..."

if [ -d "/Applications/Mac文件搜索.app" ]; then
    echo "   ⚠️  应用已存在，正在替换..."
    rm -rf "/Applications/Mac文件搜索.app"
fi

cp -R "$APP_PATH" "/Applications/"
echo "   ✅ 已复制到 /Applications/Mac文件搜索.app"
echo ""

echo "========================================="
echo "  🎉 安装成功！"
echo "========================================="
echo ""
echo "现在可以："
echo "  • 从「启动台」找到「Mac文件搜索」"
echo "  • 或在「应用程序」文件夹打开"
echo ""
echo "提示: 本窗口将在 5 秒后自动关闭..."
sleep 5

# 可选：安装完成后打开应用
# open "/Applications/Mac文件搜索.app"
