export function migrate(db){
  db.run(`
    CREATE TABLE IF NOT EXISTS sessions(
      id TEXT PRIMARY KEY,
      created_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS messages(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id TEXT NOT NULL,
      role TEXT NOT NULL,
      content TEXT NOT NULL,
      meta_json TEXT,
      created_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS annual_report_docs(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      cik TEXT NOT NULL,
      year INTEGER NOT NULL,
      form_type TEXT NOT NULL,
      filing_url TEXT NOT NULL,
      fetched_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS annual_report_chunks(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      doc_id INTEGER NOT NULL,
      cik TEXT NOT NULL,
      year INTEGER NOT NULL,
      section TEXT,
      text TEXT NOT NULL,
      source_url TEXT NOT NULL,
      created_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS chunk_embeddings(
      chunk_id INTEGER PRIMARY KEY,
      embedding_json TEXT NOT NULL,
      updated_at TEXT NOT NULL
    );
  `);
}
