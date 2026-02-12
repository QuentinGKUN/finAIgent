import 'dotenv/config';

/**
 * 统一配置管理模块
 * 从环境变量读取所有配置项
 */
export const config = {
  // AI配置
  ai: {
    apiKey: process.env.GLM_KEY || '',
    model: 'glm-4-flash'
  },

  // Tushare配置
  tushare: {
    token: process.env.TUSHARE_TOKEN || '',
    apiUrl: process.env.TUSHARE_API_URL || 'http://api.tushare.pro'
  },

  // SEC配置（美股）
  sec: {
    userAgent: process.env.SEC_USER_AGENT || 'FinAssistantChampion/1.0 (email: you@example.com)'
  },

  // 服务配置
  server: {
    port: parseInt(process.env.PORT || '3000', 10),
    goUrl: process.env.GO_SERVICE_URL || 'http://localhost:3001',
    goPort: parseInt(process.env.GO_PORT || '3001', 10),
    sqlitePath: process.env.SQLITE_PATH || '../data/app.db'
  },

  // 环境
  env: process.env.NODE_ENV || 'development'
};

/**
 * 验证必需的配置项
 */
export function validateConfig() {
  const errors = [];

  if (!config.ai.apiKey) {
    errors.push('GLM_KEY (AI API Key) 未设置');
  }

  // Tushare token是可选的（可以使用本地JSON文件）
  // if (!config.tushare.token) {
  //   errors.push('TUSHARE_TOKEN 未设置（可选，如果使用本地JSON文件则不需要）');
  // }

  if (!config.sec.userAgent || config.sec.userAgent.includes('you@example.com')) {
    console.warn('警告: SEC_USER_AGENT 未正确设置，建议使用你的邮箱地址');
  }

  if (errors.length > 0) {
    console.error('配置错误:');
    errors.forEach(err => console.error(`  - ${err}`));
    console.error('\n请检查 .env 文件配置');
  }

  return errors.length === 0;
}
