import tushare as ts
#tushare版本 1.4.24
token = "fb21391f810bee022b33e8907850afe18ee68833bd3e1f897f16c3224094"

pro = ts.pro_api(token)

pro._DataApi__token = token # 保证有这个代码，不然不可以获取
pro._DataApi__http_url = 'http://lianghua.nanyangqiankun.top'  # 保证有这个代码，不然不可以获取

# #  正常使用（与官方API完全一致）
df = pro.stock_basic(exchange='', list_status='L', fields='ts_code,symbol,name,area,industry,list_date')

# 将数据保存为 JSON 文件
df.to_json('stock_basic_data.json', orient='records', force_ascii=False, indent=2)

print(f"数据已保存到 stock_basic_data.json，共 {len(df)} 条记录")
print(df.head())
