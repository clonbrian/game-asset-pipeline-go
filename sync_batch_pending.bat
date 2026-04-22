@echo off
cd /d "%~dp0"
call .\set_env.bat
game-asset-pipeline.exe sync-batch-pending -config ./config.json
echo.
pause