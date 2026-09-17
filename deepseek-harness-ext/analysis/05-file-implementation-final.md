# 文件上传和会话输出下载实现文档

## 概述

本次实现将 meta-harness console 的文件上传和会话输出下载功能迁移到混合架构：
- **文件上传**：使用 deepseek-harness 的 `POST /api/session/uploadFileBinary` RPC
- **会话输出列表和下载**：使用 oma-server 的 `/v1/files` REST API

## 架构说明

### 为什么使用混合架构？

1. **文件上传（用户附件）**：
   - deepseek-harness 有专门的文件上传 RPC 接口
   - 支持二进制上传和 base64 上传
   - 文件由 deepseek-harness 管理，用于 LLM 上下文附件

2. **会话输出文件**：
   - 由 oma-server 在本地文件系统管理（`./data/session-outputs/{tenant}/{session}/`）
   - oma-server 的 `/v1/files` API 原生支持 `scope_id` 参数来列出 session 相关文件
   - 不需要通过 deepseek-harness RPC 访问

### 数据流

```
浏览器
  │
  ├─ 文件上传 → /api/session/uploadFileBinary → DSH 代理 → deepseek-harness (3080)
  │                                              └─ 注入 auth cookie
  │
  └─ 会话输出 → /v1/files?scope_id={sessionId} → oma-server (8787)
     列表/下载                           └─ 读取本地文件系统
```

## 变更内容

### 1. 修改文件

**`console/vite.config.ts`**
```typescript
// API_TARGET points to oma-server (port 8787), which has a DSH proxy that
// forwards /api/* requests to deepseek-harness (port 3080) with auth cookies.
const API_TARGET = process.env.VITE_API_TARGET || "http://localhost:8787";
```

**`console/src/lib/deepseek-rpc-client.ts`**
- `listSessionOutputs(sessionId)`：使用 `GET /v1/files?scope_id={sessionId}`
- `readSessionOutputFileBytes(sessionId, filename)`：使用 `GET /v1/files/out:{sessionId}:{filename}/content`

### 2. 文件上传流程

```typescript
// SessionDetail.tsx - 文件上传
const rpcClient = new DeepSeekRpcClient(window.location.origin);

// 用户上传文件
const result = await rpcClient.uploadFileBinary(file, sessionId, file.name);
// 返回：{ receiptId, file: { attachmentId, name, bytes } }

// 发送 attachmentId 给 LLM
send({
  text: message,
  attachments: [{ id: result.file.attachmentId, name: file.name }]
});
```

### 3. 会话输出列表流程

```typescript
// Panels.tsx - FilesPanel 组件
const rpcClient = new DeepSeekRpcClient(window.location.origin);

// 列出 session 输出文件
const files = await rpcClient.listSessionOutputs(sessionId);
// 返回：[{ filename, size_bytes, uploaded_at, media_type }]
```

### 4. 会话输出下载流程

```typescript
// Panels.tsx - 下载处理
const handleDownload = async (filename: string) => {
  const result = await rpcClient.readSessionOutputFileBytes(sessionId, filename);
  // result: { bytes: Uint8Array, mediaType: string }
  
  const blob = new Blob([result.bytes], { type: result.mediaType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
};
```

## API 对照表

| 功能 | 原 REST API | 新 API | 说明 |
|------|------------|--------|------|
| 文件上传（用户附件） | `POST /v1/files` | `POST /api/session/uploadFileBinary` | deepseek-harness RPC |
| 会话输出列表 | `GET /v1/sessions/{id}/outputs` | `GET /v1/files?scope_id={id}` | oma-server API |
| 会话输出下载 | `GET /v1/sessions/{id}/outputs/{filename}` | `GET /v1/files/out:{id}:{filename}/content` | oma-server API |

## 认证说明

### 文件上传（deepseek-harness）
- DSH 代理自动从 `$DSH_HOME/.credentials.yaml` 读取认证配置
- 生成 HMAC-SHA256 签名的 cookie
- 注入到转发请求中

### 会话输出（oma-server）
- 使用浏览器 cookie session 认证
- 通过 `/auth/refresh` 获取 session cookie
- 请求自动携带 cookie（`credentials: "include"`）

## 验证步骤

### 1. 启动服务

```bash
# 启动 oma-server
cd C:\mine\workspacePy\meta-harness\meta-harness
.\oma-server.exe

# 启动 console 开发服务器
cd console
npm run dev
```

### 2. 验证文件上传

1. 打开浏览器访问 http://localhost:5173
2. 创建新会话
3. 点击 "+" 按钮上传文件
4. 打开开发者工具 → Network 标签
5. 验证请求：
   - URL: `POST /api/session/uploadFileBinary`
   - 响应：包含 `receiptId` 和 `attachmentId`

### 3. 验证会话输出列表

1. 在会话中让 agent 写入文件：
   ```
   Please write a summary to /mnt/session/outputs/summary.txt
   ```
2. 点击 "Output Files" 按钮
3. 验证请求：
   - URL: `GET /v1/files?scope_id={sessionId}`
   - 响应：文件列表数组

### 4. 验证会话输出下载

1. 在文件列表中点击文件名
2. 验证请求：
   - URL: `GET /v1/files/out:{sessionId}:{filename}/content`
   - 响应：文件二进制内容
3. 检查下载是否成功
4. 检查 UI 是否显示 "Downloading…" 状态

## 注意事项

1. **文件上传路径**：
   - 用户上传的文件保存在 deepseek-harness 的文件系统中
   - 用于 LLM 上下文附件

2. **会话输出路径**：
   - 会话输出保存在 `./data/session-outputs/{tenant}/{session}/`
   - 由 oma-server 管理

3. **认证配置**：
   - 确保 `$DSH_HOME/.credentials.yaml` 存在且有效
   - 浏览器 cookie session 正常工作

4. **错误处理**：
   - 404：文件不存在或 session 不存在
   - 401：认证失败（cookie 过期或无效）
   - 502：deepseek-harness 不可用

## 相关文档

- `deepseek-harness-ext/analysis/03-typert-rpc-api.md` - Typert RPC 协议文档
- `deepseek-harness-ext/analysis/04-file-rpc-implementation.md` - 原始 RPC 实现文档
- `internal/sessionoutputs/store.go` - oma-server session outputs 存储实现
- `internal/api/files.go` - oma-server 文件 API 实现
