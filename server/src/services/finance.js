import fetch from 'node-fetch';

function yoy(series=[]){
  const out = [];
  for(let i=1;i<series.length;i++){
    const prev = series[i-1], cur = series[i];
    if(prev.value === 0) continue;
    out.push({fy: cur.fy, value: (cur.value - prev.value) / prev.value, unit:'ratio'});
  }
  return out;
}

function cagr(series=[], years=3){
  if(series.length < years+1) return null;
  const tail = series.slice(-(years+1));
  const start = tail[0], end = tail[tail.length-1];
  if(start.value <= 0) return null;
  const n = end.fy - start.fy;
  if(n <= 0) return null;
  const v = Math.pow(end.value / start.value, 1/n) - 1;
  return {fy: end.fy, value: v, unit:'ratio'};
}

function ratio(a=[], b=[]){
  const bm = new Map(b.map(x=>[x.fy, x.value]));
  return a.map(x=>{
    const d = bm.get(x.fy);
    if(d == null || d === 0) return null;
    return {fy:x.fy, value: x.value/d, unit:'ratio'};
  }).filter(Boolean);
}

export async function getFinancialFacts({goUrl, secUserAgent, cik, metrics=[], years=[]}){
  if(!cik) return {series:{}, sourceUrl:''};
  const url = `${goUrl}/sec/companyfacts?cik=${encodeURIComponent(cik)}&metrics=${encodeURIComponent(metrics.join(','))}&years=${encodeURIComponent(years.join(','))}`;
  const resp = await fetch(url, {headers:{'User-Agent':secUserAgent}});
  if(!resp.ok) throw new Error(`财务数据失败: ${resp.status} ${await resp.text()}`);
  const data = await resp.json();

  const s = data.series || {};
  if(s.Revenue?.length){
    s.RevenueYoY = yoy(s.Revenue);
    const c3 = cagr(s.Revenue, 3);
    if(c3) s.RevenueCAGR3Y = [c3];
  }
  if(s.RAndD?.length && s.Revenue?.length){
    s.RAndDRatio = ratio(s.RAndD, s.Revenue);
  }
  if(s.OperatingIncome?.length && s.Revenue?.length){
    s.OperatingMargin = ratio(s.OperatingIncome, s.Revenue);
  }
  if(s.NetIncome?.length && s.Revenue?.length){
    s.NetMargin = ratio(s.NetIncome, s.Revenue);
  }

  data.series = s;
  return data;
}
