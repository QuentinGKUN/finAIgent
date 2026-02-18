import fetch from 'node-fetch';

/**
 * EODHD 数据服务（美股补充：市值/行业/部分财务摘要）
 * Docs: https://eodhd.com/
 */

function mustKey(apiKey){
  if(!apiKey || !String(apiKey).trim()){
    throw new Error('EODHD_API_KEY 未配置');
  }
}

/**
 * 获取公司 fundamentals（包含 General 与 Financials）
 * symbol 例如：AAPL.US / TSLA.US
 */
export async function getFundamentalsEODHD({baseUrl, apiKey, symbol}){
  mustKey(apiKey);
  const url = `${baseUrl.replace(/\/$/, '')}/fundamentals/${encodeURIComponent(symbol)}?api_token=${encodeURIComponent(apiKey)}&fmt=json`;
  const res = await fetch(url);
  const json = await res.json();
  if(!res.ok){
    throw new Error(`EODHD fundamentals error: ${res.status} ${JSON.stringify(json).slice(0,200)}`);
  }
  return {url, json};
}

/**
 * 从 fundamentals 里提取行业/市值等通用字段
 */
export function pickGeneralFromFundamentals(fundamentalsJson){
  const g = fundamentalsJson?.General || {};
  return {
    name: g?.Name || '',
    exchange: g?.Exchange || '',
    sector: g?.Sector || '',
    industry: g?.Industry || '',
    marketCap: toNum(g?.MarketCapitalization),
    currency: g?.CurrencyCode || ''
  };
}

function toNum(v){
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}
