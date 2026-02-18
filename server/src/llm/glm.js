import fetch from "node-fetch";

const ZHIPU_URL = "https://open.bigmodel.cn/api/paas/v4/chat/completions";

function previewMessages(messages){
  try{
    const last = [...(messages||[])].reverse().find(m=>m && m.role && m.content);
    const content = (last?.content || "").toString();
    const brief = content.length > 280 ? content.slice(0,280) + "…" : content;
    return {count: (messages||[]).length, lastRole: last?.role || "", lastBrief: brief};
  }catch{
    return {count: (messages||[]).length, lastRole: "", lastBrief: ""};
  }
}

const DEBUG_GLM = (process.env.DEBUG_GLM || '').toLowerCase() === 'true' || process.env.DEBUG_GLM === '1' || process.env.NODE_ENV !== 'production';

async function callGLM({apiKey, messages, temperature=0.5, max_tokens=1400}){
  if(!apiKey || apiKey.trim() === ''){
    throw new Error("GLM API Key未设置，请在.env文件中配置GLM_KEY或在前端设置");
  }

  if(DEBUG_GLM){
    const p = previewMessages(messages);
    console.log("[GLM] request", {model:"glm-4-flash", temperature, max_tokens, ...p});
  }

  const res = await fetch(ZHIPU_URL,{
    method:"POST",
    headers:{
      "Content-Type":"application/json",
      "Authorization":`Bearer ${apiKey.trim()}`
    },
    body:JSON.stringify({
      model:"glm-4-flash",
      messages,
      temperature,
      max_tokens
    })
  });
  
  const json = await res.json();
  if(DEBUG_GLM){
    const out = json?.choices?.[0]?.message?.content;
    const brief = (out||'').toString();
    console.log("[GLM] response", {status: res.status, ok: res.ok, preview: brief.length>500? brief.slice(0,500)+"…" : brief});
    if(json?.error) console.log("[GLM] error", json.error);
  }
  
  // 检查API错误
  if(json.error){
    const errorCode = json.error.code;
    const errorMsg = json.error.message || '未知错误';
    
    if(errorCode === 401){
      throw new Error(`GLM API Key验证失败（401）: ${errorMsg}。请检查：\n1. .env文件中的GLM_KEY是否正确\n2. API Key是否已过期\n3. 前端传入的GLM API Key是否有效`);
    } else if(errorCode === 429){
      throw new Error(`GLM API调用频率超限（429）: ${errorMsg}。请稍后再试`);
    } else {
      throw new Error(`GLM API错误（${errorCode}）: ${errorMsg}`);
    }
  }
  
  const text = json?.choices?.[0]?.message?.content;
  if(!text) {
    throw new Error("GLM返回异常: " + JSON.stringify(json).slice(0,240));
  }
  return text;
}

function looksLikeTrendQuestion(text=''){
  return /趋势|走势|同比|环比|增长|下降|变化|对比|比较|年度|月份|季度|时间线|历年|过去\d+年/i.test(text);
}

function hasYearAndNumber(text=''){
  const hasYear = /20\d{2}/.test(text);
  const hasNum = /\d+(?:\.\d+)?/.test(text);
  return hasYear && hasNum;
}

/**
 * 不影响主回答的"表格数据抽取器"：
 * - 仅在看起来是趋势/时间序列/对比类内容时触发
 * - 抽取失败则忽略
 */
export async function extractTableWithGLM({apiKey, answerText, hintTitle=''}){
  const sys = `
你是"表格抽取器"。从助手的中文回答中抽取适合表格展示的结构化数据。
仅输出严格 JSON，不要解释，不要 Markdown。

目标：如果回答里包含时间序列、年度数据、排名数据、对比数据等，请抽取成表格。
如果没有可抽取的明确数据，请输出：{"ok":false}

输出 JSON 格式：
{
  "ok": true,
  "title": "表格标题",
  "columns": ["列1","列2",...],
  "rows": [["...","..."], ...]
}

要求：
- 数字保留两位小数（如 1234.50）
- 年度/日期按从早到晚排序
`.trim();

  const user = `回答文本如下。请抽取可表格化的数据：\n\n${answerText}`;
  const messages = [
    {role:'system', content: sys},
    {role:'user', content: user}
  ];

  const text = await callGLM({apiKey, messages, temperature:0.1, max_tokens:700});
  try{
    const j = JSON.parse(text);
    if(!j || j.ok !== true) return {ok:false};
    if(!Array.isArray(j.columns) || !Array.isArray(j.rows)) return {ok:false};
    return j;
  }catch{
    return {ok:false};
  }
}

