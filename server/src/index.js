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

import { planWithGemini, extractMemoryWithGemini, answerWithGemini } from './llm/gemini.js';

const app = express();
app.use(helmet());
app.use(cors({origin:true}));
app.use(express.json({limit:'6mb'}));

// 验证配置
validateConfig();

const port = config.server.port;
const secUA = config.sec.userAgent;
const goUrl = config.server.goUrl;

const db = await getDb();
migrate(db);
migrateMemory(db);
saveDb(db);

function hasChartIntent(text=''){
  return /图表|图形|走势图|曲线|折线|柱状|柱形|饼图|chart|plot|graph|趋势|排名|榜单/i.test(text);
}

function escHtml(s=''){
  return s.replace(/[&<>"']/g,m=>({ '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;' }[m]));
}

app.get('/api/health', (_,res)=>res.json({ok:true, time:new Date().toISOString()}));

app.post('/api/session', (req,res)=>{
  const sessionId = req.body?.sessionId || uuidv4();
  ensureSession(db, sessionId);
  saveDb(db);
  res.json({sessionId});
});

app.post('/api/chat', async (req,res)=>{
  try{
    const { sessionId, message, geminiApiKey } = req.body || {};
    if(!sessionId) return res.status(400).json({error:"missing sessionId"});
    if(!message) return res.status(400).json({error:"missing message"});

    // 优先使用前端传入的API Key，否则使用配置文件中的
    const apiKey = geminiApiKey || config.ai.apiKey;
    if(!apiKey) return res.status(400).json({error:"missing GLM_KEY (请在.env文件中配置或前端传入)"});

    ensureSession(db, sessionId);
    appendMessage(db, sessionId, 'user', message);
    saveDb(db);

    // 1) 读历史与记忆
    const history = getMessages(db, sessionId, 200);
    const memory = getMemories(db, sessionId);

    // 2) 抽取新记忆并写入DB（让“我是quentin”真正变成系统记忆）
    const memExtract = await extractMemoryWithGemini({apiKey, userMessage: message});
    for(const it of (memExtract.memories || [])){
      if(it?.key && it?.value){
        upsertMemory(db, sessionId, it.key, String(it.value));
      }
    }
    saveDb(db);

    const memory2 = getMemories(db, sessionId);

    // 3) 通用路由（LLM 输出 JSON）
    const plan = await planWithGemini({
      apiKey,
      userMessage: message,
      memory: memory2,
      history
    });
    const plan2 = {...plan};
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
            db, cik: company.cik, query: message, limit: 6, apiKey: null
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

    // 5) 最终回答：默认像正常AI；有证据就增强
    const result = await answerWithGemini({
      apiKey,
      userMessage: message,
      memory: memory2,
      plan: plan2,
      evidence,
      history
    });

    appendMessage(db, sessionId, 'assistant', result.answer || '', {
      plan,
      memory: memory2,
      citations: result.citations || [],
      chart: result.chart || null
    });
    saveDb(db);

    res.json(result);

  }catch(e){
    console.error(e);
    res.status(500).json({error:e.message || 'server error'});
  }
});

app.get('/api/report/:sessionId', (req,res)=>{
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
  <title>投研报告</title>
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
  <h1>投研报告</h1>
  <div class="meta">Session: ${escHtml(sessionId)} | 生成时间: ${escHtml(new Date().toISOString())}</div>
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

app.listen(port, ()=>console.log(`Express http://localhost:${port}`));
