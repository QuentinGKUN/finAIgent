# 配置文件说明

## 配置文件位置

配置文件位于 `server/.env`，首次使用需要从 `server/ENV_EXAMPLE.txt` 复制并填写实际值。

## 配置项说明

### AI配置

#### GLM_KEY
- **说明**: Gemini API Key，用于AI对话和任务路由
- **必需**: 是
- **示例**: `GLM_KEY=AIzaSyXXXXXXXXXXXXXXXXXXXXXXXXXXXXX`

### Tushare配置

#### TUSHARE_TOKEN
- **说明**: Tushare API Token，用于获取A股财务数据
- **必需**: 否（如果使用本地JSON文件则不需要）
- **获取方式**: 注册 [Tushare Pro](https://tushare.pro/) 账号获取
- **示例**: `TUSHARE_TOKEN=fb21391f810bee022b33e8907850afe18ee68833bd3e1f897f16c3224094`

#### TUSHARE_API_URL
- **说明**: Tushare API地址，支持自定义代理服务器
- **必需**: 否（默认使用官方API）
- **默认值**: `http://api.tushare.pro`
- **自定义示例**: `TUSHARE_API_URL=http://lianghua.nanyangqiankun.top`

### SEC配置（美股数据）

#### SEC_USER_AGENT
- **说明**: SEC API User-Agent，必须设置，建议使用你的邮箱地址
- **必需**: 是
- **格式**: `FinAssistantChampion/1.0 (email: your_email@example.com)`
- **示例**: `SEC_USER_AGENT=FinAssistantChampion/1.0 (email: user@example.com)`

### 服务配置

#### PORT
- **说明**: Express服务端口
- **必需**: 否
- **默认值**: `3000`

#### GO_SERVICE_URL
- **说明**: Go服务URL（美股数据服务）
- **必需**: 否
- **默认值**: `http://localhost:3001`

#### GO_PORT
- **说明**: Go服务端口
- **必需**: 否
- **默认值**: `3001`

#### SQLITE_PATH
- **说明**: SQLite数据库路径
- **必需**: 否
- **默认值**: `../data/app.db`

### 环境配置

#### NODE_ENV
- **说明**: 运行环境
- **必需**: 否
- **可选值**: `development`（开发）或 `production`（生产）
- **默认值**: `development`

## 配置示例

### 最小配置（仅必需项）

```env
GLM_KEY=your_gemini_api_key_here
SEC_USER_AGENT=FinAssistantChampion/1.0 (email: your_email@example.com)
```

### 完整配置（包含Tushare）

```env
# AI配置
GLM_KEY=your_gemini_api_key_here

# Tushare配置
TUSHARE_TOKEN=your_tushare_token_here
TUSHARE_API_URL=http://lianghua.nanyangqiankun.top

# SEC配置
SEC_USER_AGENT=FinAssistantChampion/1.0 (email: your_email@example.com)

# 服务配置
PORT=3000
GO_SERVICE_URL=http://localhost:3001
GO_PORT=3001
SQLITE_PATH=../data/app.db

# 环境
NODE_ENV=development
```

## Tushare API接口说明

系统已集成以下Tushare接口：

### 1. stock_basic（股票基本信息）
- **用途**: 获取A股公司列表和基本信息
- **调用场景**: 用户查询公司名称时，自动匹配股票代码

### 2. income（利润表）
- **用途**: 获取公司利润表数据
- **字段**: 营业收入(revenue)、营业利润(operate_profit)、净利润(n_income)、研发费用(rd_exp)
- **调用场景**: 查询营收、利润、研发费用等指标

### 3. balancesheet（资产负债表）
- **用途**: 获取公司资产负债表数据
- **字段**: 总资产(total_assets)、总负债(total_liab)
- **调用场景**: 查询资产、负债等指标

### 4. cashflow（现金流量表）
- **用途**: 获取公司现金流量表数据
- **字段**: 经营活动现金流(oper_cash_flow)
- **调用场景**: 查询现金流相关指标

## 配置验证

启动服务时，系统会自动验证配置：
- 如果缺少必需配置项，会在控制台显示错误提示
- SEC_USER_AGENT未正确设置时会显示警告

## 注意事项

1. **敏感信息**: `.env` 文件包含敏感信息，不要提交到版本控制系统
2. **Tushare Token**: 如果使用本地JSON文件，可以不配置Tushare Token
3. **自定义API URL**: 如果使用Tushare代理服务器，需要设置 `TUSHARE_API_URL`
4. **SEC User-Agent**: 必须设置有效的User-Agent，否则SEC API可能拒绝请求

## 故障排查

### 问题1: Tushare API调用失败
- 检查 `TUSHARE_TOKEN` 是否正确设置
- 检查 `TUSHARE_API_URL` 是否可访问
- 查看控制台错误信息

### 问题2: SEC API调用失败
- 检查 `SEC_USER_AGENT` 是否设置且格式正确
- 检查网络连接是否正常

### 问题3: AI对话失败
- 检查 `GLM_KEY` 是否正确设置
- 检查API Key是否有效且有足够额度