/**
 * 让 LLM 做"通用路由"，不要写死规则。
 * 输出 JSON：
 * {
 *  "needAnnualReport": boolean,
 *  "needFinancialData": boolean,
 *  "companyQuery": string|null,
 *  "needClarify": boolean,
 *  "clarifyQuestion": string|null,
 *  "task": "chat"|"finance"|"report"|"ranking"|"compare",
 *  "years": [2022,2023] //可空
 * }
 */
export async function planWithGLM({apiKey, userMessage, memory, history=[]}){
  const sys = `
你是"任务路由器"。请把用户输入解析为严格 JSON，不要解释，不要 Markdown。
目标：让系统像正常AI聊天；仅在需要时才调用财务/年报数据增强。
你可以参考会话记忆与上下文。

输出格式：
{
  "task":"chat|finance|report|ranking|compare",
  "needAnnualReport":true/false,
  "needFinancialData":true/false,
  "companyQuery": "公司名/股票简称/可空",
  "years":[2020,2021,2022],
  "needClarify":true/false,
  "clarifyQuestion":"需要追问的问题/可空"
}

规则：
- **如果用户只是在打招呼（例如"你好/hello/在吗/hi"）或寒暄**：必须输出 task="chat"，needAnnualReport=false，needFinancialData=false，companyQuery=null，needClarify=false。
- 普通问候/闲聊/个人信息 → task=chat, needAnnualReport=false, needFinancialData=false
- 涉及某公司财务指标/趋势/对比/研报 → task=finance 或 report/compare，尽量识别 companyQuery
- "排名/前十/榜单"类 → task=ranking，如果缺少年份/口径需 needClarify=true 并提出问题
- years：能提取就提取，提不出可空
- companyQuery：支持识别公司名称（中英文）、股票代码（如300750.SZ、TSLA等）
- 系统支持美股（通过SEC API）和A股（通过本地数据或Tushare API），自动识别
`.trim();

  const memText = memory && Object.keys(memory).length
    ? `会话记忆：${JSON.stringify(memory)}`
    : "会话记忆：{}";

  const ctx = history.slice(-6).map(m=>`${m.role}:${m.content}`).join("\n");

  const prompt = `
${memText}

最近对话：
${ctx}

用户输入：
${userMessage}
`.trim();

  const out = await callGLM({
    apiKey,
    messages:[
      {role:"system", content: sys},
      {role:"user", content: prompt}
    ],
    temperature:0.1,
    max_tokens:600
  });

  try { return JSON.parse(out); }
  catch {
    const m = out.match(/\{[\s\S]*\}/);
    if(m) return JSON.parse(m[0]);
    return {task:"chat",needAnnualReport:false,needFinancialData:false,companyQuery:"",years:[],needClarify:false,clarifyQuestion:null};
  }
}

/**
 * 让 LLM 从用户话里抽取"可记忆信息"，写入数据库。
 * 输出 JSON: {"memories":[{"key":"user_name","value":"Quentin"}]}
 */
export async function extractMemoryWithGLM({apiKey, userMessage}){
  const sys = `
你是"记忆抽取器"。从用户文本中提取值得长期记住的信息，输出严格 JSON：
{"memories":[{"key":"user_name","value":"xxx"},{"key":"user_preference","value":"xxx"}]}
规则：
- 只提取用户明确陈述的事实（例如"我叫X"、"我在北京"、"我喜欢新能源"）
- 如果没有可记忆信息，输出 {"memories":[]}
只输出 JSON。
`.trim();

  const out = await callGLM({
    apiKey,
    messages:[
      {role:"system", content: sys},
      {role:"user", content: userMessage}
    ],
    temperature:0.1,
    max_tokens:300
  });

  try { return JSON.parse(out); }
  catch {
    const m = out.match(/\{[\s\S]*\}/);
    if(m) return JSON.parse(m[0]);
    return {memories:[]};
  }
}

/**
 * 最终回答：默认像正常AI聊天；
 * 只有 evidence 存在时才做"财务/年报增强"。
 */
