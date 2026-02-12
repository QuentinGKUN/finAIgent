import fetch from 'node-fetch';
import { resolveCompanyCN, isLikelyCNCompany } from './companyCN.js';

/**
 * 统一公司解析接口，自动识别美股和A股
 * @param {Object} options
 * @param {string} options.query - 公司名称或代码
 * @param {string} options.goUrl - Go服务URL（用于美股）
 * @param {string} options.secUserAgent - SEC User-Agent
 * @param {string} options.tushareToken - Tushare API token（可选，用于A股）
 * @param {string} options.tushareApiUrl - Tushare API URL（可选，用于A股）
 * @returns {Promise<Object>} {name, cik, code, tickers, market, source}
 */
export async function resolveCompany({goUrl, secUserAgent, query, tushareToken, tushareApiUrl}){
  if(!query) return {name:'未知公司', cik:'', code:'', tickers:[], market:'', source:''};

  // 判断是否可能是中国公司
  const isCN = isLikelyCNCompany(query);
  
  if(isCN){
    // 尝试解析A股
    const cnResult = await resolveCompanyCN({query, tushareToken, tushareApiUrl});
    if(cnResult){
      return {
        name: cnResult.name,
        cik: '', // A股没有CIK
        code: cnResult.code,
        tickers: [cnResult.code],
        market: cnResult.market,
        source: cnResult.source || 'cn_local'
      };
    }
  }

  // 尝试解析美股
  try {
    const url = `${goUrl}/sec/resolve?query=${encodeURIComponent(query)}`;
    const resp = await fetch(url, {headers:{'User-Agent':secUserAgent}});
    if(resp.ok){
      const usResult = await resp.json();
      if(usResult.cik && usResult.cik.length > 0){
        return {
          name: usResult.name,
          cik: usResult.cik,
          code: '',
          tickers: usResult.tickers || [],
          market: 'US',
          source: 'sec_api'
        };
      }
    }
  }catch(e){
    console.warn("美股解析失败:", e.message);
  }

  // 如果都不匹配，返回默认值
  return {
    name: query,
    cik: '',
    code: '',
    tickers: [],
    market: '',
    source: ''
  };
}
