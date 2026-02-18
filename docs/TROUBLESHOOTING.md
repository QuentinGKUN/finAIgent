# 故障排查指南

## GLM API错误

### 错误：401 - 令牌已过期或验证不正确

**错误信息**：
```
GLM返回异常: {"error":{"code":"401","message":"令牌已过期或验证不正确"}}
```

**可能原因**：
1. GLM_KEY未配置或配置错误
2. API Key已过期
3. API Key格式不正确（可能包含多余空格）
4. 前端传入的API Key无效

**解决方案**：

#### 方案1：检查.env配置文件
1. 确认 `server/.env` 文件存在
2. 检查 `GLM_KEY` 配置项是否正确设置
3. 确保API Key没有多余的空格或引号

```env
# 正确格式
GLM_KEY=your_api_key_here

# 错误格式（不要加引号）
GLM_KEY="your_api_key_here"
```

#### 方案2：检查前端设置
1. 打开浏览器访问 http://localhost:5173
2. 点击右上角【设置】
3. 检查GLM API Key是否正确填写
4. 确保API Key有效且未过期

#### 方案3：获取新的API Key
1. 访问 [智谱AI开放平台](https://open.bigmodel.cn/)
2. 登录账号
3. 进入API密钥管理
4. 创建新的API Key或刷新现有Key
5. 更新 `.env` 文件中的 `GLM_KEY`

#### 方案4：验证API Key格式
智谱AI的API Key通常是：
- 格式：`xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`（32位字符串）
- 不要包含空格
- 不要包含引号

### 错误：429 - 调用频率超限

**错误信息**：
```
GLM API调用频率超限（429）: 请求过于频繁
```

**解决方案**：
1. 等待一段时间后重试
2. 检查API套餐的调用限制
3. 考虑升级API套餐

### 错误：API Key未设置

**错误信息**：
```
GLM API Key未设置，请在.env文件中配置GLM_KEY或在前端设置
```

**解决方案**：
1. 创建 `server/.env` 文件
2. 从 `server/ENV_EXAMPLE.txt` 复制模板
3. 填写 `GLM_KEY=your_api_key_here`
4. 重启服务

## Tushare API错误

### 错误：Tushare token未配置

**错误信息**：
```
Tushare token未配置
```

**解决方案**：
1. 如果使用本地JSON文件，可以忽略此错误
2. 如果需要使用Tushare API，在 `.env` 文件中设置：
   ```env
   TUSHARE_TOKEN=your_tushare_token
   ```

### 错误：Tushare API调用失败

**可能原因**：
1. Token无效或已过期
2. API URL不可访问
3. 网络连接问题

**解决方案**：
1. 检查 `TUSHARE_TOKEN` 是否正确
2. 检查 `TUSHARE_API_URL` 是否可访问
3. 检查网络连接
4. 查看控制台详细错误信息

## SEC API错误

### 错误：SEC API调用失败

**可能原因**：
1. SEC_USER_AGENT未正确设置
2. 网络连接问题
3. SEC服务器临时不可用

**解决方案**：
1. 确保 `.env` 文件中的 `SEC_USER_AGENT` 已设置
2. 格式：`FinAssistantChampion/1.0 (email: your_email@example.com)`
3. 使用真实的邮箱地址
4. 检查网络连接

## 常见问题

### Q1: 如何检查配置是否正确？

**A**: 启动服务时，系统会自动验证配置：
- 如果缺少必需配置，会在控制台显示错误
- 检查 `server/.env` 文件是否存在且格式正确

### Q2: 前端设置了API Key，为什么还是报错？

**A**: 检查以下几点：
1. 前端设置的API Key格式是否正确
2. API Key是否有效（未过期）
3. 网络请求是否成功发送到后端

### Q3: 如何确认API Key是否有效？

**A**: 可以手动测试：
```bash
curl -X POST https://open.bigmodel.cn/api/paas/v4/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "glm-4-flash",
    "messages": [{"role": "user", "content": "测试"}]
  }'
```

如果返回401，说明API Key无效。

### Q4: 配置文件在哪里？

**A**: 
- 配置文件：`server/.env`
- 示例文件：`server/ENV_EXAMPLE.txt`
- 如果 `.env` 不存在，从 `ENV_EXAMPLE.txt` 复制并重命名

### Q5: 修改配置后需要重启服务吗？

**A**: 是的，修改 `.env` 文件后需要重启Express服务才能生效。

## 调试技巧

1. **查看控制台日志**：启动服务时查看是否有配置错误提示
2. **检查环境变量**：确认 `.env` 文件被正确加载
3. **测试API连接**：使用curl或Postman测试API Key是否有效
4. **查看网络请求**：在浏览器开发者工具中查看API请求和响应

## 获取帮助

如果问题仍未解决：
1. 检查控制台完整错误信息
2. 查看 `docs/CONFIG.md` 配置说明
3. 查看 `docs/TUSHARE_INTEGRATION.md` Tushare集成说明
