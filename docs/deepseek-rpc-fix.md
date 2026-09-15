# DeepSeek RPC 协议修复

## 问题 1: 404 错误

mete-harness 访问 deepseek-harness 时出现 404 错误。

### 根因

deepseek-harness 的 `endpointFromPath` 函数要求 URL 路径必须包含方法名。

**错误的请求**:
```http
POST /api HTTP/1.1
```

**正确的请求**:
```http
POST /api/session/create HTTP/1.1
```

## 问题 2: gateway/arguments-invalid 错误

mete-harness 的 payload 结构错误，导致 `session.create` 返回参数无效错误。

### 根因

根据 deepseek-harness 的 Typert RPC 协议，`payload` 字段**必须**包含一个 `args` 对象，请求参数应该放在 `args` 对象中。

**错误的请求体**:
```json
{
  "type": "client-request",
  "rpcId": "uuid",
  "method": "session/create",
  "payload": {
    "sessionId": "abc123",
    "cwd": "."
  }
}
```

**正确的请求体**:
```json
{
  "type": "client-request",
  "rpcId": "uuid",
  "method": "session/create",
  "payload": {
    "args": {
      "sessionId": "abc123",
      "cwd": "."
    }
  }
}
```

## 正确的请求格式

根据 Typert RPC 协议，正确的请求格式是：

```http
POST /api/session/create HTTP/1.1
Content-Type: application/json
Cookie: dsh-auth-...=v1....

{
  "type": "client-request",
  "rpcId": "uuid",
  "method": "session/create",
  "payload": {
    "args": {
      "sessionId": "abc123",
      "cwd": "."
    }
  }
}
```

**关键要点**：
1. URL 路径必须包含方法名：`/api/<namespace>/<method>`
2. 方法名使用斜杠：`session/create`
3. payload **必须**包装在 `args` 对象中
4. 方法名同时出现在 URL 和请求体中

## session.create 参数说明

根据 deepseek-harness 的 `SessionCreateRequest` 接口：

| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `sessionId` | string | 否 | 会话 ID，不提供则自动生成 |
| `cwd` | string | 否 | 工作目录，默认使用 Host 的默认目录 |
| `workspaceId` | string | 否 | 工作区 ID，与 `cwd` 互斥 |
| `agentPreset` | string | 否 | Agent 预设名称 |

注意：`cwd` 和 `workspaceId` 不能同时提供。

## 修改的文件

1. **`internal/harness/deepseek_client.go`**
   - 修改 `rpc()` 方法：
     - URL 从 `/api` 改为 `/api/<method>`
     - payload 包装在 `args` 对象中
     - 方法名从 `session.create` 转换为 `session/create`

2. **测试文件更新**
   - 更新测试服务器以处理新的请求格式
   - 更新 payload 解析以从 `args` 中提取参数

## 验证

所有测试通过：
```
✓ TestDeepSeekClient_WithAuth
✓ TestDeepSeekClient_RunTurn
✓ TestDeepSeekClient_RunTurn_RpcError
✓ TestDeepSeekClient_RunTurn_GatewayDown
✓ TestDeepSeekClient_RunTurnStream
PASS
```

## 参考文档

参见 `deepseek-harness-ext/analysis/03-typert-rpc-api.md` 中的 RPC 协议规范。
