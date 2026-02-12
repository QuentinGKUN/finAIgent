import tushare as ts
#tushare版本 1.4.24
token = "fb21391f810bee022b33e8907850afe18ee68833bd3e1f897f16c3224094"

pro = ts.pro_api(token)

pro._DataApi__token = token # 保证有这个代码，不然不可以获取
pro._DataApi__http_url = 'http://lianghua.nanyangqiankun.top'  # 保证有这个代码，不然不可以获取

# #  正常使用（与官方API完全一致）
df = pro.daily(ts_code='000001.SZ', start_date='20240101', end_date='20240131')


print(df)