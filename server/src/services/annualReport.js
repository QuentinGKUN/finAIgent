import fetch from "node-fetch";
import { batchEmbed, cosine } from "../llm/embedding.js";

function getCachedEmbedding(db, chunkId){
  const stmt = db.prepare("SELECT embedding_json FROM chunk_embeddings WHERE chunk_id=?");
  stmt.bind([chunkId]);
  let vec = null;
  if(stmt.step()){
    const row = stmt.getAsObject();
    try{ vec = JSON.parse(row.embedding_json); }catch{}
  }
  stmt.free();
  return vec;
}

function upsertEmbedding(db, chunkId, vec){
  const del = db.prepare("DELETE FROM chunk_embeddings WHERE chunk_id=?");
  del.run([chunkId]); del.free();

  const ins = db.prepare("INSERT INTO chunk_embeddings(chunk_id, embedding_json, updated_at) VALUES(?,?,?)");
  ins.run([chunkId, JSON.stringify(vec), new Date().toISOString()]);
  ins.free();
}

export async function ensureAnnualReportIndexed({db, goUrl, secUserAgent, cik}){
  if(!cik) return;

  // 已有则跳过
  const chk = db.prepare("SELECT id FROM annual_report_docs WHERE cik=? LIMIT 1");
  chk.bind([cik]);
  const has = chk.step();
  chk.free();
  if(has) return;

  const resp = await fetch(`${goUrl}/sec/latest10k?cik=${encodeURIComponent(cik)}`, {headers:{'User-Agent':secUserAgent}});
  if(!resp.ok) throw new Error(`年报抓取失败: ${resp.status} ${await resp.text()}`);
  const data = await resp.json();
  if(!data?.filingUrl) return;

  const insDoc = db.prepare(`
    INSERT INTO annual_report_docs(cik,year,form_type,filing_url,fetched_at)
    VALUES(?,?,?,?,?)
  `);
  insDoc.run([cik, data.year, data.formType, data.filingUrl, new Date().toISOString()]);
  insDoc.free();

  // 取 doc_id
  const getDoc = db.prepare("SELECT id FROM annual_report_docs WHERE cik=? ORDER BY id DESC LIMIT 1");
  getDoc.bind([cik]);
  let docId = null;
  if(getDoc.step()) docId = getDoc.getAsObject().id;
  getDoc.free();
  if(!docId) return;

  const ins = db.prepare(`
    INSERT INTO annual_report_chunks(doc_id,cik,year,section,text,source_url,created_at)
    VALUES(?,?,?,?,?,?,?)
  `);

  for(const sec of (data.sections||[])){
    const clean = (sec.text||"").replace(/\s+/g," ").trim();
    if(clean.length < 200) continue;
    for(let i=0;i<clean.length;i+=1100){
      ins.run([docId, cik, data.year, sec.title||null, clean.slice(i,i+1100), sec.url||data.filingUrl, new Date().toISOString()]);
    }
  }
  ins.free();
}

// 简易召回：LIKE（多取一些） + embedding 重排
export async function searchAnnualReportRerank({db, cik, query, limit=6, apiKey, embedModel='text-embedding-004'}){
  if(!cik) return [];
  const q = (query||"").trim();
  const like = `%${q.slice(0,40)}%`;

  const stmt = db.prepare(`
    SELECT id, text, section, year, source_url
    FROM annual_report_chunks
    WHERE cik=? AND text LIKE ?
    ORDER BY year DESC, id DESC
    LIMIT 50
  `);
  stmt.bind([cik, like]);
  const rows = [];
  while(stmt.step()){
    rows.push(stmt.getAsObject());
  }
  stmt.free();

  if(rows.length === 0){
    // 兜底：不带 LIKE 取最新 50
    const s2 = db.prepare(`
      SELECT id, text, section, year, source_url
      FROM annual_report_chunks
      WHERE cik=?
      ORDER BY year DESC, id DESC
      LIMIT 50
    `);
    s2.bind([cik]);
    while(s2.step()) rows.push(s2.getAsObject());
    s2.free();
  }

  if(rows.length <= limit || !apiKey){
    return rows.slice(0,limit).map(r=>({id:r.id,text:r.text,section:r.section,year:r.year,url:r.source_url}));
  }

  const [qVec] = await batchEmbed({apiKey, model: embedModel, texts:[query]});

  const needIds = [];
  const idToRow = new Map();
  const idToVec = new Map();
  for(const r of rows){
    idToRow.set(r.id, r);
    const cached = getCachedEmbedding(db, r.id);
    if(cached) idToVec.set(r.id, cached);
    else needIds.push(r.id);
  }

  if(needIds.length){
    const texts = needIds.map(id=>idToRow.get(id).text);
    const vecs = await batchEmbed({apiKey, model: embedModel, texts});
    for(let i=0;i<needIds.length;i++){
      const id = needIds[i];
      const v = vecs[i] || [];
      if(v.length){
        idToVec.set(id, v);
        upsertEmbedding(db, id, v);
      }
    }
  }

  const scored = rows.map(r=>{
    const v = idToVec.get(r.id);
    const s = v ? cosine(qVec, v) : -1;
    return {r, score:s};
  }).sort((a,b)=>b.score-a.score);

  return scored.slice(0,limit).map(x=>({
    id:x.r.id,
    text:x.r.text,
    section:x.r.section,
    year:x.r.year,
    url:x.r.source_url,
    score:x.score
  }));
}
