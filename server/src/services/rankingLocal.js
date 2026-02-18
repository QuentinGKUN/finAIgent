import fs from "fs";
import path from "path";

// 放一个本地 CSV：data/cn_top_revenue.csv
// 格式：year,rank,name,revenue,unit,source_url
// 2026,1,中国石油,3200000,RMBCNY_million,https://example.com

function loadLocalRankingCSV(){
  const p = path.resolve(process.cwd(), "../data/cn_top_revenue.csv");
  if(!fs.existsSync(p)){
    throw new Error("本地榜单缺失：请创建 data/cn_top_revenue.csv");
  }
  const text = fs.readFileSync(p, "utf-8");
  const lines = text.split(/\r?\n/).filter(Boolean);
  const rows = [];
  for(let i=1;i<lines.length;i++){
    const [yy, rank, name, revenue, unit, source_url] = lines[i].split(",");
    if(!yy || !rank || !name || !revenue) continue;
    rows.push({
      year: Number(yy),
      rank: Number(rank),
      name: name,
      value: Number(revenue),
      unit: unit || "CNY_million",
      source_url: source_url || ""
    });
  }
  return rows;
}

function getLatestYear(rows){
  let latest = null;
  for(const r of rows){
    if(Number.isFinite(r.year)){
      if(latest === null || r.year > latest) latest = r.year;
    }
  }
  return latest;
}

export async function getTopRevenueCNFromLocalCSV({year, limit=10} = {}){
  const rows = loadLocalRankingCSV();
  const latestYear = getLatestYear(rows);
  const targetYear = year || latestYear;
  if(!targetYear){
    throw new Error("本地榜单为空：未找到有效年份");
  }
  const picked = rows.filter(r=>r.year === Number(targetYear));
  picked.sort((a,b)=>a.rank-b.rank);
  const items = picked.slice(0, limit).map(r=>({rank:r.rank, name:r.name, value:r.value}));
  const unit = picked[0]?.unit || "CNY_million";
  return {
    year: Number(targetYear),
    metric: "Revenue",
    unit,
    source: {title:"本地榜单CSV", url:"local:data/cn_top_revenue.csv"},
    items
  };
}
