#!/bin/bash

# 构建应用
echo "正在构建应用..."
make app

# 检查构建是否成功
if [ $? -ne 0 ]; then
    echo "❌ 构建失败！"
    exit 1
fi

echo "✅ 构建成功！"

# 关闭正在运行的应用
echo "正在关闭 mac-search-app..."
pkill -f "mac-search-app.app" 2>/dev/null
killall -9 "mac-search-app" 2>/dev/null

# 等待进程完全退出
sleep 1

# 重新打开应用
echo "正在启动 mac-search-app..."
open mac-search-app/build/bin/mac-search-app.app

echo "✅ 应用已重启"
