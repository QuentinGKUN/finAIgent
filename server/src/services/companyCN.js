import fs from "fs";
import path from "path";
import fetch from "node-fetch";
import { config } from "../config.js";

/**
 * 加载本地A股公司列表JSON文件
 * 格式：{"stocks": [{"code": "300750.SZ", "name": "宁德时代", "market": "SZ"}, ...]}
 */
function loadLocalStockList() {
  const p = path.resolve(process.cwd(), "../../data/cn_stocks.json");
  if (!fs.existsSync(p)) {
    return null;
  }
  try {
    const text = fs.readFileSync(p, "utf-8");
    return JSON.parse(text);
  } catch (e) {
    console.warn("加载本地A股列表失败:", e.message);
    return null;
  }
}

/**
 * 从本地JSON文件解析公司
 */
function resolveFromLocal(query) {
  const data = loadLocalStockList();
  if (!data || !data.stocks || !Array.isArray(data.stocks)) {
    return null;
  }

  const q = (query || "").trim().toUpperCase();
  if (!q) return null;

  let bestMatch = null;
  let bestScore = -1;

  for (const stock of data.stocks) {
    const code = (stock.code || "").toUpperCase();
    const name = (stock.name || "").toUpperCase();
    const nameShort = name.replace(/股份有限(公司)?|有限公司/g, "").trim();

    let score = 0;
    // 精确匹配股票代码
    if (code === q || code.replace(/\.(SZ|SH)$/, "") === q) {
      score = 100;
    }
    // 精确匹配公司名称
    else if (name === q) {
      score = 90;
    }
    // 包含公司名称
    else if (name.includes(q)) {
      score = 50;
    }
    // 包含简称
    else if (nameShort.includes(q)) {
      score = 40;
    }
    // 包含查询词
    else if (q.includes(nameShort) || nameShort.includes(q)) {
      score = 30;
    }

    if (score > bestScore) {
      bestScore = score;
      bestMatch = stock;
    }
  }

  if (bestMatch && bestScore >= 30) {
    return {
      name: bestMatch.name,
      code: bestMatch.code,
      market: bestMatch.market || (bestMatch.code.includes(".SZ") ? "SZ" : "SH"),
      source: "local_json"
    };
  }

  return null;
}

/**
 * 调用Tushare API
 */
async function callTushareAPI(apiName, params, apiToken, apiUrl) {
  const url = apiUrl || config.tushare.apiUrl;
  const token = apiToken || config.tushare.token;
  
  if (!token) {
    throw new Error("Tushare token未配置");
  }

  const requestParams = {
    api_name: apiName,
    token: token,
    params: params
  };

  const resp = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(requestParams)
  });

  if (!resp.ok) {
    throw new Error(`Tushare API调用失败: ${resp.status} ${await resp.text()}`);
  }

  const data = await resp.json();
  if (data.code !== 0) {
    throw new Error(`Tushare API错误: ${data.msg || '未知错误'}`);
  }

  return data.data;
}

/**
 * 通过Tushare API解析（需要token）
 */
async function resolveFromTushare(query, apiToken, apiUrl) {
  const token = apiToken || config.tushare.token;
  if (!token) return null;

  try {
    const url = apiUrl || config.tushare.apiUrl;
    
    // Tushare股票列表接口
    const data = await callTushareAPI("stock_basic", {
      exchange: "",
      list_status: "L",
      fields: "ts_code,symbol,name,area,industry,market"
    }, token, url);

    if (!data || !data.items) return null;

    const q = (query || "").trim().toUpperCase();
    const stocks = data.items || [];

    for (const item of stocks) {
      const code = (item.ts_code || "").toUpperCase();
      const name = (item.name || "").toUpperCase();
      const symbol = (item.symbol || "").toUpperCase();

      if (
        code === q ||
        symbol === q ||
        name === q ||
        name.includes(q) ||
        q.includes(name)
      ) {
        return {
          name: item.name,
          code: code,
          market: code.includes(".SZ") ? "SZ" : "SH",
          source: "tushare_api"
        };
      }
    }
  } catch (e) {
    console.warn("Tushare API调用失败:", e.message);
  }

  return null;
}

/**
 * 解析中国A股公司
 * @param {Object} options
 * @param {string} options.query - 公司名称或股票代码
 * @param {string} options.tushareToken - Tushare API token（可选）
 * @returns {Promise<Object>} {name, code, market, source} 或 null
 */
export async function resolveCompanyCN({ query, tushareToken, tushareApiUrl } = {}) {
  if (!query) return null;

  // 优先使用本地JSON
  const localResult = resolveFromLocal(query);
  if (localResult) return localResult;

  // 如果本地没有，尝试Tushare API
  const token = tushareToken || config.tushare.token;
  const apiUrl = tushareApiUrl || config.tushare.apiUrl;
  
  if (token) {
    const apiResult = await resolveFromTushare(query, token, apiUrl);
    if (apiResult) return apiResult;
  }

  return null;
}

/**
 * 判断查询是否可能是中国公司
 * @param {string} query - 查询字符串
 * @returns {boolean}
 */
export function isLikelyCNCompany(query) {
  if (!query) return false;
  const q = query.trim();
  
  // 包含中文字符
  if (/[\u4e00-\u9fa5]/.test(q)) return true;
  
  // 包含A股市场标识
  if (/\.(SZ|SH)$/i.test(q)) return true;
  
  // 6位数字代码（A股代码通常是6位）
  if (/^\d{6}$/.test(q)) return true;
  
  return false;
}
