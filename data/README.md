# 数据文件说明

## cn_stocks.json
A股公司列表，包含股票代码、公司名称和市场标识。

**格式：**
```json
{
  "stocks": [
    {
      "code": "300750.SZ",
      "name": "宁德时代",
      "market": "SZ"
    }
  ]
}
```

**数据来源：**
- 可以从Tushare API获取：`stock_basic`接口
- 可以从Baostock API获取：`query_all_stock`接口
- 可以从公开数据源下载完整列表

## cn_finance_data.json
A股公司财务数据，包含各年度的财务指标。

**格式：**
```json
{
  "300750.SZ": {
    "name": "宁德时代",
    "data": [
      {
        "year": 2023,
        "revenue": 400917000000,
        "gross_profit": 100000000000,
        "operating_income": 44121000000,
        "net_income": 44121000000,
        "rd": 18356000000,
        "unit": "CNY"
      }
    ]
  }
}
```

**支持的指标：**
- `revenue`: 营业收入
- `gross_profit`: 毛利润
- `operating_income`: 营业利润
- `net_income`: 净利润
- `rd`: 研发费用
- `total_assets`: 总资产
- `total_liabilities`: 总负债

**数据来源：**
- Tushare API：`income`、`balancesheet`等接口
- Baostock API：`query_profit_data`、`query_balance_data`等接口
- 公司年报：从巨潮资讯、公司官网等获取

## 使用Tushare API（可选）

如果你有Tushare API token，可以在`.env`文件中设置：
```
TUSHARE_TOKEN=your_tushare_token
```

系统会优先使用本地JSON文件，如果本地没有数据，会自动调用Tushare API获取。
