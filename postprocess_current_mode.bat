@echo off
cd /d "%~dp0"
game-asset-pipeline.exe postprocess-current-mode -config ./config.json
echo.
pause