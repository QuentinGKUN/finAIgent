import os 
for k in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY','http_proxy','https_proxy','all_proxy','no_proxy']: os.environ.pop(k,None) 
os.environ['NO_PROXY']='*' 
import akshare as ak 
try: 
 df=ak.stock_zh_a_spot_em() 
 print(df.head(5).to_dict(orient='records')) 
except Exception as e: 
 print('ERR',repr(e)) 
