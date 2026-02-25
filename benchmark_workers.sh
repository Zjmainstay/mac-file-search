#!/bin/bash

TEST_PATH="/Users/macbok"
WORKERS=(8 16 32 64 128)

echo "=== Worker 性能测试 ==="
echo "测试路径: $TEST_PATH"
printf "%-10s %-15s %-20s\n" "Workers" "用时(秒)" "速度(文件/秒)"
echo "---------------------------------------------------"

for workers in "${WORKERS[@]}"; do
    output=$(./mac-file-scan -path "$TEST_PATH" -workers $workers -exclude /Volumes/MacExtDisk 2>&1)
    
    # 提取用时
    time_str=$(echo "$output" | grep "⏱️  用时:" | awk '{print $3}')
    if [[ $time_str =~ ([0-9]+)m([0-9.]+)s ]]; then
        minutes=${BASH_REMATCH[1]}
        seconds=${BASH_REMATCH[2]}
        time_seconds=$(echo "$minutes * 60 + $seconds" | bc)
    else
        time_seconds=$(echo "$time_str" | sed 's/s$//')
    fi
    
    # 提取速度
    speed=$(echo "$output" | grep "⚡ 平均速度:" | awk '{print $3}' | tr -d ',')
    
    printf "%-10s %-15s %-20s\n" "$workers" "$time_seconds" "$speed"
done

echo "---------------------------------------------------"
echo "✅ 测试完成"
