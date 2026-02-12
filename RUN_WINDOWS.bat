@echo off
chcp 65001 >nul
setlocal

echo [1/3] 启动 Go 服务...
start "go-service" cmd /k "cd go-service && go run ."

echo [2/3] 启动 Express...
start "server" cmd /k "cd server && npm i && copy .env.example .env && echo 请编辑 server\.env 设置 SEC_USER_AGENT && npm run start"

echo [3/3] 启动 Web 静态服务...
start "web" cmd /k "cd web && python -m http.server 5173"

echo.
echo ✅ 打开浏览器 http://localhost:5173
echo 右上角设置 Gemini Key，然后提问；可导出投研PDF（Ctrl+P 保存为PDF）
pause
