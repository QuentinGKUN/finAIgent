# 财务分析智能助手（冠军版）
Go + Express + SQLite + Gemini  
能力：年报RAG（FTS5+Embedding重排）+ SEC财务时间序列 + 派生指标 + 多轮对话 + 引用溯源 + 投研报告导出（打印保存PDF）+ **A股支持**

## 核心功能

### 1. 智能公司识别
- **自动识别公司名称**：系统能够从用户问题中自动提取公司名称
- **支持美股和A股**：
  - 美股：通过SEC API自动识别（如Tesla、Google、AAPL）
  - A股：通过本地JSON或Tushare API识别（如宁德时代、300750.SZ）

### 2. 多数据源支持
- **美股数据源**：
  - 年报文档：SEC EDGAR（10-K/20-F）
  - 财务数据：SEC Company Facts API（XBRL数据）
- **A股数据源**：
  - 财务数据：本地JSON文件或Tushare API
  - 公司列表：本地JSON文件或Tushare API

### 3. 智能数据路由
- 系统能够理解问题的含义，自动判断需要从哪些数据源获取信息
- 支持多轮对话，可以基于之前的答案进行后续提问

### 4. 数据引用和来源说明
- 所有答案都包含数据引用和来源说明
- 支持引用格式：[D1]年报片段、[F1]财务数据、[R1]榜单数据

## 环境
- Node.js 18+
- Go 1.22+
- Python（用于启动静态网页服务，可用系统自带或安装）

## 启动（Windows）
1) 启动 Go 服务
```bash
cd go-service
go mod tidy
go run .
```

2) 启动 Express
```bash
cd ../server
npm i
# 创建配置文件（参考 ENV_EXAMPLE.txt）
# Windows: copy ENV_EXAMPLE.txt .env
# Linux/Mac: cp ENV_EXAMPLE.txt .env
# 然后编辑 .env 文件，填写以下配置：
#   - GLM_KEY: 你的Gemini API Key
#   - TUSHARE_TOKEN: 你的Tushare Token（可选，如果使用本地JSON文件则不需要）
#   - TUSHARE_API_URL: Tushare API地址（默认使用官方，或使用自定义代理）
#   - SEC_USER_AGENT: 你的邮箱/联系方式（必须设置）
npm run start
```

3) 启动前端静态服务
```bash
cd ../web
python -m http.server 5173
```

浏览器打开：http://localhost:5173
右上角【设置】填写 Gemini API Key

## 数据文件配置

### A股公司列表（data/cn_stocks.json）
包含A股公司的股票代码和名称。可以从以下来源获取：
- Tushare API：`stock_basic`接口
- Baostock API：`query_all_stock`接口
- 公开数据源下载

### A股财务数据（data/cn_finance_data.json）
包含A股公司的财务指标数据。可以从以下来源获取：
- Tushare API：`income`、`balancesheet`等接口
- Baostock API：`query_profit_data`、`query_balance_data`等接口
- 公司年报：从巨潮资讯、公司官网等获取

详细格式说明请参考 `data/README.md`

## 演示题（答辩）

### 美股示例
- Tesla 的主要业务风险是什么？
- Google 过去三年的收入同比（YoY）如何？并给出三年CAGR。
- 结合年报提到的风险，分析 Tesla 过去三年的研发投入占比是否有效？

### A股示例
- 宁德时代过去三年的营收情况如何？
- 贵州茅台的毛利率趋势如何？
- 300750.SZ 的研发投入占比是多少？

## 投研报告导出
完成一次回答后点右上角【导出投研PDF】→ 新页面 Ctrl+P → 另存为 PDF
