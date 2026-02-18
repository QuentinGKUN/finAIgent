# Tushare API集成说明

## 概述

系统已完整集成Tushare API，支持通过配置文件统一管理所有API密钥和设置。当用户查询A股公司财务数据时，系统会自动调用相应的Tushare接口获取专业财务数据。

## 配置管理

### 统一配置模块

所有配置项统一在 `server/src/config.js` 中管理，从环境变量读取：

```javascript
import { config } from './config.js';

// 使用配置
const token = config.tushare.token;
const apiUrl = config.tushare.apiUrl;
```

### 配置文件

配置文件位置：`server/.env`

参考文件：`server/ENV_EXAMPLE.txt`

### 必需配置项

1. **GLM_KEY**: 智谱GLM API Key（AI对话）
2. **SEC_USER_AGENT**: SEC API User-Agent（美股数据）
3. **TUSHARE_TOKEN**: Tushare API Token（A股数据，可选）
4. **TUSHARE_API_URL**: Tushare API地址（可选，支持自定义代理）

## Tushare接口集成

### 1. stock_basic（股票基本信息）

**用途**: 根据公司名称查找股票代码

**调用位置**: `server/src/services/companyCN.js`

**使用场景**: 
- 用户输入"宁德时代"，系统自动查找对应的股票代码"300750.SZ"
- 支持公司名称、股票代码、简称等多种查询方式

**示例**:
```javascript
const result = await resolveCompanyCN({ 
  query: "宁德时代",
  tushareToken: config.tushare.token,
  tushareApiUrl: config.tushare.apiUrl
});
```

### 2. income（利润表）

**用途**: 获取公司利润表数据

**调用位置**: `server/src/services/financeCN.js`

**获取字段**:
- `revenue`: 营业收入
- `operate_profit`: 营业利润
- `n_income`: 净利润
- `rd_exp`: 研发费用

**使用场景**:
- 查询公司营收情况
- 查询利润指标
- 查询研发投入

### 3. balancesheet（资产负债表）

**用途**: 获取公司资产负债表数据

**调用位置**: `server/src/services/financeCN.js`

**获取字段**:
- `total_assets`: 总资产
- `total_liab`: 总负债

**使用场景**:
- 查询公司资产规模
- 查询负债情况

### 4. cashflow（现金流量表）

**用途**: 获取公司现金流量表数据

**调用位置**: `server/src/services/financeCN.js`

**获取字段**:
- `oper_cash_flow`: 经营活动现金流

**使用场景**:
- 查询公司现金流情况

## 数据流程

### 用户查询A股公司财务数据时的流程：

1. **问题解析**: LLM解析用户问题，提取公司名称和查询指标
2. **公司识别**: 
   - 优先从本地JSON文件查找
   - 如果本地没有，调用Tushare `stock_basic`接口查找
3. **数据获取**:
   - 优先从本地JSON文件获取财务数据
   - 如果本地没有，根据查询指标调用相应Tushare接口：
     - 利润相关 → `income`接口
     - 资产相关 → `balancesheet`接口
     - 现金流相关 → `cashflow`接口
4. **数据整合**: 将多个接口的数据整合为统一格式
5. **派生指标计算**: 自动计算毛利率、研发占比、YoY、CAGR等
6. **AI回答**: 基于获取的数据生成专业财务分析回答

## 支持的财务指标

系统支持以下财务指标查询：

### 基础指标
- **Revenue**: 营业收入
- **GrossProfit**: 毛利润
- **OperatingIncome**: 营业利润
- **NetIncome**: 净利润
- **RAndD**: 研发费用
- **TotalAssets**: 总资产
- **TotalLiabilities**: 总负债
- **OperatingCashFlow**: 经营活动现金流

### 派生指标（自动计算）
- **GrossMargin**: 毛利率 = 毛利润 / 营业收入
- **RAndDRatio**: 研发费用占比 = 研发费用 / 营业收入
- **RevenueYoY**: 营收同比增长率
- **RevenueCAGR3Y**: 三年复合增长率

## 使用示例

### 示例1: 查询营收情况

**用户**: "宁德时代过去三年的营收情况如何？"

**系统处理**:
1. 识别公司：宁德时代 → 300750.SZ
2. 调用接口：`income`接口，获取revenue字段
3. 返回数据：2021-2023年营收数据
4. 生成回答：包含数据引用和趋势分析

### 示例2: 查询研发投入

**用户**: "300750.SZ的研发投入占比是多少？"

**系统处理**:
1. 识别公司：300750.SZ
2. 调用接口：`income`接口，获取revenue和rd_exp字段
3. 计算指标：研发费用占比 = rd_exp / revenue
4. 生成回答：包含计算结果和数据引用

### 示例3: 综合财务分析

**用户**: "分析宁德时代的盈利能力变化趋势"

**系统处理**:
1. 识别公司：宁德时代 → 300750.SZ
2. 调用多个接口：
   - `income`接口：获取营收、利润数据
   - `balancesheet`接口：获取资产数据
3. 计算派生指标：毛利率、净利率、ROE等
4. 生成回答：综合分析盈利能力变化

## 配置自定义API URL

如果你的Tushare Token需要通过代理服务器访问，可以在`.env`文件中设置：

```env
TUSHARE_API_URL=http://lianghua.nanyangqiankun.top
```

系统会自动使用自定义URL调用Tushare API。

## 错误处理

系统包含完善的错误处理机制：

1. **Token未配置**: 如果未配置Tushare Token，系统会优先使用本地JSON文件
2. **API调用失败**: 如果API调用失败，会记录警告日志，不影响其他功能
3. **数据缺失**: 如果某个指标数据缺失，会跳过该指标，不影响其他指标

## 性能优化

1. **本地优先**: 优先使用本地JSON文件，减少API调用
2. **批量获取**: 一次API调用获取多年数据
3. **缓存机制**: 公司列表和财务数据可以缓存到本地JSON文件

## 扩展接口

如果需要添加更多Tushare接口，可以在 `server/src/services/financeCN.js` 中添加：

```javascript
async function getNewInterfaceFromTushare(code, params, apiToken, apiUrl) {
  const data = await callTushareAPI("new_interface_name", params, apiToken, apiUrl);
  // 处理数据...
  return processedData;
}
```

然后在 `getFromTushare` 函数中调用新接口。

## 注意事项

1. **API限制**: Tushare API有调用频率限制，建议使用本地JSON文件作为主要数据源
2. **数据准确性**: API返回的数据以年报数据为准（end_date以1231结尾）
3. **单位统一**: 所有金额数据统一为CNY（人民币）单位
4. **年份过滤**: 系统会自动过滤指定年份的数据
