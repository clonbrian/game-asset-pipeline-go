@echo off
cd /d "%~dp0"
call .\set_env.bat
game-asset-pipeline.exe batch-gemini -config ./config.json
echo.
pause