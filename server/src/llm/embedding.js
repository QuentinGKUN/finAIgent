import fetch from 'node-fetch';

function embedUrl(model, apiKey){
  const m = encodeURIComponent(model);
  return `https://generativelanguage.googleapis.com/v1beta/models/${m}:batchEmbedContents?key=${encodeURIComponent(apiKey)}`;
}

export async function batchEmbed({apiKey, model='text-embedding-004', texts=[]}){
  if(!texts.length) return [];
  const payload = { requests: texts.map(t => ({ content: { parts: [{text: t}] } })) };

  const resp = await fetch(embedUrl(model, apiKey), {
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body: JSON.stringify(payload)
  });

  const raw = await resp.text();
  if(!resp.ok) throw new Error(`Gemini embedding失败: ${resp.status} ${raw}`);
  const data = JSON.parse(raw);
  return (data.embeddings || []).map(e => e.values || []);
}

export function cosine(a,b){
  let dot=0, na=0, nb=0;
  for(let i=0;i<Math.min(a.length,b.length);i++){
    dot += a[i]*b[i];
    na += a[i]*a[i];
    nb += b[i]*b[i];
  }
  if(na===0 || nb===0) return 0;
  return dot / (Math.sqrt(na)*Math.sqrt(nb));
}
