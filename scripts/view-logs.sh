#!/bin/bash
# 日志查看辅助脚本

echo "=== 日志文件统计 ==="
echo "全量日志: $(wc -l < logs/app.log 2>/dev/null || echo 0) 行"
echo "错误日志: $(wc -l < logs/error.log 2>/dev/null || echo 0) 行"
echo ""

echo "=== 最近的错误和警告（来自 error.log）==="
if [ -f logs/error.log ]; then
    tail -10 logs/error.log | jq -r '[.time, .level, .msg] | @tsv' 2>/dev/null || tail -10 logs/error.log
else
    echo "error.log 文件不存在（需要重启服务后生成）"
fi
echo ""

echo "=== 最近10条日志（来自 app.log）==="
tail -10 logs/app.log | jq -r '[.time, .level, .msg] | @tsv' 2>/dev/null || tail -10 logs/app.log
