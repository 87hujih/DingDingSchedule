@echo off
setlocal

echo ========================================
echo Schedule Server Windows 源码部署入口
echo ========================================
echo.
echo 当前脚本会调用 one-click-deploy.sh，
echo 使用“本地打源码包 -> 上传服务器 -> 服务器本地构建”的旧部署方式。
echo.

where bash >nul 2>nul
if %errorlevel% neq 0 (
    echo 错误: 未找到 Git Bash，请先安装 Git for Windows
    echo 下载地址: https://git-scm.com/download/win
    pause
    exit /b 1
)

set SCRIPT_DIR=%~dp0
pushd "%SCRIPT_DIR%"
bash -lc "./one-click-deploy.sh"
set EXIT_CODE=%errorlevel%
popd

if %EXIT_CODE% neq 0 (
    echo.
    echo 应急部署失败，请检查 one-click-deploy.sh 输出。
    pause
    exit /b %EXIT_CODE%
)

echo.
echo 应急部署完成。
pause
