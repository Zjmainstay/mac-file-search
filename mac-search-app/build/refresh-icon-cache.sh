#!/bin/bash
# macOS 应用图标缓存清理（更换图标后仍显示旧图标时使用）
# 用法: 在终端执行 ./refresh-icon-cache.sh（部分步骤需要输入密码）

set -e
APP_PATH="/Volumes/MacExtDisk/mac-file-scan/mac-search-app/build/bin/Mac文件搜索.app"

echo "==> 1. 触碰 app 更新时间戳..."
touch "$APP_PATH"

echo "==> 2. 重启 Dock 和 Finder..."
killall Dock 2>/dev/null || true
killall Finder 2>/dev/null || true
sleep 1

echo "==> 3. 清除图标缓存（需要输入密码）..."
sudo rm -rf /Library/Caches/com.apple.iconservices.store 2>/dev/null || true
sudo find /private/var/folders -name "com.apple.dock.iconcache" -exec rm -rf {} \; 2>/dev/null || true
sudo find /private/var/folders -name "com.apple.iconservices" -type d -exec rm -rf {} \; 2>/dev/null || true

echo "==> 4. 再次重启 Dock..."
killall Dock 2>/dev/null || true

echo "✓ 完成。若仍显示旧图标："
echo "  - 从 Dock 中移除「Mac文件搜索」图标，再重新从 build/bin 拖入 Dock"
echo "  - 或完全退出应用后重新打开一次"
