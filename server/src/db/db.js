import fs from "fs";
import path from "path";
import initSqlJs from "sql.js";
import { config } from "../config.js";

// 这个 db.js 会导出一个 async 的 getDb()，其他地方调用 await getDb()
const sqlitePath = config.server.sqlitePath;
const absPath = path.resolve(process.cwd(), sqlitePath);

let _dbPromise = null;

export async function getDb() {
  if (_dbPromise) return _dbPromise;

  _dbPromise = (async () => {
    const SQL = await initSqlJs(); // 自动加载 wasm（npm 包自带）
    fs.mkdirSync(path.dirname(absPath), { recursive: true });

    let db;
    if (fs.existsSync(absPath)) {
      const filebuf = fs.readFileSync(absPath);
      db = new SQL.Database(new Uint8Array(filebuf));
    } else {
      db = new SQL.Database();
    }

    // 持久化：每次写操作后我们手动保存（下面会提供 saveDb）
    db.__absPath = absPath;
    return db;
  })();

  return _dbPromise;
}

export function saveDb(db) {
  const data = db.export();
  fs.mkdirSync(path.dirname(db.__absPath), { recursive: true });
  fs.writeFileSync(db.__absPath, Buffer.from(data));
}
