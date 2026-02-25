#!/bin/bash

# Mac File Scan - 使用示例

echo "========================================"
echo "Mac File Scan - 使用示例"
echo "========================================"
echo ""

echo "1. 扫描当前目录："
echo "   ./file-scan"
echo ""

echo "2. 扫描指定目录并显示完整文件树："
echo "   ./file-scan -path /Users/username/Documents -tree"
echo ""

echo "3. 扫描指定目录并限制显示深度："
echo "   ./file-scan -path /Users/username/Documents -tree -depth 3"
echo ""

echo "4. 查找大文件（大于100MB）："
echo "   ./file-scan -path /Users/username -min 104857600"
echo ""

echo "5. 扫描并筛选1MB-100MB之间的文件："
echo "   ./file-scan -path /path/to/scan -min 1048576 -max 104857600"
echo ""

echo "6. 全盘扫描并显示完整树（需要sudo权限）："
echo "   sudo ./file-scan -path / -workers 32 -tree"
echo ""

echo "7. 自定义并发数和显示深度："
echo "   ./file-scan -path ~/projects -workers 8 -tree -depth 4"
echo ""

echo "========================================"
echo "提示："
echo "- 程序会实时显示扫描进度和速度"
echo "- 由于无法预知文件总数，不显示百分比进度"
echo "- 默认显示完整文件树，深度不限制"
echo "- 使用 -depth 参数可以限制显示深度，避免输出过多"
echo "- 使用 -min 参数过滤小文件，提高扫描效率"
echo "- 扫描大目录时可增加 -workers 数量"
echo "- 权限错误会被自动忽略，不影响扫描"
echo "- 使用 Ctrl+C 可随时中断扫描"
echo "========================================"
