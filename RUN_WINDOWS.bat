@echo off
chcp 65001 >nul
setlocal
set HTTP_PROXY=
set HTTPS_PROXY=
set ALL_PROXY=
set NO_PROXY=*
set http_proxy=
set https_proxy=
set all_proxy=
set no_proxy=*
set VALUECELL_API_URL=http://127.0.0.1:8010/api/v1
set VALUECELL_AGENT_NAME=
set PYTHON_EXE=C:\tools\python\python.exe
if not exist "%PYTHON_EXE%" set PYTHON_EXE=python
if exist "server\.env" (
  for /f "usebackq tokens=1,* delims==" %%A in ("server\.env") do (
    if /I "%%A"=="GLM_KEY" if not defined GLM_KEY set "GLM_KEY=%%B"
    if /I "%%A"=="GLM_API_URL" if not defined GLM_API_URL set "GLM_API_URL=%%B"
    if /I "%%A"=="LLM_MODEL" if not defined LLM_MODEL set "LLM_MODEL=%%B"
  )
)
if not defined GLM_API_URL set "GLM_API_URL=https://open.bigmodel.cn/api/paas/v4/chat/completions"
if not defined LLM_MODEL set "LLM_MODEL=glm-4-flash"
set "GLM_BASE_URL=%GLM_API_URL:/chat/completions=%"
if not defined OPENAI_COMPATIBLE_API_KEY if defined GLM_KEY set "OPENAI_COMPATIBLE_API_KEY=%GLM_KEY%"
if not defined OPENAI_COMPATIBLE_BASE_URL set "OPENAI_COMPATIBLE_BASE_URL=%GLM_BASE_URL%"
if not defined PRIMARY_PROVIDER set "PRIMARY_PROVIDER=openai-compatible"
if not defined SUPER_AGENT_PROVIDER set "SUPER_AGENT_PROVIDER=openai-compatible"
if not defined RESEARCH_AGENT_PROVIDER set "RESEARCH_AGENT_PROVIDER=openai-compatible"
if not defined PLANNER_MODEL_ID set "PLANNER_MODEL_ID=%LLM_MODEL%"
if not defined SUPER_AGENT_MODEL_ID set "SUPER_AGENT_MODEL_ID=%LLM_MODEL%"
if not defined RESEARCH_AGENT_MODEL_ID set "RESEARCH_AGENT_MODEL_ID=%LLM_MODEL%"

echo [1/3] 启动 Go 后端服务（优先 EXE，不依赖 go 命令）...
if exist "go-service\finassistant_go.exe" (
  start "go-service" cmd /k "cd /d go-service && set VALUECELL_API_URL=%VALUECELL_API_URL% && set VALUECELL_AGENT_NAME=%VALUECELL_AGENT_NAME% && finassistant_go.exe"
) else (
  start "go-service" cmd /k "cd /d go-service && set VALUECELL_API_URL=%VALUECELL_API_URL% && set VALUECELL_AGENT_NAME=%VALUECELL_AGENT_NAME% && go run ."
)

echo [2/3] 启动 ValueCell（8010，若未安装依赖会在该窗口报错）...
if exist "_tmp_valuecell\python\valuecell\server\main.py" (
  start "valuecell-8010" cmd /k "cd /d _tmp_valuecell\python && set ENV=local_dev && set API_HOST=127.0.0.1 && set API_PORT=8010 && %PYTHON_EXE% -m valuecell.server.main"
) else (
  echo 未检测到 _tmp_valuecell，跳过 ValueCell 启动。
)

echo [3/3] 启动 React 前端...
start "web" cmd /k "cd web && npm i && npm run dev"

echo.
echo ✅ 打开浏览器 http://localhost:5173
echo API Key 请在 server\.env 配置，然后提问；可导出投研PDF（Ctrl+P 保存为PDF）
pause
