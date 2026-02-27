import os 
for k in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY','http_proxy','https_proxy','all_proxy','no_proxy']: os.environ.pop(k,None) 
os.environ['NO_PROXY']='*' 
import akshare as ak 
df=ak.stock_zh_a_spot() 
print(df.head(10).to_dict(orient='records')) 
