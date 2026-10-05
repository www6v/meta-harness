# Codex MCP 和 Credential 功能集成方案

> **生成日期：** 2026-10-05  
> **基于：** meta-harness 代码分析 + Codex 本地代码分析  
> **目标机器：** 124.221.28.203

---

## 执行摘要

本方案详细说明如何将 Codex 的 MCP 和 Credential 功能集成到 meta-harness 系统中，包括：
- 现有架构分析
- 冲突点识别
- 实施方案设计
- 具体实施计划

**关键决策：**
1. ✅ 凭证管理：保留 meta-harness Vault 系统
2. ✅ MCP 配置来源：采用混合模式（全局 + 租户 + Agent 三层配置）
3. ✅ 线程同步：实现 thread/list 和 thread/read 接口

**预计工期：** 3.5 天  
**改动量：** ~800 行代码  
**风险等级：** 低

---

## 目录

1. [现有架构分析](#1-现有架构分析)
2. [Codex MCP 功能清单](#2-codex-mcp-功能清单)
3. [冲突分析](#3-冲突分析)
4. [方案选择](#4-方案选择)
5. [最终架构设计](#5-最终架构设计)
6. [详细实施计划](#6-详细实施计划)
7. [关键 Codex API 清单](#7-关键-codex-api-清单)
8. [风险评估](#8-风险评估)
9. [时间线](#9-时间线)

---

## 1. 现有架构分析

### 1.1 Meta-harness 已实现的功能

#### MCP 代理系统 (`internal/mcpproxy/`)
- 自动凭证注入
- 支持 OAuth 流程（RFC 9728 发现 + RFC 7591 动态注册）
- Vault 凭证存储系统

#### 凭证/认证系统
- Vault 管理（多租户隔离）
- OAuth token 存储和自动刷新
- 支持多种认证类型（bearer_token, access_token 等）

#### Codex 集成 (`internal/harness/codex_client.go`)
- 通过 Python Bridge 与 Codex app-server 通信
- 已使用 8 个 ClientRequest 方法（占 ~120 个的 7%）
- 支持 SSE 流式传输

### 1.2 当前 Codex 使用情况

**已使用的 ClientRequest 方法：**
1. `initialize` - 建立连接握手
2. `thread/start` - 创建线程
3. `turn/start` - 启动 turn
4. `turn/interrupt` - 中断 turn（仅 TS 子代理）
5. `fs/readFile` - 读取文件
6. `fs/writeFile` - 写入文件
7. `fs/createDirectory` - 创建目录
8. `thread/items/list` - 列出线程项目

**使用率统计：**
- ClientRequest: 8/~120 (7%)
- ServerNotification: 12/~60 (20%)
- ServerRequest: 0 (Python Bridge 未处理)

---

## 2. Codex MCP 功能清单

### 2.1 核心 MCP API 接口

从 `codex-rs/app-server-protocol/src/protocol/common.rs` 提取：

| 接口 | 方法名 | 功能 | 代码位置 |
|------|--------|------|---------|
| **OAuth 登录** | `mcpServer/oauth/login` | 启动 MCP 服务器 OAuth 流程 | `common.rs:322` |
| **OAuth 完成通知** | `mcpServer/oauthLogin/completed` | OAuth 登录完成通知 | `common.rs:345` |
| **MCP 重载** | `config/mcpServer/reload` | 重新加载 MCP 服务器配置 | `common.rs:326` |
| **状态列表** | `mcpServerStatus/list` | 列出所有 MCP 服务器状态 | `common.rs:330` |
| **工具调用** | `mcpServer/tool/call` | 直接调用 MCP 工具 | `common.rs:338` |
| **资源读取** | `mcpServer/resource/read` | 读取 MCP 资源 | `common.rs:334` |
| **事件流开始** | `mcpServer/event/stream/start` | 启动 MCP 事件流（实验性） | `common.rs:342` |
| **事件流停止** | `mcpServer/event/stream/stop` | 停止 MCP 事件流 | `common.rs:346` |
| **引出请求** | `mcpServer/elicitation/request` | MCP 服务器引出请求 | `common.rs:350` |

### 2.2 线程管理 API

| 接口 | 方法名 | 功能 |
|------|--------|------|
| **列出线程** | `thread/list` | 列出所有线程 |
| **读取线程** | `thread/read` | 读取线程详情 |
| **分叉线程** | `thread/fork` | 从某 turn 分叉探索 |
| **恢复线程** | `thread/resume` | 恢复线程 |
| **归档线程** | `thread/archive` | 归档线程 |
| **删除线程** | `thread/delete` | 删除线程 |

### 2.3 凭证存储实现

从 `codex-rs/rmcp-client/src/oauth.rs`:

```rust
// 核心数据结构
pub struct StoredOAuthTokens {
    access_token: String,
    refresh_token: Option<String>,
    expires_at: Option<i64>,
    token_type: String,
    scope: Option<String>,
}

pub struct StoredOAuthCredentialSnapshot {
    credentials: StoredOAuthTokens,
    store_was_contended: bool,
}

// 关键函数
pub fn stored_oauth_credentials(...) -> Result<StoredOAuthTokens>
pub fn stored_oauth_credential_snapshot(...) -> Result<StoredOAuthCredentialSnapshot>
```

**存储位置：** Codex 使用 keyring-store（系统密钥环）或文件存储（`/home/codex/.codex/`）

---

## 3. 冲突分析

### 3.1 冲突点 1：MCP 凭证管理

| 方面 | Meta-harness 现有 | Codex 提供 | 冲突程度 |
|------|------------------|-----------|---------|
| **存储位置** | MySQL `credentials` 表（加密） | 系统 keyring 或本地文件 | 🔴 **高** |
| **OAuth 流程** | RFC 9728/7591 完整实现 | 内置 OAuth 实现 | 🔴 **高** |
| **凭证注入** | MCP Proxy 自动注入 | Codex 内部管理 | 🟡 **中** |
| **多租户** | 完整的 tenant 隔离 | 单用户模式 | 🔴 **高** |

**结论：** 如果完全使用 Codex 的凭证系统，会破坏 meta-harness 的多租户架构。

### 3.2 冲突点 2：MCP 服务器配置

| 方面 | Meta-harness | Codex | 冲突程度 |
|------|--------------|-------|---------|
| **配置位置** | Agent 配置 JSON 的 `mcp_servers` 字段 | Codex 配置文件 (`config.toml`) | 🟡 **中** |
| **动态更新** | 需要重启 session | `config/mcpServer/reload` 热重载 | 🟢 **低**（Codex 更优） |
| **状态查询** | 无 | `mcpServerStatus/list` | 🟢 **低**（Codex 更优） |

### 3.3 冲突点 3：架构模式

```
Meta-harness 架构:
  Agent → MCP Proxy (Go) → 外部 MCP 服务器
           ↑
       Vault 凭证注入

Codex 架构:
  Agent → Codex MCP Runtime → 外部 MCP 服务器
              ↑
          内置 OAuth + keyring
```

**问题：** 两套系统同时存在会导致：
- 用户需要在两个地方配置 MCP 服务器
- 凭证不共享
- 调试复杂

---

## 4. 方案选择

### 4.1 凭证管理：选择 A（保留 meta-harness Vault）

**理由：**
- ✅ 保持多租户隔离
- ✅ 保护现有投资（Vault、OAuth 流程）
- ✅ 统一的凭证管理界面
- ✅ 符合 SaaS 安全要求

**影响：**
- Codex 不直接管理凭证
- 通过 MCP Proxy 注入凭证
- 需要 Codex 配置为使用外部凭证端点

### 4.2 MCP 配置来源：选择 C（混合模式）

**三层配置架构：**

```
┌─────────────────────────────────────────────────────┐
│  Layer 1: 全局默认配置（Codex config.toml）         │
│  - 公共 MCP 服务器（GitHub, Linear, Slack）         │
│  - 所有租户可用                                     │
└─────────────────────────────────────────────────────┘
                      ↓ 继承
┌─────────────────────────────────────────────────────┐
│  Layer 2: 租户级配置（meta-harness Vault）          │
│  - 租户特定的 MCP 服务器                            │
│  - 租户特定的凭证                                   │
└─────────────────────────────────────────────────────┘
                      ↓ 覆盖
┌─────────────────────────────────────────────────────┐
│  Layer 3: Agent 级配置（Agent JSON）                │
│  - Agent 特定的 MCP 服务器                          │
│  - 覆盖全局和租户级配置                             │
└─────────────────────────────────────────────────────┘
```

**优势：**
- ✅ 兼顾灵活性和便利性
- ✅ 保持多租户隔离
- ✅ 渐进式迁移
- ✅ 热重载 + 动态配置
- ✅ 向后兼容

**配置合并规则：**
1. 同名 MCP 服务器，高优先级覆盖低优先级
2. 不同名的 MCP 服务器，全部保留
3. 凭证始终从 Vault 读取（不受配置层级影响）

### 4.3 线程同步：选择 是

**需要实现的接口：**
- `thread/list` - Session 列表同步
- `thread/read` - Session 详情查看

**可选的高级功能：**
- `thread/fork` - 分叉探索
- `thread/resume` - 跨 session 续接对话
- `turn/steer` - 中途纠正 Agent 方向

---

## 5. 最终架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                      Console UI / API Client                    │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Meta-harness Go Server (:8787)                                 │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Agent API (配置管理)                                   │    │
│  │  - Agent JSON (Agent 级 MCP 配置)                       │    │
│  │  - Vault API (租户级凭证)                               │    │
│  └────────────────────────────────────────────────────────┘    │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Session API (线程同步)                                 │    │
│  │  - thread/list → Session 列表                           │    │
│  │  - thread/read → Session 详情                           │    │
│  └────────────────────────────────────────────────────────┘    │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  MCP Proxy (凭证注入)                                   │    │
│  │  - 从 Vault 读取凭证                                    │    │
│  │  - 自动注入 Authorization header                        │    │
│  └────────────────────────────────────────────────────────┘    │
└──────────────────────────┬──────────────────────────────────────┘
                           │ POST /internal/turn
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Python Bridge (:8092)                                          │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  配置合并逻辑                                           │    │
│  │  1. 全局配置 (Codex config.toml)                        │    │
│  │  2. 租户配置 (从 Go 传入)                               │    │
│  │  3. Agent 配置 (从 Go 传入)                             │    │
│  └────────────────────────────────────────────────────────┘    │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Codex JSON-RPC Client                                  │    │
│  │  - mcpServerStatus/list                                 │    │
│  │  - config/mcpServer/reload                              │    │
│  │  - thread/list, thread/read                             │    │
│  └────────────────────────────────────────────────────────┘    │
└──────────────────────────┬──────────────────────────────────────┘
                           │ WebSocket / SSH Tunnel
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Codex App-Server (:5432 / :8765)                               │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  MCP Runtime (高性能)                                   │    │
│  │  - 连接管理                                              │    │
│  │  - 工具调用                                              │    │
│  │  - 状态监控                                              │    │
│  └────────────────────────────────────────────────────────┘    │
│  ┌────────────────────────────────────────────────────────┐    │
│  │  Thread Manager                                         │    │
│  │  - 线程生命周期                                          │    │
│  │  - 历史记录                                              │    │
│  └────────────────────────────────────────────────────────┘    │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
                    ┌──────────────┐
                    │ 外部 MCP 服务器 │
                    │ (GitHub, etc.) │
                    └──────────────┘
```

---

## 6. 详细实施计划

### Phase 1：基础设施准备（0.5 天）

#### 1.1 Codex 容器全局配置

**文件：** `/data/disk/codex/config.toml`（203 机器）

```toml
# 全局 MCP 服务器配置（所有租户可用）
[mcp_servers.github]
name = "GitHub"
url = "https://mcp.github.com/v1"
type = "url"
scope = "global"

[mcp_servers.linear]
name = "Linear"
url = "https://mcp.linear.app/v1"
type = "url"
scope = "global"

[mcp_servers.slack]
name = "Slack"
url = "https://mcp.slack.com/v1"
type = "url"
scope = "global"

# 禁用 Codex 的凭证存储（使用 meta-harness Vault）
[credentials]
storage_backend = "external"
external_endpoint = "http://meta-harness:8787/v1/mcp-proxy"
```

**改动：**
- 在 203 机器上创建/更新配置文件
- 重启 Codex 容器

#### 1.2 Python Bridge 配置合并模块

**新建文件：** `meta-harness-ext/ssh/config_merger.py`

```python
"""
MCP 配置合并器
优先级：Agent 级 > 租户级 > 全局级
"""

from typing import Dict, List, Any
from dataclasses import dataclass

@dataclass
class MCPServerConfig:
    name: str
    url: str
    type: str = "url"
    scope: str = "agent"  # global | tenant | agent
    authorization_token: str = None
    
    def to_dict(self) -> Dict[str, Any]:
        return {
            "name": self.name,
            "url": self.url,
            "type": self.type,
            "scope": self.scope,
            "authorization_token": self.authorization_token,
        }

class MCPConfigMerger:
    """合并三层 MCP 配置"""
    
    def __init__(self):
        self.global_servers: Dict[str, MCPServerConfig] = {}
        self.load_global_config()
    
    def load_global_config(self):
        """从 Codex config.toml 加载全局配置"""
        import toml
        try:
            with open('/home/codex/.codex/config.toml') as f:
                config = toml.load(f)
                for name, server in config.get('mcp_servers', {}).items():
                    self.global_servers[name] = MCPServerConfig(
                        name=name,
                        url=server['url'],
                        type=server.get('type', 'url'),
                        scope='global',
                    )
        except FileNotFoundError:
            pass
    
    def merge(
        self,
        tenant_servers: List[Dict],
        agent_servers: List[Dict],
    ) -> List[MCPServerConfig]:
        """
        合并三层配置
        优先级：agent > tenant > global
        """
        merged: Dict[str, MCPServerConfig] = {}
        
        # Layer 1: 全局配置
        for name, server in self.global_servers.items():
            merged[name] = server
        
        # Layer 2: 租户配置（覆盖全局）
        for server_dict in tenant_servers:
            name = server_dict['name']
            merged[name] = MCPServerConfig(
                name=name,
                url=server_dict['url'],
                type=server_dict.get('type', 'url'),
                scope='tenant',
                authorization_token=server_dict.get('authorization_token'),
            )
        
        # Layer 3: Agent 配置（覆盖租户和全局）
        for server_dict in agent_servers:
            name = server_dict['name']
            merged[name] = MCPServerConfig(
                name=name,
                url=server_dict['url'],
                type=server_dict.get('type', 'url'),
                scope='agent',
                authorization_token=server_dict.get('authorization_token'),
            )
        
        return list(merged.values())
```

**测试：**
```python
# test_config_merger.py
def test_merge_priority():
    merger = MCPConfigMerger()
    
    tenant_servers = [
        {"name": "github", "url": "https://tenant.github.com/mcp"},
    ]
    agent_servers = [
        {"name": "github", "url": "https://agent.github.com/mcp"},
    ]
    
    result = merger.merge(tenant_servers, agent_servers)
    
    # Agent 级配置应该覆盖租户级
    github = next(s for s in result if s.name == "github")
    assert github.url == "https://agent.github.com/mcp"
    assert github.scope == "agent"
```

### Phase 2：Go 服务端增强（1 天）

#### 2.1 添加 MCP 状态查询接口

**修改文件：** `internal/api/mcp_proxy.go`

```go
// GET /v1/mcp/status?session_id=xxx
func (h *mcpProxyHandler) getStatus(w http.ResponseWriter, r *http.Request) {
    sid := r.URL.Query().Get("session_id")
    if sid == "" {
        http.Error(w, "session_id required", http.StatusBadRequest)
        return
    }
    
    // 获取租户
    tenantID, ok := h.resolveTenant(r, sid)
    if !ok {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    
    // 调用 Codex 的 mcpServerStatus/list
    status, err := h.deps.CodexClient.ListMCPServerStatus(r.Context(), tenantID, sid)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(status)
}

// 注册路由
// GET /v1/mcp/status
mux.HandleFunc("/v1/mcp/status", h.getStatus)
```

**修改文件：** `internal/harness/codex_client.go`

```go
// MCPServerStatus 表示 MCP 服务器状态
type MCPServerStatus struct {
    Name   string `json:"name"`
    Status string `json:"status"` // connected | disconnected | error
    Error  string `json:"error,omitempty"`
}

// ListMCPServerStatus 调用 Codex 的 mcpServerStatus/list
func (c *CodexClient) ListMCPServerStatus(
    ctx context.Context,
    tenantID, sessionID string,
) ([]MCPServerStatus, error) {
    // 通过 Python Bridge 调用
    req := map[string]any{
        "session_id": sessionID,
        "method":     "mcpServerStatus/list",
    }
    
    resp, err := c.callBridge(ctx, req)
    if err != nil {
        return nil, err
    }
    
    var result struct {
        Servers []MCPServerStatus `json:"servers"`
    }
    if err := json.Unmarshal(resp, &result); err != nil {
        return nil, err
    }
    
    return result.Servers, nil
}

// ReloadMCPServers 调用 Codex 的 config/mcpServer/reload
func (c *CodexClient) ReloadMCPServers(
    ctx context.Context,
    sessionID string,
) error {
    req := map[string]any{
        "session_id": sessionID,
        "method":     "config/mcpServer/reload",
    }
    
    _, err := c.callBridge(ctx, req)
    return err
}
```

#### 2.2 添加线程/Session 同步接口

**新建文件：** `internal/api/threads.go`

```go
package api

import (
    "encoding/json"
    "net/http"
)

// ThreadInfo 表示 Codex 线程信息
type ThreadInfo struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Status    string `json:"status"`
    CreatedAt int64  `json:"created_at"`
    UpdatedAt int64  `json:"updated_at"`
}

// GET /v1/threads?tenant_id=xxx
func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
    tenantID := r.URL.Query().Get("tenant_id")
    if tenantID == "" {
        http.Error(w, "tenant_id required", http.StatusBadRequest)
        return
    }
    
    // 调用 Codex 的 thread/list
    threads, err := h.deps.CodexClient.ListThreads(r.Context(), tenantID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(threads)
}

// GET /v1/threads/:id
func (h *Handler) getThread(w http.ResponseWriter, r *http.Request) {
    threadID := r.PathValue("id")
    tenantID := r.URL.Query().Get("tenant_id")
    
    thread, err := h.deps.CodexClient.ReadThread(r.Context(), tenantID, threadID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(thread)
}

// 注册路由
// GET /v1/threads
mux.HandleFunc("/v1/threads", h.listThreads)
// GET /v1/threads/:id
mux.HandleFunc("/v1/threads/{id}", h.getThread)
```

**修改文件：** `internal/harness/codex_client.go`

```go
// ListThreads 调用 Codex 的 thread/list
func (c *CodexClient) ListThreads(
    ctx context.Context,
    tenantID string,
) ([]ThreadInfo, error) {
    req := map[string]any{
        "method": "thread/list",
    }
    
    resp, err := c.callBridge(ctx, req)
    if err != nil {
        return nil, err
    }
    
    var result struct {
        Threads []ThreadInfo `json:"threads"`
    }
    if err := json.Unmarshal(resp, &result); err != nil {
        return nil, err
    }
    
    return result.Threads, nil
}

// ReadThread 调用 Codex 的 thread/read
func (c *CodexClient) ReadThread(
    ctx context.Context,
    tenantID, threadID string,
) (*ThreadInfo, error) {
    req := map[string]any{
        "method": "thread/read",
        "params": map[string]any{
            "thread_id": threadID,
        },
    }
    
    resp, err := c.callBridge(ctx, req)
    if err != nil {
        return nil, err
    }
    
    var thread ThreadInfo
    if err := json.Unmarshal(resp, &thread); err != nil {
        return nil, err
    }
    
    return &thread, nil
}
```

### Phase 3：Python Bridge 增强（1 天）

#### 3.1 添加通用 JSON-RPC 调用方法

**修改文件：** `meta-harness-ext/ssh/codex_bridge.py`

```python
@app.post("/codex/rpc")
async def codex_rpc(request: dict):
    """
    通用 JSON-RPC 调用端点
    用于调用 Codex 的各种 API（mcpServerStatus/list, thread/list 等）
    """
    session_id = request.get("session_id")
    method = request.get("method")
    params = request.get("params", {})
    
    if not method:
        return JSONResponse({"error": "method required"}, status_code=400)
    
    # 获取或创建 Codex 客户端
    client = get_or_create_client(session_id)
    
    try:
        # 发送 JSON-RPC 请求
        response = await client.call_method(method, params)
        return JSONResponse({"result": response})
    except Exception as e:
        logger.error(f"Codex RPC error: {e}")
        return JSONResponse({"error": str(e)}, status_code=500)

class CodexClient:
    async def call_method(self, method: str, params: dict) -> dict:
        """调用 Codex JSON-RPC 方法"""
        request = {
            "jsonrpc": "2.0",
            "id": next_id(),
            "method": method,
            "params": params,
        }
        
        await self.ws.send(json.dumps(request))
        response = await self.ws.recv()
        result = json.loads(response)
        
        if "error" in result:
            raise Exception(result["error"]["message"])
        
        return result.get("result", {})
```

#### 3.2 集成配置合并器

**修改文件：** `meta-harness-ext/ssh/codex_bridge.py`

```python
from config_merger import MCPConfigMerger

config_merger = MCPConfigMerger()

@app.post("/codex/turn")
async def codex_turn(request: dict):
    session_id = request.get("session_id")
    agent = request.get("agent", {})
    
    # 提取 MCP 配置
    tenant_servers = request.get("tenant_mcp_servers", [])
    agent_servers = agent.get("mcp_servers", [])
    
    # 合并配置
    merged_servers = config_merger.merge(tenant_servers, agent_servers)
    
    # 使用合并后的配置执行 turn
    events = await execute_turn(
        session_id=session_id,
        mcp_servers=[s.to_dict() for s in merged_servers],
        ...
    )
    
    return {"events": events}
```

### Phase 4：Console UI 增强（0.5 天）

#### 4.1 MCP 状态显示组件

**新建文件：** `console/src/components/MCPStatusPanel.tsx`

```tsx
import { useQuery } from '@tanstack/react-query';

export function MCPStatusPanel({ sessionId }: { sessionId: string }) {
  const { data: status, isLoading } = useQuery({
    queryKey: ['mcp-status', sessionId],
    queryFn: async () => {
      const res = await fetch(`/v1/mcp/status?session_id=${sessionId}`);
      return res.json();
    },
    refetchInterval: 5000, // 每 5 秒刷新
  });
  
  if (isLoading) return <div>Loading...</div>;
  
  return (
    <div className="mcp-status-panel">
      <h3>MCP Servers</h3>
      {status?.map(server => (
        <div key={server.name} className="server-row">
          <span className="server-name">{server.name}</span>
          <span className={`status-badge ${server.status}`}>
            {server.status}
          </span>
          {server.error && (
            <span className="error-text">{server.error}</span>
          )}
        </div>
      ))}
    </div>
  );
}
```

#### 4.2 Session 列表同步

**修改文件：** `console/src/pages/SessionsPage.tsx`

```tsx
import { useQuery } from '@tanstack/react-query';

export function SessionsPage() {
  // 从 Codex 同步线程列表
  const { data: threads } = useQuery({
    queryKey: ['threads', tenantId],
    queryFn: async () => {
      const res = await fetch(`/v1/threads?tenant_id=${tenantId}`);
      return res.json();
    },
  });
  
  return (
    <div>
      <h1>Sessions</h1>
      {threads?.map(thread => (
        <Link to={`/sessions/${thread.id}`} key={thread.id}>
          <div className="session-card">
            <h3>{thread.name || thread.id}</h3>
            <span className="status">{thread.status}</span>
            <span className="date">
              {new Date(thread.updated_at).toLocaleDateString()}
            </span>
          </div>
        </Link>
      ))}
    </div>
  );
}
```

### Phase 5：测试和文档（0.5 天）

#### 5.1 集成测试

**新建文件：** `internal/api/mcp_status_test.go`

```go
func TestMCPStatus(t *testing.T) {
    // 测试 MCP 状态查询
    req := httptest.NewRequest("GET", "/v1/mcp/status?session_id=sess-123", nil)
    req.Header.Set("x-api-key", "test-key")
    
    w := httptest.NewRecorder()
    handler.getStatus(w, req)
    
    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
    
    var status []MCPServerStatus
    json.Unmarshal(w.Body.Bytes(), &status)
    
    if len(status) == 0 {
        t.Error("expected at least one MCP server")
    }
}
```

#### 5.2 用户文档

**新建文件：** `docs/MCP_CONFIGURATION.md`

```markdown
# MCP 配置指南

## 配置层级

Meta-harness 支持三层 MCP 配置，优先级从高到低：

### 1. Agent 级配置（最高优先级）

在 Agent 配置 JSON 中设置：

```json
{
  "mcp_servers": [
    {
      "name": "custom-tool",
      "url": "https://custom.example.com/mcp"
    }
  ]
}
```

**适用场景：** Agent 特定的 MCP 服务器

### 2. 租户级配置

通过 Vault API 设置：

```bash
POST /v1/vaults/:id/credentials
{
  "display_name": "Internal Wiki",
  "auth": {
    "type": "mcp_oauth",
    "mcp_server_url": "https://wiki.company.com/mcp"
  }
}
```

**适用场景：** 租户特定的 MCP 服务器

### 3. 全局配置（最低优先级）

由管理员在 Codex 配置文件中设置：

```toml
[mcp_servers.github]
url = "https://mcp.github.com/v1"
```

**适用场景：** 所有租户共享的公共 MCP 服务器

## 配置合并规则

1. 同名 MCP 服务器，高优先级覆盖低优先级
2. 不同名的 MCP 服务器，全部保留
3. 凭证始终从 Vault 读取（不受配置层级影响）

## 查看 MCP 状态

```bash
GET /v1/mcp/status?session_id=xxx
```

返回所有 MCP 服务器的连接状态。
```

---

## 7. 关键 Codex API 清单

### 必须实现（P0）

| API | 用途 | 调用位置 |
|-----|------|---------|
| `mcpServerStatus/list` | 查询 MCP 服务器状态 | Go → Bridge → Codex |
| `config/mcpServer/reload` | 热重载 MCP 配置 | Go → Bridge → Codex |
| `thread/list` | 列出所有线程 | Go → Bridge → Codex |
| `thread/read` | 读取线程详情 | Go → Bridge → Codex |

### 可选实现（P1）

| API | 用途 | 调用位置 |
|-----|------|---------|
| `turn/steer` | 中途纠正 Agent 方向 | Console UI → Go → Bridge |
| `thread/fork` | 分叉线程探索 | Console UI → Go → Bridge |
| `account/rateLimits/read` | 显示额度信息 | Console UI → Go → Bridge |
| `mcpServer/tool/call` | 直接调用 MCP 工具 | Console UI → Go → Bridge |

---

## 8. 风险评估

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|---------|
| Codex 配置合并逻辑复杂 | 中 | 中 | 充分的单元测试 + 日志记录 |
| 线程同步性能问题 | 低 | 中 | 缓存 + 增量同步 |
| 向后兼容性问题 | 低 | 高 | 渐进式迁移 + 功能开关 |
| Codex API 变更 | 低 | 中 | 抽象层隔离 + 版本检查 |
| Python Bridge 稳定性 | 中 | 高 | 健康检查 + 自动重启 |

---

## 9. 时间线

### 详细时间线

| 阶段 | 任务 | 工期 | 交付物 | 依赖 |
|------|------|------|--------|------|
| **Phase 1** | 基础设施准备 | 0.5 天 | 全局配置 + 配置合并器 | 无 |
| **Phase 2** | Go 服务端增强 | 1 天 | MCP 状态 API + 线程同步 API | Phase 1 |
| **Phase 3** | Python Bridge 增强 | 1 天 | 通用 RPC 调用 + 配置合并 | Phase 1 |
| **Phase 4** | Console UI 增强 | 0.5 天 | 状态面板 + Session 列表 | Phase 2 |
| **Phase 5** | 测试和文档 | 0.5 天 | 集成测试 + 用户文档 | Phase 2-4 |
| **总计** | | **3.5 天** | **完整功能** | |

### 里程碑

**M1（第 1 天结束）：** 基础设施就绪
- [ ] Codex 全局配置完成
- [ ] 配置合并器测试通过

**M2（第 2 天结束）：** Go 服务端完成
- [ ] MCP 状态 API 可用
- [ ] 线程同步 API 可用
- [ ] 单元测试通过

**M3（第 3 天结束）：** Bridge 增强完成
- [ ] 通用 RPC 调用可用
- [ ] 配置合并集成完成
- [ ] 集成测试通过

**M4（第 3.5 天结束）：** 全部完成
- [ ] Console UI 更新完成
- [ ] 文档编写完成
- [ ] E2E 测试通过

### 下一步行动

1. **立即可做（今天）：**
   - 在 203 机器上创建全局 `config.toml`
   - 创建 `config_merger.py` 模块

2. **本周完成：**
   - Phase 1 + Phase 2（基础设施 + Go 服务端）

3. **下周完成：**
   - Phase 3 + Phase 4 + Phase 5（Bridge + UI + 测试）

4. **验证标准：**
   - MCP 状态可以在 Console 显示
   - Session 列表可以从 Codex 同步
   - 配置合并逻辑正确工作
   - 现有功能不受影响

---

## 附录

### A. 相关代码文件

**Meta-harness 关键文件：**
- `internal/harness/codex_client.go` - Codex 客户端
- `internal/api/mcp_proxy.go` - MCP 代理
- `internal/store/vaults.go` - Vault 存储
- `internal/store/credentials.go` - 凭证存储

**Codex 关键文件：**
- `codex-rs/app-server-protocol/src/protocol/common.rs` - API 定义
- `codex-rs/rmcp-client/src/oauth.rs` - OAuth 实现
- `codex-rs/codex-mcp/src/connection_manager.rs` - 连接管理

**Python Bridge 关键文件：**
- `meta-harness-ext/ssh/codex_bridge.py` - HTTP→WebSocket 桥接
- `meta-harness-ext/ssh/codex_client.py` - JSON-RPC 客户端

### B. 配置示例

**Agent 配置示例：**
```json
{
  "id": "agent-123",
  "name": "My Agent",
  "model": "qwen3.7-plus",
  "mcp_servers": [
    {
      "name": "github",
      "type": "url",
      "url": "https://mcp.github.com/v1"
    },
    {
      "name": "custom-tool",
      "type": "url",
      "url": "https://custom.example.com/mcp"
    }
  ]
}
```

**全局配置示例（`config.toml`）：**
```toml
[mcp_servers.github]
name = "GitHub"
url = "https://mcp.github.com/v1"
type = "url"
scope = "global"

[mcp_servers.linear]
name = "Linear"
url = "https://mcp.linear.app/v1"
type = "url"
scope = "global"
```

### C. API 参考

**新增 API 端点：**

```
GET /v1/mcp/status?session_id=xxx
  描述：查询 MCP 服务器状态
  响应：[{name, status, error}]

GET /v1/threads?tenant_id=xxx
  描述：列出所有线程
  响应：[{id, name, status, created_at, updated_at}]

GET /v1/threads/:id?tenant_id=xxx
  描述：读取线程详情
  响应：{id, name, status, created_at, updated_at}

POST /v1/mcp/reload
  描述：热重载 MCP 配置
  响应：{success: true}
```

---

**文档版本：** v1.0  
**最后更新：** 2026-10-05  
**维护者：** Meta-harness Team
