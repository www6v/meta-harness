# DeepSeek Harness 认证集成

## 概述

mete-harness 现在可以自动认证并访问 deepseek-harness 的 Web API，无需手动配置。

## 工作原理

1. **凭据读取**：Go 客户端从 `~/.dsh/.credentials.yaml` 读取 HMAC 签名密钥
2. **Cookie 构造**：使用密钥构造符合 deepseek-harness 认证要求的浏览器 cookie
3. **自动携带**：所有 HTTP POST 和 WebSocket 请求都自动携带认证 cookie

## 配置要求

确保 deepseek-harness 已启动并生成了凭据文件：

```bash
# deepseek-harness 启动后会在 ~/.dsh/.credentials.yaml 中生成凭据
# Windows: %USERPROFILE%\.dsh\.credentials.yaml
# macOS/Linux: ~/.dsh/.credentials.yaml
cat ~/.dsh/.credentials.yaml
```

文件应包含 `client-connection/browser-session` 记录：

```yaml
version: 1
records:
  client-connection/browser-session:
    kind: grant
    payload:
      version: 1
      secret: <base64url-encoded-secret>
```

## 环境变量

配置 mete-harness 访问 deepseek-harness：

```bash
# .env 文件
OMA_DEEPSEEK_ENABLED=1
OMA_DEEPSEEK_GATEWAY_URL=http://127.0.0.1:3080
# OMA_DEEPSEEK_TOKEN 不需要，认证通过 cookie 自动处理
```

## 自定义 DSH_HOME

如果 deepseek-harness 的凭据文件不在默认位置，可以设置 `DSH_HOME` 环境变量：

```bash
export DSH_HOME=/path/to/dsh/home
```

## 故障排除

### 401 未授权错误

1. 检查 `~/.dsh/.credentials.yaml` 文件是否存在
2. 确认文件包含 `client-connection/browser-session` 记录
3. 重启 mete-harness 服务器

### Cookie 过期

认证 cookie 有效期为 7 天。如果看到认证错误：
1. 重启 mete-harness 服务器（会重新读取凭据并构造新 cookie）
2. 或等待 deepseek-harness 重启后重新生成凭据

## 技术细节

- Cookie 名称：`dsh-auth-<sha256(authority)>`
- Cookie 值：`v1.<base64url(payload)>.<base64url(hmac-sha256-signature)>`
- Payload 包含：version、authority、issuedAt、expiresAt
- 签名使用：HMAC-SHA256
