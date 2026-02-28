@echo off
chcp 65001 >nul
setlocal EnableExtensions

set "PYTHON_EXE=C:\tools\python\python.exe"
if not exist "%PYTHON_EXE%" set "PYTHON_EXE=python"

echo [1/2] Starting Go + ValueCell backend services...
"%PYTHON_EXE%" _start_local_debug.py
if errorlevel 1 (
  echo [ERROR] backend startup failed. Check _go_backend.log and _valuecell.log
  pause
  exit /b 1
)

echo [2/2] Starting React web...
start "web" cmd /k "cd /d web && npm i && npm run dev"

echo.
echo Open browser: http://localhost:5180
echo Logs: _go_backend.log and _valuecell.log
pause
