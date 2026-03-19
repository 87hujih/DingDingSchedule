@echo off
setlocal

echo ========================================
echo Schedule Server Windows 应急部署入口
echo ========================================
echo.
echo 正式发布请优先使用 GitHub Actions。
echo 当前脚本仅作为 one-click-deploy.sh 的 Windows 包装器。
echo.

where bash >nul 2>nul
if %errorlevel% neq 0 (
    echo 错误: 未找到 Git Bash，请先安装 Git for Windows
    echo 下载地址: https://git-scm.com/download/win
    pause
    exit /b 1
)

set SCRIPT_DIR=%~dp0
set IMAGE_TAG=%~1
if "%IMAGE_TAG%"=="" set IMAGE_TAG=latest

pushd "%SCRIPT_DIR%"
bash -lc "./one-click-deploy.sh %IMAGE_TAG%"
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