export async function answerWithGLM({apiKey, userMessage, memory, plan, evidence, history=[]}){
  const memText = memory && Object.keys(memory).length
    ? `已知的用户信息（必须遵守）：${JSON.stringify(memory)}`
    : "已知的用户信息：{}";

  // 追问
  if(plan.needClarify && plan.clarifyQuestion){
    return {answer: plan.clarifyQuestion, citations:[], chart:null};
  }

  // 数据增强上下文（可空）
  let toolCtx = "";
  if(evidence?.docSnippets?.length){
    toolCtx += "【年报证据】\n" + evidence.docSnippets.slice(0,4).map((d,i)=>`[D${i+1}] ${d.section||""} ${d.text}`).join("\n") + "\n";
  }
  if(evidence?.fin?.series){
    toolCtx += "【财务数据】\n";
    for(const [k, arr] of Object.entries(evidence.fin.series)){
      if(!arr?.length) continue;
      toolCtx += `${k}: ${arr.map(x=>`${x.fy}:${x.value}`).join(" ")}\n`;
    }
  }

  if(evidence?.compare?.length){
    toolCtx += "【对比数据】\n";
    for(const item of evidence.compare){
      const cname = item.company?.name || item.company?.code || item.company?.query || '';
      if(item.fin?.series){
        toolCtx += `- ${cname}\n`;
        for(const [k, arr] of Object.entries(item.fin.series)){
          if(!arr?.length) continue;
          toolCtx += `  ${k}: ${arr.map(x=>`${x.fy}:${x.value}`).join(" ")}\n`;
        }
      } else {
        toolCtx += `- ${cname}: (无可用财务数据)\n`;
      }
    }
  }

  if(evidence?.ranking){
    toolCtx += "【榜单数据】\n" + evidence.ranking.items.map((x,i)=>`${i+1}. ${x.name} ${x.value} ${evidence.ranking.unit}`).join("\n") + "\n";
  }

  const sys = `
你是中文智能财务助手：既能正常聊天，也能做严谨财务分析。
要求：
- 默认像普通AI一样自然回复。
- **如果用户只是打招呼/寒暄/确认在不在（如"你好""hi""在吗"）**：只需简短友好回应，并问一句"你想做年报问答/财务趋势/公司对比/国内榜单中的哪一种？"不要自作主张输出榜单或公司列表。
- 当提供了【年报证据/财务数据/榜单数据】时，必须基于证据回答，并在关键结论后标注引用 [D1]/[F1]/[R1]。
- 不要编造不存在的数字。
`.trim();

  const ctx = history.slice(-8).map(m=>({role: m.role==="assistant"?"assistant":"user", content:m.content}));
  const messages = [
    {role:"system", content: sys + "\n" + memText},
    ...ctx,
    {role:"user", content: `${userMessage}\n\n${toolCtx ? ("可用证据：\n"+toolCtx) : ""}`.trim()}
  ];

  const text = await callGLM({apiKey, messages, temperature:0.55, max_tokens:1500});

  // chart：统一输出结构，前端仅渲染 chart 对象即可
  let chart = null;

  // 1) 对比任务：多条序列（按 Revenue 优先）
  if(evidence?.compare?.length){
    const series = [];
    const metric = "Revenue";
    for(const it of evidence.compare){
      const name = it.company?.name || it.company?.code || it.company?.query || 'Unknown';
      const arr = it.fin?.series?.[metric] || [];
      if(arr.length){
        series.push({
          name,
          points: arr.map(r=>({x: r.fy, y: r.value}))
        });
      }
    }
    if(series.length){
      chart = {
        title: `营收对比趋势`,
        type: "line",
        xLabel: "年度",
        yLabel: "营收",
        series
      };
    }
  }

  // 2) 单公司财务：Revenue 趋势
  if(!chart && evidence?.fin?.series?.Revenue?.length){
    chart = {
      title: "营收趋势",
      type: "line",
      xLabel: "年度",
      yLabel: "营收",
      series: [{
        name: "营收",
        points: evidence.fin.series.Revenue.map(r=>({x:r.fy, y:r.value}))
      }]
    };
  }

  // 3) 榜单：Top10
  if(!chart && evidence?.ranking?.items?.length){
    chart = {
      title: "榜单 Top10",
      type: "bar",
      xLabel: "公司",
      yLabel: evidence.ranking.unit || "值",
      series: [{
        name: evidence.ranking.metric === 'Revenue' ? '营收' : (evidence.ranking.metric || "指标"),
        points: evidence.ranking.items.map(it=>({x: it.name, y: it.value}))
      }]
    };
  }

  // citations：简化（你前端若展示 citations 也 OK）（你前端若展示 citations 也 OK）
  const citations = [];
  if(evidence?.docSnippets?.length) citations.push({id:"D1", type:"annual_report", title:"SEC 年报片段", url:evidence.docSnippets[0]?.url||"", note:"年报检索片段"});
  if(evidence?.fin?.sourceUrl) {
    const sourceType = evidence.fin.sourceUrl.includes('sec.gov') ? 'sec_companyfacts' : 'cn_finance';
    const sourceTitle = evidence.fin.sourceUrl.includes('sec.gov') ? 'SEC companyfacts' : (evidence.fin.sourceUrl.includes('tushare') ? 'Tushare API' : '本地财务数据');
    citations.push({id:"F1", type:sourceType, title:sourceTitle, url:evidence.fin.sourceUrl, note:"财务数据序列"});
  }
  if(evidence?.ranking?.source) citations.push({id:"R1", type:"ranking", title:evidence.ranking.source.title, url:evidence.ranking.source.url, note:"榜单数据源"});

  return {answer:text, citations, chart};
}
