import fetch from "node-fetch";

// 尝试从 Fortune Global 500 的公开页面抓取（不同年份页面结构可能变化）
// 为了比赛稳定，这里做“HTML中提取JSON片段”的宽松解析。
// 如果未来页面变动，你只需要替换 parse 部分即可。

function num(v){
  if(v == null) return null;
  const s = String(v).replace(/[, ]/g,'').trim();
  const x = Number(s);
  return Number.isFinite(x) ? x : null;
}

function isChina(name, country){
  const c = (country || "").toLowerCase();
  if(c.includes("china")) return true;
  // 兼容 “China, Hong Kong” 等
  if(c.includes("hong kong") && c.includes("china")) return true;
  return false;
}

export async function getTopRevenueCN({year=2024, limit=10}){
  // Fortune 年份页面常见形态：/global500/<year>/search/
  const url = `https://fortune.com/rankings/global500/${year}/search/`;
  const resp = await fetch(url, {headers: {"User-Agent":"Mozilla/5.0"}});
  if(!resp.ok){
    const t = await resp.text().catch(()=> "");
    throw new Error(`榜单抓取失败: ${resp.status} ${t.slice(0,120)}`);
  }
  const html = await resp.text();

  // 在页面里找可能的数据片段（Fortune 通常在 window.__DATA__ / NEXT_DATA / JSON script 里）
  // 我们做宽松兜底：找包含 "revenue" & "country" 的大 JSON。
  const candidates = [];
  const scriptRe = /<script[^>]*>([\s\S]*?)<\/script>/g;
  let m;
  while((m = scriptRe.exec(html))){
    const s = m[1];
    if(s.includes("revenue") && s.includes("country")) candidates.push(s);
  }

  let rows = null;

  for(const s of candidates){
    // 试图从脚本里截取 JSON
    const jsonMatch = s.match(/\{[\s\S]*\}/);
    if(!jsonMatch) continue;
    try{
      const obj = JSON.parse(jsonMatch[0]);
      // 在 obj 里深搜可能的列表
      const stack = [obj];
      while(stack.length){
        const cur = stack.pop();
        if(Array.isArray(cur)){
          // 看是不是公司列表
          if(cur.length && typeof cur[0] === "object" && (("revenue" in cur[0]) || ("revenues" in cur[0]))){
            rows = cur;
            break;
          }
          for(const it of cur) if(it && typeof it === "object") stack.push(it);
        }else if(cur && typeof cur === "object"){
          for(const k of Object.keys(cur)) stack.push(cur[k]);
        }
      }
    }catch{}
    if(rows) break;
  }

  // 如果页面结构变了，给出明确报错
  if(!rows){
    throw new Error("未能从榜单页面解析出数据（页面结构可能变化）。");
  }

  // 规范字段
  const norm = rows.map(r=>{
    const name = r.companyName || r.name || r.company || r.title;
    const country = r.country || r.countryName || r.headquartersCountry || r.hq_country;
    const revenue = num(r.revenue) ?? num(r.revenues) ?? num(r.revenue_mil) ?? num(r.revenueMillions);
    // Fortune 常见口径：revenue 是百万美元（Millions）
    // 如果字段是 revenue（millions），我们在输出里标注单位
    return {name, country, revenue};
  }).filter(x=>x.name && x.revenue && isChina(x.name, x.country));

  norm.sort((a,b)=>b.revenue - a.revenue);
  const top = norm.slice(0, limit);

  return {
    source: { title:`Fortune Global 500 ${year}`, url },
    unit: "USD_millions",
    year,
    items: top
  };
}
