#!/bin/bash

# 从扫描结果文件重建文件树
# 用法: ./build-tree.sh scan-result.jsonl [max-depth]

if [ $# -lt 1 ]; then
    echo "用法: $0 <scan-result.jsonl> [max-depth]"
    echo "示例: $0 /tmp/disk-scan.jsonl 3"
    exit 1
fi

SCAN_FILE="$1"
MAX_DEPTH="${2:-0}"  # 默认不限制深度

if [ ! -f "$SCAN_FILE" ]; then
    echo "错误: 文件不存在: $SCAN_FILE"
    exit 1
fi

echo "========================================"
echo "从扫描结果重建文件树"
echo "========================================"
echo "输入文件: $SCAN_FILE"
echo "最大深度: ${MAX_DEPTH} (0表示不限制)"
echo ""

# 统计信息
echo "统计信息:"
TOTAL_LINES=$(wc -l < "$SCAN_FILE")
DATA_LINES=$(grep -v '^#' "$SCAN_FILE" | wc -l)
DIR_COUNT=$(grep -v '^#' "$SCAN_FILE" | jq -r 'select(.is_dir==true)' | wc -l)
FILE_COUNT=$(grep -v '^#' "$SCAN_FILE" | jq -r 'select(.is_dir==false)' | wc -l)
TOTAL_SIZE=$(grep -v '^#' "$SCAN_FILE" | jq -s 'map(select(.is_dir==false) | .size) | add // 0')

echo "  总行数: $TOTAL_LINES"
echo "  数据行: $DATA_LINES"
echo "  目录数: $DIR_COUNT"
echo "  文件数: $FILE_COUNT"

# 格式化大小
if [ "$TOTAL_SIZE" -ge 1099511627776 ]; then
    SIZE_STR=$(echo "scale=2; $TOTAL_SIZE / 1099511627776" | bc)" TB"
elif [ "$TOTAL_SIZE" -ge 1073741824 ]; then
    SIZE_STR=$(echo "scale=2; $TOTAL_SIZE / 1073741824" | bc)" GB"
elif [ "$TOTAL_SIZE" -ge 1048576 ]; then
    SIZE_STR=$(echo "scale=2; $TOTAL_SIZE / 1048576" | bc)" MB"
elif [ "$TOTAL_SIZE" -ge 1024 ]; then
    SIZE_STR=$(echo "scale=2; $TOTAL_SIZE / 1024" | bc)" KB"
else
    SIZE_STR="$TOTAL_SIZE B"
fi

echo "  总大小: $SIZE_STR"
echo ""

# 查找最大的10个文件
echo "最大的10个文件:"
grep -v '^#' "$SCAN_FILE" | \
    jq -r 'select(.is_dir==false) | "\(.size)\t\(.path)"' | \
    sort -rn | \
    head -10 | \
    while IFS=$'\t' read -r size path; do
        if [ "$size" -ge 1073741824 ]; then
            size_str=$(echo "scale=2; $size / 1073741824" | bc)" GB"
        elif [ "$size" -ge 1048576 ]; then
            size_str=$(echo "scale=2; $size / 1048576" | bc)" MB"
        elif [ "$size" -ge 1024 ]; then
            size_str=$(echo "scale=2; $size / 1024" | bc)" KB"
        else
            size_str="$size B"
        fi
        printf "  %10s  %s\n" "$size_str" "$path"
    done

echo ""
echo "========================================"
echo "提示:"
echo "- 所有扫描数据已保存在文件中"
echo "- 即使扫描中断，数据也不会丢失"
echo "- 可以使用 jq 进一步分析数据"
echo "========================================"
