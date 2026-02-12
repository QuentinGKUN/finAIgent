import fs from "fs";
import path from "path";
import fetch from "node-fetch";
import { config } from "../config.js";

/**
 * 从本地JSON文件获取财务数据
 * 格式：{"300750.SZ": {"name": "宁德时代", "data": [{"year": 2023, "revenue": 400000, ...}, ...]}}
 */
function loadLocalFinanceData() {
  const p = path.resolve(process.cwd(), "../../data/cn_finance_data.json");
  if (!fs.existsSync(p)) {
    return null;
  }
  try {
    const text = fs.readFileSync(p, "utf-8");
    return JSON.parse(text);
  } catch (e) {
    console.warn("加载本地财务数据失败:", e.message);
    return null;
  }
}

/**
 * 从本地数据获取财务指标
 */
function getFromLocal(code, metrics = [], years = []) {
  const data = loadLocalFinanceData();
  if (!data || !data[code]) return null;

  const companyData = data[code];
  const series = {};
  const rawData = companyData.data || [];

  // 过滤年份
  let filteredData = rawData;
  if (years.length > 0) {
    filteredData = rawData.filter((d) => years.includes(d.year));
  }

  // 提取指标
  const metricMap = {
    Revenue: "revenue",
    RAndD: "rd",
    GrossProfit: "gross_profit",
    OperatingIncome: "operating_income",
    NetIncome: "net_income",
    TotalAssets: "total_assets",
    TotalLiabilities: "total_liabilities"
  };

  for (const metric of metrics) {
    const field = metricMap[metric] || metric.toLowerCase();
    const points = filteredData
      .map((d) => {
        const value = d[field];
        if (value == null) return null;
        return {
          fy: d.year,
          value: Number(value),
          unit: d.unit || "CNY"
        };
      })
      .filter((p) => p !== null);

    if (points.length > 0) {
      series[metric] = points.sort((a, b) => a.fy - b.fy);
    }
  }

  // 计算派生指标
  if (series.Revenue?.length && series.GrossProfit?.length) {
    series.GrossMargin = series.GrossProfit.map((gp) => {
      const rev = series.Revenue.find((r) => r.fy === gp.fy);
      if (!rev || rev.value === 0) return null;
      return {
        fy: gp.fy,
        value: gp.value / rev.value,
        unit: "ratio"
      };
    }).filter(Boolean);
  }

  if (series.Revenue?.length && series.RAndD?.length) {
    series.RAndDRatio = series.RAndD.map((rd) => {
      const rev = series.Revenue.find((r) => r.fy === rd.fy);
      if (!rev || rev.value === 0) return null;
      return {
        fy: rd.fy,
        value: rd.value / rev.value,
        unit: "ratio"
      };
    }).filter(Boolean);
  }

  if (series.Revenue?.length) {
    // 计算YoY
    const yoy = [];
    for (let i = 1; i < series.Revenue.length; i++) {
      const prev = series.Revenue[i - 1];
      const cur = series.Revenue[i];
      if (prev.value === 0) continue;
      yoy.push({
        fy: cur.fy,
        value: (cur.value - prev.value) / prev.value,
        unit: "ratio"
      });
    }
    if (yoy.length > 0) series.RevenueYoY = yoy;

    // 计算3年CAGR
    if (series.Revenue.length >= 4) {
      const tail = series.Revenue.slice(-4);
      const start = tail[0];
      const end = tail[tail.length - 1];
      if (start.value > 0) {
        const n = end.fy - start.fy;
        if (n > 0) {
          const cagr = Math.pow(end.value / start.value, 1 / n) - 1;
          series.RevenueCAGR3Y = [
            {
              fy: end.fy,
              value: cagr,
              unit: "ratio"
            }
          ];
        }
      }
    }
  }

  return {
    series,
    sourceUrl: `local:data/cn_finance_data.json#${code}`,
    companyName: companyData.name || code
  };
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
 * 通过Tushare API获取利润表数据（income接口）
 */
async function getIncomeFromTushare(code, metrics = [], years = [], apiToken, apiUrl) {
  try {
    const startDate = years.length > 0 ? `${Math.min(...years)}0101` : "";
    const endDate = years.length > 0 ? `${Math.max(...years)}1231` : "";
    
    // 利润表字段：营业收入、营业利润、净利润、研发费用等
    const fields = "ts_code,end_date,f_ann_date,revenue,operate_profit,n_income,rd_exp";
    
    const data = await callTushareAPI("income", {
      ts_code: code,
      start_date: startDate,
      end_date: endDate,
      fields: fields
    }, apiToken, apiUrl);

    if (!data || !data.items) return null;

    const series = {};
    const items = data.items;

    // 转换为统一格式
    const revenueData = [];
    const operatingIncomeData = [];
    const netIncomeData = [];
    const rdData = [];

    for (const item of items) {
      const endDate = item.end_date || "";
      const year = endDate.length >= 4 ? parseInt(endDate.substring(0, 4)) : null;
      if (!year) continue;

      // 只取年报数据（end_date以1231结尾）
      if (!endDate.endsWith("1231")) continue;

      if (item.revenue != null && metrics.includes("Revenue")) {
        revenueData.push({ fy: year, value: Number(item.revenue), unit: "CNY" });
      }
      if (item.operate_profit != null && metrics.includes("OperatingIncome")) {
        operatingIncomeData.push({ fy: year, value: Number(item.operate_profit), unit: "CNY" });
      }
      if (item.n_income != null && metrics.includes("NetIncome")) {
        netIncomeData.push({ fy: year, value: Number(item.n_income), unit: "CNY" });
      }
      if (item.rd_exp != null && metrics.includes("RAndD")) {
        rdData.push({ fy: year, value: Number(item.rd_exp), unit: "CNY" });
      }
    }

    if (revenueData.length > 0) {
      series.Revenue = revenueData.sort((a, b) => a.fy - b.fy);
    }
    if (operatingIncomeData.length > 0) {
      series.OperatingIncome = operatingIncomeData.sort((a, b) => a.fy - b.fy);
    }
    if (netIncomeData.length > 0) {
      series.NetIncome = netIncomeData.sort((a, b) => a.fy - b.fy);
    }
    if (rdData.length > 0) {
      series.RAndD = rdData.sort((a, b) => a.fy - b.fy);
    }

    return series;
  } catch (e) {
    console.warn("Tushare利润表数据获取失败:", e.message);
    return null;
  }
}

/**
 * 通过Tushare API获取资产负债表数据（balancesheet接口）
 */
async function getBalanceSheetFromTushare(code, metrics = [], years = [], apiToken, apiUrl) {
  try {
    const startDate = years.length > 0 ? `${Math.min(...years)}0101` : "";
    const endDate = years.length > 0 ? `${Math.max(...years)}1231` : "";
    
    const fields = "ts_code,end_date,total_assets,total_liab";
    
    const data = await callTushareAPI("balancesheet", {
      ts_code: code,
      start_date: startDate,
      end_date: endDate,
      fields: fields
    }, apiToken, apiUrl);

    if (!data || !data.items) return null;

    const series = {};
    const items = data.items;

    const totalAssetsData = [];
    const totalLiabilitiesData = [];

    for (const item of items) {
      const endDate = item.end_date || "";
      const year = endDate.length >= 4 ? parseInt(endDate.substring(0, 4)) : null;
      if (!year || !endDate.endsWith("1231")) continue;

      if (item.total_assets != null && metrics.includes("TotalAssets")) {
        totalAssetsData.push({ fy: year, value: Number(item.total_assets), unit: "CNY" });
      }
      if (item.total_liab != null && metrics.includes("TotalLiabilities")) {
        totalLiabilitiesData.push({ fy: year, value: Number(item.total_liab), unit: "CNY" });
      }
    }

    if (totalAssetsData.length > 0) {
      series.TotalAssets = totalAssetsData.sort((a, b) => a.fy - b.fy);
    }
    if (totalLiabilitiesData.length > 0) {
      series.TotalLiabilities = totalLiabilitiesData.sort((a, b) => a.fy - b.fy);
    }

    return series;
  } catch (e) {
    console.warn("Tushare资产负债表数据获取失败:", e.message);
    return null;
  }
}

/**
 * 通过Tushare API获取现金流量表数据（cashflow接口）
 */
async function getCashFlowFromTushare(code, metrics = [], years = [], apiToken, apiUrl) {
  try {
    const startDate = years.length > 0 ? `${Math.min(...years)}0101` : "";
    const endDate = years.length > 0 ? `${Math.max(...years)}1231` : "";
    
    const fields = "ts_code,end_date,oper_cash_flow";
    
    const data = await callTushareAPI("cashflow", {
      ts_code: code,
      start_date: startDate,
      end_date: endDate,
      fields: fields
    }, apiToken, apiUrl);

    if (!data || !data.items) return null;

    const series = {};
    const items = data.items;

    const operatingCashFlowData = [];

    for (const item of items) {
      const endDate = item.end_date || "";
      const year = endDate.length >= 4 ? parseInt(endDate.substring(0, 4)) : null;
      if (!year || !endDate.endsWith("1231")) continue;

      if (item.oper_cash_flow != null && metrics.includes("OperatingCashFlow")) {
        operatingCashFlowData.push({ fy: year, value: Number(item.oper_cash_flow), unit: "CNY" });
      }
    }

    if (operatingCashFlowData.length > 0) {
      series.OperatingCashFlow = operatingCashFlowData.sort((a, b) => a.fy - b.fy);
    }

    return series;
  } catch (e) {
    console.warn("Tushare现金流量表数据获取失败:", e.message);
    return null;
  }
}

/**
 * 通过Tushare API获取财务数据（整合多个接口）
 */
async function getFromTushare(code, metrics = [], years = [], apiToken, apiUrl) {
  if (!apiToken && !config.tushare.token) return null;

  const token = apiToken || config.tushare.token;
  const url = apiUrl || config.tushare.apiUrl;

  try {
    const allSeries = {};

    // 利润表数据
    const incomeSeries = await getIncomeFromTushare(code, metrics, years, token, url);
    if (incomeSeries) Object.assign(allSeries, incomeSeries);

    // 资产负债表数据
    const balanceSeries = await getBalanceSheetFromTushare(code, metrics, years, token, url);
    if (balanceSeries) Object.assign(allSeries, balanceSeries);

    // 现金流量表数据
    const cashflowSeries = await getCashFlowFromTushare(code, metrics, years, token, url);
    if (cashflowSeries) Object.assign(allSeries, cashflowSeries);

    if (Object.keys(allSeries).length === 0) return null;

    return {
      series: allSeries,
      sourceUrl: `tushare_api:${url}#${code}`,
      companyName: code
    };
  } catch (e) {
    console.warn("Tushare财务数据API调用失败:", e.message);
    return null;
  }
}

/**
 * 获取中国A股公司财务数据
 * @param {Object} options
 * @param {string} options.code - 股票代码（如 300750.SZ）
 * @param {Array<string>} options.metrics - 指标列表
 * @param {Array<number>} options.years - 年份列表
 * @param {string} options.tushareToken - Tushare API token（可选）
 * @returns {Promise<Object>} {series, sourceUrl, companyName}
 */
export async function getFinancialDataCN({ code, metrics = [], years = [], tushareToken, tushareApiUrl } = {}) {
  if (!code) return { series: {}, sourceUrl: "", companyName: "" };

  // 优先使用本地数据
  const localResult = getFromLocal(code, metrics, years);
  if (localResult && Object.keys(localResult.series).length > 0) {
    return localResult;
  }

  // 如果本地没有，尝试Tushare API
  const token = tushareToken || config.tushare.token;
  const apiUrl = tushareApiUrl || config.tushare.apiUrl;
  
  if (token) {
    const apiResult = await getFromTushare(code, metrics, years, token, apiUrl);
    if (apiResult) return apiResult;
  }

  return { series: {}, sourceUrl: "", companyName: code };
}
