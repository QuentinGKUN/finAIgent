import express from 'express';
import cors from 'cors';
import helmet from 'helmet';
import { v4 as uuidv4 } from 'uuid';

import { config, validateConfig } from './config.js';
import { getDb, saveDb } from './db/db.js';
import { migrate } from './db/migrate.js';
import { ensureSession, appendMessage, getMessages, getMessagesWithMeta } from './db/chat.js';

import { migrateMemory, upsertMemory, getMemories } from './db/memory.js';

import { resolveCompany } from './services/company.js';
import { ensureAnnualReportIndexed, searchAnnualReportRerank } from './services/annualReport.js';
import { getFinancialFacts } from './services/finance.js';
import { getFinancialDataCN } from './services/financeCN.js';

// 你自己的国内榜单服务：建议先做"本地CSV主数据源"最稳
import { getTopRevenueCNFromLocalCSV } from './services/rankingLocal.js';

import { planWithGLM, extractMemoryWithGLM, answerWithGLM, extractTableWithGLM } from './llm/glm.js';

const app = express();
app.use(helmet());
app.use(cors({origin:true}));
app.use(express.json({limit:'6mb'}));

// 验证配置
validateConfig();

const port = config.server.port;
const secUA = config.sec.userAgent;
const goUrl = config.server.goUrl;

const DEBUG_API = (process.env.DEBUG_API || '').toLowerCase()==='true' || process.env.DEBUG_API==='1' || process.env.NODE_ENV !== 'production';

const db = await getDb();
migrate(db);
migrateMemory(db);
saveDb(db);

function hasChartIntent(text=''){
  return /图表|图形|走势图|曲线|折线|柱状|柱形|饼图|chart|plot|graph|趋势|排名|榜单/i.test(text);
}

function splitCompareTargets(text='', fallback=''){
  const raw = (fallback && String(fallback).trim()) ? String(fallback) : String(text || '');
  // 尝试把“对比/比较/VS/和/与/以及/、/,”等连接词拆成多个公司目标
  const norm = raw
    .replace(/（/g,'(').replace(/）/g,')')
    .replace(/\s+/g,' ')
    .trim();

  // 常见“美国前10名公司 vs 中国前10名公司”这种，直接返回两个宏观目标
  if(/美国.*前\s*10|美股.*前\s*10/i.test(norm) && /中国.*前\s*10|A股.*前\s*10/i.test(norm)){
    return ['US_TOP10','CN_TOP10'];
  }

  const parts = norm
    .split(/\s*(?:vs\.?|VS\.?|对比|比较|和|与|以及|&|,|、|\/|\||;|\n)\s*/i)
    .map(s=>s.trim())
    .filter(Boolean);

  const uniq = [];
  for(const p of parts){
    const v = p.replace(/^对比\s*/,'').trim();
    if(v && !uniq.includes(v)) uniq.push(v);
  }
  return uniq.slice(0,3);
}


