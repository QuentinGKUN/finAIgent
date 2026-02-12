export function ensureSession(db, sessionId){
  const stmt = db.prepare("SELECT id FROM sessions WHERE id=?");
  stmt.bind([sessionId]);
  const has = stmt.step();
  stmt.free();

  if(!has){
    const ins = db.prepare("INSERT INTO sessions(id, created_at) VALUES(?,?)");
    ins.run([sessionId, new Date().toISOString()]);
    ins.free();
  }
}

export function appendMessage(db, sessionId, role, content, meta=null){
  const ins = db.prepare(`
    INSERT INTO messages(session_id, role, content, meta_json, created_at)
    VALUES(?,?,?,?,?)
  `);
  ins.run([sessionId, role, content, meta ? JSON.stringify(meta) : null, new Date().toISOString()]);
  ins.free();
}

export function getMessages(db, sessionId, limit=50){
  const stmt = db.prepare(`
    SELECT role, content, created_at
    FROM messages
    WHERE session_id=?
    ORDER BY id ASC
    LIMIT ?
  `);
  stmt.bind([sessionId, limit]);
  const rows = [];
  while(stmt.step()){
    rows.push(stmt.getAsObject());
  }
  stmt.free();
  return rows.map(r=>({role:r.role, content:r.content, created_at:r.created_at}));
}

export function getMessagesWithMeta(db, sessionId, limit=200){
  const stmt = db.prepare(`
    SELECT role, content, meta_json, created_at
    FROM messages
    WHERE session_id=?
    ORDER BY id ASC
    LIMIT ?
  `);
  stmt.bind([sessionId, limit]);
  const rows = [];
  while(stmt.step()){
    rows.push(stmt.getAsObject());
  }
  stmt.free();
  return rows.map(r=>({
    role: r.role,
    content: r.content,
    meta: r.meta_json ? JSON.parse(r.meta_json) : null,
    created_at: r.created_at
  }));
}
