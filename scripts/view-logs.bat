@echo off
REM Windows 日志查看辅助脚本

echo === 日志文件统计 ===
for %%f in (logs\app.log) do echo 全量日志: %%~zf 字节
for %%f in (logs\error.log) do echo 错误日志: %%~zf 字节
echo.

echo === 最近的错误和警告（来自 error.log）===
if exist logs\error.log (
    powershell -Command "Get-Content logs\error.log -Tail 10 | ConvertFrom-Json | Select-Object time, level, msg | Format-Table -AutoSize"
) else (
    echo error.log 文件不存在（需要重启服务后生成）
)
echo.

echo === 最近10条日志（来自 app.log）===
powershell -Command "Get-Content logs\app.log -Tail 10 | ConvertFrom-Json | Select-Object time, level, msg | Format-Table -AutoSize"