function escHtml(s=''){
  return s.replace(/[&<>"']/g,m=>({ '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;' }[m]));
}

function shouldExtractTable({plan, userMessage, evidence, answerText}){
  if(!answerText || !String(answerText).trim()) return false;
  if(plan?.task && ['finance','report','compare','ranking'].includes(plan.task)) return true;
  if(evidence?.fin || evidence?.ranking || (evidence?.compare && evidence.compare.length)) return true;
  // 轻量启发式：包含年份 + 数字 或者明显的趋势词
  const t = String(userMessage||'') + '\n' + String(answerText||'');
  const hasYear = /20\d{2}/.test(t);
  const hasNum = /\d+(?:\.\d+)?/.test(t);
  const trend = /趋势|走势|同比|环比|增长|下降|变化|对比|比较|年度|月份|季度|时间线|历年|过去\d+年/i.test(t);
  return (hasYear && hasNum) || trend;
}

app.get('/api/health', (_,res)=>res.json({ok:true, time:new Date().toISOString()}));

app.post('/api/session', (req,res)=>{
  const sessionId = req.body?.sessionId || uuidv4();
  ensureSession(db, sessionId);
  saveDb(db);
  res.json({sessionId});
});

app.post('/api/chat', async (req,res)=>{
  if(DEBUG_API){
    console.log('[API] /api/chat', {sessionId: req.body?.sessionId, messagePreview: (req.body?.message||'').toString().slice(0,200)});
  }
  try{
    const { sessionId, message } = req.body || {};
    if(!sessionId) return res.status(400).json({error:"missing sessionId"});
    if(!message) return res.status(400).json({error:"missing message"});

    // API Key 仅从服务端 .env 读取（不接受前端传入，避免泄露）
    const apiKey = config.ai.apiKey;
    if(!apiKey) return res.status(500).json({error:"missing GLM_KEY (请在 server/.env 中配置)"});

    // Embedding Key 仅从服务端 .env 读取（可选，不配则退化为关键词匹配）
    const embeddingKey = config.embedding?.apiKey || '';

    ensureSession(db, sessionId);
    appendMessage(db, sessionId, 'user', message);
    saveDb(db);

    // 1) 读历史与记忆
    const history = getMessages(db, sessionId, 200);
    const memory = getMemories(db, sessionId);

    // 2) 抽取新记忆并写入DB（让“我是quentin”真正变成系统记忆）
    const memExtract = await extractMemoryWithGLM({apiKey, userMessage: message});
    for(const it of (memExtract.memories || [])){
      if(it?.key && it?.value){
        upsertMemory(db, sessionId, it.key, String(it.value));
      }
    }
    saveDb(db);

    const memory2 = getMemories(db, sessionId);

    // 3) 通用路由（LLM 输出 JSON）
    const plan = await planWithGLM({
      apiKey,
      userMessage: message,
      memory: memory2,
      history
    });
    const plan2 = {...plan};
    if(DEBUG_API){
      console.log('[API] plan', plan2);
    }
    const chartIntent = hasChartIntent(message);
    if(chartIntent && plan2.task === "ranking" && plan2.needClarify){
      plan2.needClarify = false;
      plan2.clarifyQuestion = null;
    }

    // 4) 数据增强（按 plan 调用，不写死具体问题）
    const evidence = {docSnippets:[], fin:null, ranking:null};
    let company = {name:'', cik:'', code:'', tickers:[], market:'', source:''};

    // 4.1 国内榜单（最稳：本地CSV）
    if(plan2.task === "ranking"){
      // plan.years 可能为空，优先用本地榜单最新年份
      const year = (plan2.years && plan2.years[0]) ? plan2.years[0] : null;
      try{
        evidence.ranking = await getTopRevenueCNFromLocalCSV({year, limit:10});
      }catch(e){
        console.warn("rankingLocal error:", e.message || e);
      }
    }

      // 4.2 公司解析：支持美股和A股
    if(plan2.needAnnualReport || plan2.needFinancialData){

      // 4.2.0 对比任务：支持 2~3 家公司（优先 2 家）
      if(plan2.task === 'compare'){
        const qs = splitCompareTargets(message, plan2.companyQuery || '');
        evidence.compare = [];
        for(const q of qs){
          const c = await resolveCompany({
            goUrl,
            secUserAgent: secUA,
            query: q,
            tushareToken: config.tushare.token,
            tushareApiUrl: config.tushare.apiUrl
          });

          // 只做财务数据对比（年报证据对比后续可扩展）
          if(c.market === 'US' && c.cik){
            const fin = await getFinancialFacts({
              goUrl, secUserAgent: secUA, cik: c.cik,
              metrics: ["Revenue","RAndD","GrossProfit","GrossMargin","OperatingIncome","NetIncome"],
              years: plan2.years || []
            });
            evidence.compare.push({company: c, fin});
          } else if((c.market === 'SZ' || c.market === 'SH') && c.code){
            const fin = await getFinancialDataCN({
              code: c.code,
              metrics: ["Revenue","RAndD","GrossProfit","GrossMargin","OperatingIncome","NetIncome","TotalAssets","TotalLiabilities"],
              years: plan2.years || [],
              tushareToken: config.tushare.token,
              tushareApiUrl: config.tushare.apiUrl
            });
            evidence.compare.push({company: c, fin});
          } else {
            evidence.compare.push({company: c, fin: null});
          }
        }

        // compare 任务不走单公司分支
      } else {
      const q = (plan2.companyQuery || '').trim();
      if(q){
        company = await resolveCompany({
          goUrl, 
          secUserAgent: secUA, 
          query: q,
          tushareToken: config.tushare.token,
          tushareApiUrl: config.tushare.apiUrl
        });
      }

      // 4.2.1 美股：年报/财务数据
      if(company.market === 'US'){
        if(plan2.needAnnualReport && company.cik){
          await ensureAnnualReportIndexed({db, goUrl, secUserAgent: secUA, cik: company.cik});
          saveDb(db);
          evidence.docSnippets = await searchAnnualReportRerank({
            db,
            cik: company.cik,
            query: message,
            limit: 6,
            apiKey: embeddingKey || null,
            embedModel: config.embedding?.model
          });
        }

        if(plan2.needFinancialData && company.cik){
          evidence.fin = await getFinancialFacts({
            goUrl, secUserAgent: secUA, cik: company.cik,
            metrics: ["Revenue","RAndD","GrossProfit","GrossMargin","OperatingIncome","NetIncome"],
            years: plan2.years || []
          });
        }
      }
      
      // 4.2.2 A股：财务数据（A股年报暂不支持，可通过其他数据源）
      if(company.market === 'SZ' || company.market === 'SH'){
        if(plan2.needFinancialData && company.code){
          evidence.fin = await getFinancialDataCN({
            code: company.code,
            metrics: ["Revenue","RAndD","GrossProfit","GrossMargin","OperatingIncome","NetIncome","TotalAssets","TotalLiabilities"],
            years: plan2.years || [],
            tushareToken: config.tushare.token,
            tushareApiUrl: config.tushare.apiUrl
          });
        }
        // A股年报可以通过其他方式获取，这里暂时跳过
        // 未来可以集成巨潮资讯等数据源
      }
      }
    }


    if(chartIntent && !evidence.fin && !evidence.ranking && !evidence.docSnippets?.length){
      try{
        evidence.ranking = await getTopRevenueCNFromLocalCSV({
          year: (plan2.years && plan2.years[0]) ? plan2.years[0] : null,
          limit:10
        });
      }catch(e){
        console.warn("rankingLocal fallback error:", e.message || e);
      }
    }

    if(DEBUG_API){
      console.log('[API] evidence', {
        docSnippets: evidence.docSnippets?.length||0,
        finKeys: evidence.fin?.series ? Object.keys(evidence.fin.series) : [],
        hasRanking: !!evidence.ranking,
        compareN: evidence.compare?.length||0
      });
    }

    // 5) 最终回答：默认像正常AI；有证据就增强
    const result = await answerWithGLM({
      apiKey,
      userMessage: message,
      memory: memory2,
      plan: plan2,
      evidence,
      history
    });

    // 5.1) 仅在“看起来是时间序列/对比/榜单/趋势”时，额外抽取表格数据（不影响主回答）
    const maybeNeedsTable = (
      plan2.task === 'finance' || plan2.task === 'report' || plan2.task === 'compare' || plan2.task === 'ranking' ||
      !!evidence.fin || !!evidence.ranking || (evidence.compare && evidence.compare.length>0) ||
      (/20\d{2}/.test(result.answer||'') && /\d+(?:\.\d+)?/.test(result.answer||''))
    );

    if(maybeNeedsTable){
      try{
        const table = await extractTableWithGLM({apiKey, answerText: result.answer || ''});
        if(table?.ok){
          result.table = {
            title: table.title || '数据表格',
            columns: table.columns,
            rows: table.rows
          };
          if(DEBUG_API) console.log('[API] table', {title: result.table.title, cols: result.table.columns?.length, rows: result.table.rows?.length});
        }
      }catch(err){
        if(DEBUG_API) console.warn('[API] table extract failed', err.message||err);
      }
    }

    if(DEBUG_API){
      console.log('[API] answerPreview', (result.answer||'').toString().slice(0,600));
      if(result.chart) console.log('[API] chart', result.chart);
    }

    appendMessage(db, sessionId, 'assistant', result.answer || '', {
      plan,
      memory: memory2,
      citations: result.citations || [],
      chart: result.chart || null,
      table: result.table || null
    });
    saveDb(db);

    res.json(result);

  }catch(e){
    console.error(e);
    res.status(500).json({error:e.message || 'server error'});
  }
});

// 查看历史对话（保留原先“导出”页面的功能，但更名为 history）
app.get('/api/history/:sessionId', (req,res)=>{
  try{
    const sessionId = req.params.sessionId;
    if(!sessionId) return res.status(400).send("missing sessionId");
    const messages = getMessagesWithMeta(db, sessionId, 500);
    if(!messages.length){
      return res.send("<h2>暂无对话数据</h2>");
    }

    let html = `
<!doctype html>
<html lang="zh-Hans">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>历史对话</title>
  <style>
    body{font-family:Arial,Helvetica,sans-serif;color:#0f172a;margin:24px}
    h1{font-size:22px;margin:0 0 6px}
    .meta{color:#64748b;font-size:12px;margin-bottom:16px}
    .msg{border:1px solid #e2e8f0;border-radius:12px;padding:12px;margin:10px 0}
    .role{font-weight:600;margin-bottom:6px}
    .assistant{background:#f8fafc}
    .user{background:#0f172a;color:#fff}
    .section-title{margin:12px 0 6px;font-weight:600}
    table{border-collapse:collapse;width:100%;font-size:12px}
    th,td{border:1px solid #e2e8f0;padding:6px;text-align:left}
    .cite{font-size:12px;color:#475569;margin-top:6px}
  </style>
</head>
<body>
  <h1>历史对话</h1>
  <div class="meta">Session: ${escHtml(sessionId)} | 时间: ${escHtml(new Date().toISOString())}</div>
`;

    for(const m of messages){
      const roleClass = m.role === "assistant" ? "assistant" : "user";
      html += `<div class="msg ${roleClass}">`;
      html += `<div class="role">${escHtml(m.role)}</div>`;
      html += `<div>${escHtml(m.content || "").replace(/\n/g,"<br/>")}</div>`;

      if(m.role === "assistant" && m.meta){
        const citations = m.meta.citations || [];
        const chart = m.meta.chart || null;
        if(citations.length){
          html += `<div class="section-title">引用 / 来源</div><ul class="cite">` +
            citations.map(c=>`<li>${escHtml(c.id||"")} ${escHtml(c.title||c.type||"")} ${c.url ? escHtml(c.url) : ""}</li>`).join("") +
            `</ul>`;
        }
        if(chart?.series?.length){
          html += `<div class="section-title">图表数据：${escHtml(chart.title || "")}</div>`;
          for(const s of chart.series){
            html += `<div class="cite">指标：${escHtml(s.metric || "")}</div>`;
            html += `<table><thead><tr><th>年份/名称</th><th>数值</th></tr></thead><tbody>` +
              (s.points||[]).map(p=>`<tr><td>${escHtml(String(p.year))}</td><td>${escHtml(String(p.value))}</td></tr>`).join("") +
              `</tbody></table>`;
          }
        }
      }

      html += `</div>`;
    }

    html += `</body></html>`;
    res.setHeader("Content-Type","text/html; charset=utf-8");
    res.send(html);
  }catch(e){
    console.error(e);
    res.status(500).send("report error");
  }
});

// 专业报告（用于浏览器打印为 PDF）
app.get('/api/report/:sessionId', (req,res)=>{
  try{
    const sessionId = req.params.sessionId;
    if(!sessionId) return res.status(400).send('missing sessionId');
    const messages = getMessagesWithMeta(db, sessionId, 500);
    if(!messages.length) return res.send('<h2>暂无对话数据</h2>');

    // 取最近一轮问答作为“本次报告”的主体
    const lastAssistantIndex = (()=>{
      for(let i=messages.length-1;i>=0;i--){
        if(messages[i].role==='assistant') return i;
      }
      return -1;
    })();
    const lastAssistant = lastAssistantIndex>=0 ? messages[lastAssistantIndex] : null;
    const lastUser = (()=>{
      if(lastAssistantIndex<0){
        for(let i=messages.length-1;i>=0;i--) if(messages[i].role==='user') return messages[i];
        return null;
      }
      for(let i=lastAssistantIndex-1;i>=0;i--) if(messages[i].role==='user') return messages[i];
      return null;
    })();
    const answer = lastAssistant?.content || '';
    const chart = lastAssistant?.meta?.chart || null;
    const table = lastAssistant?.meta?.table || null;
    const citations = lastAssistant?.meta?.citations || [];

    const title = '财务分析报告';
    const now = new Date().toISOString();

    const esc = escHtml;

    const chartJson = chart ? JSON.stringify(chart) : 'null';
    const tableHtml = table ? (
      `<div class="card">
        <div class="card-title">数据表格：${esc(table.title||'')}</div>
        <div class="table-wrap">
          <table>
            <thead><tr>${(table.columns||[]).map(c=>`<th>${esc(c)}</th>`).join('')}</tr></thead>
            <tbody>
              ${(table.rows||[]).map(r=>`<tr>${(r||[]).map(c=>`<td>${esc(String(c))}</td>`).join('')}</tr>`).join('')}
            </tbody>
          </table>
        </div>
      </div>`
    ) : '';

    const citeHtml = (citations && citations.length) ? (
      `<div class="card">
        <div class="card-title">数据来源与引用</div>
        <div class="muted" style="margin-top:6px">说明：年报证据=年报片段；财务数据=官方财报/接口；榜单数据=排行榜/口径数据源。</div>
        <ul class="cite">
          ${citations.map(c=>`<li><b>${esc(c.id||'')}</b> ${esc(c.title||'')} ${c.url?`<a href="${esc(c.url)}" target="_blank">打开</a>`:''}</li>`).join('')}
        </ul>
      </div>`
    ) : '';

    const html = `
<!doctype html>
<html lang="zh-Hans">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>${title}</title>
  <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
  <style>
    :root{--ink:#0f172a;--muted:#64748b;--line:#e2e8f0;--bg:#f8fafc}
    body{font-family:Arial,Helvetica,sans-serif;color:var(--ink);margin:0;background:#fff}
    .page{max-width:980px;margin:0 auto;padding:28px}
    .header{display:flex;justify-content:space-between;align-items:flex-end;border-bottom:2px solid var(--line);padding-bottom:14px;margin-bottom:18px}
    .title{font-size:22px;font-weight:700}
    .meta{font-size:12px;color:var(--muted)}
    .grid{display:grid;grid-template-columns:1fr;gap:14px}
    .card{border:1px solid var(--line);border-radius:14px;padding:14px;background:var(--bg)}
    .card-title{font-weight:700;margin-bottom:8px}
    .muted{color:var(--muted);font-size:12px}
    .kpi{display:flex;gap:10px;flex-wrap:wrap}
    .pill{border:1px solid var(--line);background:#fff;border-radius:999px;padding:6px 10px;font-size:12px}
    .content{line-height:1.65;font-size:14px;white-space:pre-wrap;word-break:break-word;overflow-wrap:anywhere}
    table{border-collapse:collapse;width:100%;font-size:12px;background:#fff}
    th,td{border:1px solid var(--line);padding:6px;text-align:left}
    .table-wrap{overflow-x:auto}
    .cite{margin:8px 0 0;padding-left:18px;font-size:12px;color:var(--muted)}
    .cite a{color:var(--ink)}
    @media print{ .page{padding:0} .card{break-inside:avoid} }
  </style>
</head>
<body>
  <div class="page">
    <div class="header">
      <div>
        <div class="title">${title}</div>
        <div class="meta">Session: ${esc(sessionId)} | 生成时间: ${esc(now)}</div>
      </div>
      <div class="meta">Fin Assistant Champion</div>
    </div>

    <div class="grid">
      <div class="card">
        <div class="card-title">本次问题</div>
        <div class="content">${esc(lastUser?.content || '')}</div>
      </div>

      <div class="card">
        <div class="card-title">核心结论与分析</div>
        <div class="muted">注：当回答中出现“年报证据/财务数据/榜单数据”引用时，报告底部会列出数据来源。</div>
        <div class="content" style="margin-top:10px">${esc(answer)}</div>
      </div>

      <div class="card" id="chartCard" style="display:${chart ? 'block' : 'none'}">
        <div class="card-title">图表</div>
        <canvas id="chart" height="240"></canvas>
        <div class="muted" id="chartNote" style="margin-top:8px"></div>
      </div>

      ${tableHtml}

      ${citeHtml}
    </div>
  </div>

  <script>
    const chartData = ${chartJson};
    function unionLabels(series){
      const set = new Set();
      (series||[]).forEach(s => (s.points||[]).forEach(p => set.add(p.x)));
      let labels = Array.from(set);
      if(labels.every(x => typeof x === 'number' || /^\d+$/.test(String(x)))){
        labels = labels.map(x => Number(x)).sort((a,b)=>a-b);
      }
      return labels;
    }
    function alignSeries(series, labels){
      return (series||[]).map(s=>{
        const map = new Map((s.points||[]).map(p=>[String(p.x), p.y]));
        return {label: s.name, data: labels.map(x => map.has(String(x)) ? map.get(String(x)) : null)};
      });
    }
    if(chartData && chartData.series && chartData.series.length){
      const labels = unionLabels(chartData.series);
      const datasets = alignSeries(chartData.series, labels);
      const ctx = document.getElementById('chart').getContext('2d');
      new Chart(ctx, {
        type: chartData.type || 'line',
        data: { labels, datasets },
        options: {
          responsive: true,
          plugins: {
            title: { display: true, text: chartData.title || '图表' }
          },
          scales: {
            x: { title: { display: true, text: chartData.xLabel || '年度' } },
            y: { title: { display: true, text: chartData.yLabel || '数值' } }
          }
        }
      });
      document.getElementById('chartNote').innerText = `横轴：${chartData.xLabel||'年度'}；纵轴：${chartData.yLabel||'数值'}`;
    }
  </script>
</body>
</html>`;

    res.setHeader('Content-Type','text/html; charset=utf-8');
    res.send(html);
  }catch(e){
    console.error(e);
    res.status(500).send(e.message || 'server error');
  }
});

app.listen(port, ()=>console.log(`Express http://localhost:${port}`));