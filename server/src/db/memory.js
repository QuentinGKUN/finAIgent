// sql.js 版本：用 prepare + run
export function migrateMemory(db){
    db.run(`
      CREATE TABLE IF NOT EXISTS memories(
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        session_id TEXT NOT NULL,
        key TEXT NOT NULL,
        value TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        UNIQUE(session_id, key)
      );
    `);
  }
  
  export function upsertMemory(db, sessionId, key, value){
    // upsert：先删再插（sql.js 简化）
    const del = db.prepare(`DELETE FROM memories WHERE session_id=? AND key=?`);
    del.run([sessionId, key]);
    del.free();
  
    const ins = db.prepare(`INSERT INTO memories(session_id, key, value, updated_at) VALUES(?,?,?,?)`);
    ins.run([sessionId, key, value, new Date().toISOString()]);
    ins.free();
  }
  
  export function getMemories(db, sessionId){
    const stmt = db.prepare(`SELECT key, value FROM memories WHERE session_id=?`);
    stmt.bind([sessionId]);
    const out = {};
    while(stmt.step()){
      const r = stmt.getAsObject();
      out[r.key] = r.value;
    }
    stmt.free();
    return out;
  }
  