# meta-harness 使用的 Codex Harness 接口分析

> 基于 meta-harness 代码 + codex-rust v0.155.1 源码分析
>
> 生成日期：2026-09-25

---

## 1. 总体架构

meta-harness 体系内存在 **三个 Codex app-server 客户端层**，分别用于不同场景：

```
┌─────────────────────────────────────────────────────────────────────┐
│  Layer A: Python Bridge（生产用，meta-harness Go 服务调用）         │
│                                                                     │
│   meta-harness Go (codex_client.go)                                 │
│       │  HTTP POST                                                  │
│       ▼                                                             │
│   Python Bridge (codex_bridge.py :8092)                             │
│       │  WebSocket / SSH Tunnel                                     │
│       ▼                                                             │
│   Codex App-Server (:5432 / :8765)                                  │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Layer B: stdio 子代理（deepseek-harness-old 内部子进程模式）       │
│                                                                     │
│   subagent-codex (wire.ts)                                          │
│       │  stdin/stdout pipes (JSON-RPC line protocol)                │
│       ▼                                                             │
│   `codex app-server --stdio` 子进程                                 │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Layer C: PoC 脚本（验证/调试用）                                   │
│                                                                     │
│   poc_direct.py / poc_tunnel.py                                     │
│       │  WebSocket (直连或 SSH 隧道)                                 │
│       ▼                                                             │
│   Codex App-Server (:8765)                                          │
└─────────────────────────────────────────────────────────────────────┘
```

### 关键文件

| 文件 | 语言 | 角色 |
|---|---|---|
| `meta-harness/internal/harness/codex_client.go` | Go | 生产客户端，调 Python Bridge HTTP（不直接发 JSON-RPC） |
| `meta-harness-ext/ssh/codex_bridge.py` | Python | HTTP→WebSocket 桥接，暴露 `/codex/turn` 给 Go |
| `meta-harness-ext/ssh/codex_client.py` | Python | JSON-RPC WebSocket 客户端（Bridge 内部使用） |
| `deepseek-harness-old/packages/subagent/subagent-codex/src/wire.ts` | TypeScript | **主要直连调用者**，stdio 协议 |
| `deepseek-harness-old/packages/subagent/subagent-codex/src/run.ts` | TypeScript | 生命周期管理，spawn `codex app-server --stdio` |
| `codex-ext/remote/client/poc_direct.py` | Python | PoC：直连 WebSocket |
| `codex-ext/remote/client/poc_tunnel.py` | Python | PoC：SSH 隧道 + WebSocket |
| `codex-ext/analysis/app-server-api-reference.md` | Markdown | 完整 API 参考（~120 方法） |

---

## 2. meta-harness 已使用的 Codex 接口

### 2.1 客户端→服务器请求（ClientRequest）

| 方法 | 使用位置 | 用途 |
|---|---|---|
| `initialize` | `codex_client.py:_initialize()` / `wire.ts:133` | 建立连接握手，发送 `clientInfo` + `capabilities` |
| `thread/start` | `codex_client.py:_start_thread()` / `wire.ts:154` | 创建线程。Python 侧传 `instructions`；TS 侧传 `cwd` + `ephemeral: true` |
| `turn/start` | `codex_client.py:run_turn()` / `wire.ts:180` | 启动 turn，传入 `threadId` 和 `input` |
| `turn/interrupt` | `wire.ts:212` | 最佳努力远程取消运行中的 turn |
| `fs/readFile` | `codex_client.py:_read_file()` | 读取工作区文件（base64），提取 fileChange 产出 |
| `fs/writeFile` | `codex_client.py:_write_file()` | 写入 skill 文件到工作区 |
| `fs/createDirectory` | `codex_client.py:_ensure_directory()` | 递归创建 skill 挂载目录祖先 |
| `thread/items/list` | `codex_client.py:_list_thread_items()` | 读取子代理线程的对话历史 |

### 2.2 客户端通知（ClientNotification）

| 通知 | 使用位置 | 用途 |
|---|---|---|
| `initialized` | `wire.ts:144` | 在 `initialize` 响应后发送，完成握手 |

### 2.3 处理的服务器→客户端请求（ServerRequest）

**wire.ts 子代理模式** 处理了 5 个 ServerRequest（全部自动响应，无人工审批）：

| 方法 | 响应策略 | 说明 |
|---|---|---|
| `item/commandExecution/requestApproval` | `decision: unattendedDecision(params)` | 自动拒绝 shell 命令 |
| `item/fileChange/requestApproval` | `decision: unattendedDecision(params)` | 自动拒绝文件修改 |
| `item/permissions/requestApproval` | `{ permissions: {}, scope: 'turn' }` | 自动批准（空权限 + turn 作用域） |
| `item/tool/requestUserInput` | `{ answers: {} }` | 自动响应空答案 |
| `mcpServer/elicitation/request` | `{ action: 'decline', content: null }` | 自动拒绝 MCP 引出 |

> ⚠️ **Python Bridge 和 Go 侧未处理任何 ServerRequest**——仅 TS 子代理处理。

### 2.4 消费的服务器通知（ServerNotification）

| 通知 | 使用位置 | 用途 |
|---|---|---|
| `item/agentMessage/delta` | `codex_client.py` / PoC | 流式接收代理消息 → `agent.message` |
| `item/reasoning/textDelta` | `codex_client.py` / PoC | 流式接收推理文本 → `agent.thinking` |
| `item/started` | `codex_client.py` | 工具调用开始 → `agent.tool_use` + 子代理生命周期 |
| `item/completed` | `codex_client.py` / `wire.ts:330` | 工具完成 → `agent.tool_result`；TS 侧提取 `agentMessage` 文本（按 `phase` 区分 final_answer/commentary） |
| `item/commandExecution/outputDelta` | `codex_client.py` | 已识别但未处理（MVP 后续） |
| `turn/started` | `codex_client.py` / `wire.ts:321` / PoC | Turn 启动确认，提取 turnId |
| `turn/completed` | `codex_client.py` / `wire.ts:356` / PoC | Turn 结束，提取 `status`/`usage`/`items` |
| `error` | `codex_client.py` / PoC | 错误 → `session.error` |
| `warning` | `codex_client.py` / PoC | 警告仅记日志 |
| `thread/started` | PoC | 已识别静默 |
| `thread/status/changed` | PoC | 已识别静默 |
| `account/rateLimits/updated` | PoC | 已识别静默 |

### 2.5 Go 侧 HTTP 接口（codex_client.go → Bridge）

Go 不直接调用 Codex JSON-RPC，而是通过 Python Bridge：

| 端点 | 位置 | 用途 |
|---|---|---|
| `POST /codex/turn` | `codex_client.go:131` | 批量执行 turn |
| `POST /codex/turn/sse` | `codex_client.go:242` | SSE 流式执行 turn |
| `POST /codex/turn`（fallback） | `codex_client.go:405` | SSE 不可用时回退 |

请求体：`session_id`、`agent`、`events`、`skills`、`sub_agents`
响应体：`events[]`、`usage`、`files[]`、`error`

---

## 3. Codex Harness 未使用的接口

以下是 `app-server-api-reference.md` 中定义但 meta-harness **未调用** 的接口。

### 3.1 线程管理（Thread）— 大部分未使用

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `thread/resume` | 恢复线程 | ⭐ 高 — 跨 session 续接对话 |
| `thread/fork` | 分叉线程 | ⭐ 高 — 从某 turn 分叉探索 |
| `thread/archive` | 归档线程 | 中 — 清理历史 session |
| `thread/unarchive` | 取消归档 | 低 |
| `thread/delete` | 删除线程 | 中 — 清理无用 session |
| `thread/rollback` | 回滚线程 | ⭐ 高 — 撤销错误 turn |
| `thread/revert` | 还原线程 | 中 |
| `thread/compact/start` | 启动上下文压缩 | ⭐ 高 — 长对话节省 token |
| `thread/name/set` | 设置线程名称 | 中 — 给 session 命名 |
| `thread/metadata/update` | 更新元数据 | 低 |
| `thread/settings/update` | 更新设置 | 中 — 动态调整 model/profile |
| `thread/memoryMode/set` | 设置内存模式 | 中 |
| `thread/goal/set` / `get` / `clear` | 目标管理 | ⭐ 高 — 对应 OMA goal |
| `thread/queue/*` (7 个) | 队列管理（实验性） | 中 — 批量 turn 编排 |
| `thread/attachment/*` (3 个) | 附件管理 | 中 — 对应 OMA 文件附件 |
| `thread/section/*` (5 个) | 分区管理 | 低 |
| `thread/shellCommand` | 直接执行 shell | 中 — 绕过 turn 的快捷命令 |
| `thread/approveGuardianDeniedAction` | 批准 Guardian 拒绝 | ⭐ 高 — 权限审批流 |
| `thread/backgroundTerminals/*` (3 个) | 后台终端（实验性） | 低 |
| `thread/increment_elicitation` / `decrement_elicitation` | 引出计数 | 低 |
| `thread/inject_items` | 注入项目 | 中 — 向线程注入上下文 |
| `thread/list` | 列出所有线程 | ⭐ 高 — session 列表同步 |
| `thread/loaded/list` | 列出已加载线程 | 低 |
| `thread/read` | 读取线程详情 | ⭐ 高 — session 详情 |
| `thread/turns/list` | 列出 turn | ⭐ 高 — 历史 turn |
| `thread/timeline/list` | 列出时间线 | 中 |
| `thread/search` | 搜索线程 | ⭐ 高 — 全局搜索 |
| `thread/searchOccurrences` | 搜索出现次数 | 中 |
| `thread/realtime/*` (6 个) | 实时语音（实验性） | 低 — 语音场景暂不需要 |

### 3.2 Turn / 审查

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `turn/steer` | 引导 turn（中途调整方向） | ⭐ 高 — 用户中途纠正代理 |
| `turn/settings/update` | 更新 turn 设置 | 中 |
| `review/start` | 启动审查 | ⭐ 高 — 对应 /code-review |

### 3.3 文件系统

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `fs/getMetadata` | 获取文件元数据 | 中 |
| `fs/readDirectory` | 读取目录 | 中 — 工作区浏览 |
| `fs/remove` | 删除文件/目录 | 低 |
| `fs/copy` | 复制文件/目录 | 低 |
| `fs/watch` / `fs/unwatch` | 监视文件变化 | 中 — 实时同步 |

### 3.4 账户 / 认证

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `account/login/start` / `cancel` | 登录流程 | 中 — 多用户场景 |
| `account/logout` | 登出 | 低 |
| `account/read` | 读取账户信息 | 中 — 显示用户信息 |
| `account/bedrock/discover` / `setup` | Bedrock 配置 | 低 |
| `account/rateLimits/read` | 读取速率限制 | ⭐ 高 — 前端显示额度 |
| `account/rateLimitResetCredit/consume` | 消耗重置额度 | 低 |
| `account/usage/read` | 读取使用量 | ⭐ 高 — 用量统计 |
| `account/workspaceMessages/read` | 读取工作区消息 | 低 |
| `account/sendAddCreditsNudgeEmail` | 发送充值提醒 | 低 |

### 3.5 配置

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `config/read` | 读取配置 | ⭐ 高 — 获取 model/profile |
| `config/value/write` | 写入配置 | 中 — 设置页 |
| `config/batchWrite` | 批量写入 | 低 |
| `configRequirements/read` | 读取配置要求 | 低 |
| `config/mcpServer/reload` | 重新加载 MCP | ⭐ 高 — 动态更新 |

### 3.6 MCP

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `mcpServer/oauth/login` | MCP OAuth 登录 | ⭐ 高 — 对接外部 MCP |
| `mcpServerStatus/list` | 列出 MCP 状态 | 中 |
| `mcpServer/resource/read` | 读取 MCP 资源 | 中 |
| `mcpServer/tool/call` | 调用 MCP 工具 | ⭐ 高 — 绕过 agent 直接调用 |
| `mcpServer/event/stream/start` / `stop` | MCP 事件流（实验性） | 低 |

### 3.7 插件 / 技能 / 应用 / 市场

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `skills/list` | 列出技能 | ⭐ 高 — 与 OMA skills 同步 |
| `skills/extraRoots/set` | 设置额外技能根 | 中 |
| `skills/config/write` | 写入技能配置 | 中 |
| `hooks/list` | 列出钩子 | 中 |
| `model/list` | 列出模型 | ⭐ 高 — 模型选择器 |
| `permissionProfile/list` | 列出权限配置 | 中 |
| `collaborationMode/list` | 列出协作模式 | 低 |
| `plugin/*` (12 个) | 插件管理 | ⭐ 高 — 插件市场 |
| `marketplace/*` (3 个) | 市场管理 | 中 |
| `app/read` / `app/list` / `app/installed` | 应用管理 | 中 |

### 3.8 项目管理（实验性）

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `project/*` (7 个) | 项目 CRUD | ⭐ 高 — 对应 OMA workspace |

### 3.9 进程 / 命令

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `command/exec` | 一次性命令执行 | 中 — 快捷命令 |
| `command/exec/write` / `terminate` / `resize` | 命令控制 | 低 |
| `process/*` (4 个) | 进程管理（实验性） | 低 |

### 3.10 远程控制（全部实验性）

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `remoteControl/*` (7 个) | 远程控制开关/配对/客户端管理 | 低 |

### 3.11 环境（实验性）

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `environment/add` / `info` / `status` | 环境管理 | 中 — 多环境切换 |

### 3.12 其他

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `server/diagnostics` | 服务器诊断 | 低 |
| `userVerification/*` (5 个) | 用户验证 | 低 |
| `externalAgentConfig/*` (4 个) | 外部代理配置导入 | ⭐ 高 — 导入 Claude/Cursor 配置 |
| `experimentalFeature/*` (2 个) | 实验性功能 | 低 |
| `modelProvider/capabilities/read` | 模型能力 | 中 |
| `windowsSandbox/*` (2 个) | Windows 沙箱 | 低 |
| `fuzzyFileSearch*` (4 个) | 模糊文件搜索 | 中 — @file 补全 |
| `feedback/upload` | 上传反馈 | 低 |
| `memory/status` / `memory/reset` | 内存管理 | 中 — 对应 OMA memory |

### 3.13 未处理的 ServerRequest（Python Bridge / Go 侧）

| 方法 | 说明 | 潜在价值 |
|---|---|---|
| `account/chatgptAuthTokens/refresh` | 刷新 token | 低 |
| `attestation/generate` | 平台证明 | 低 |
| `currentTime/read` | 读取客户端时间 | 低 |
| `item/applyPatchApproval` | 旧版补丁批准 | 低 |
| `item/execCommandApproval` | 旧版命令批准 | 低 |

---

## 4. 高优先级建议（⭐ 标记）

按集成价值排序：

| 优先级 | 接口 | 理由 |
|---|---|---|
| **P0** | `turn/interrupt`（Go/Bridge 层） | wire.ts 已实现但 Go 层缺失，停止按钮是基本交互 |
| **P0** | ServerRequest 处理（Go/Bridge 层） | wire.ts 已处理 5 个，但 Python Bridge 完全未处理，权限审批是安全核心 |
| **P0** | `thread/list` / `thread/read` | Session 列表和详情查看 |
| **P1** | `thread/resume` / `thread/fork` | 对话续接和分叉探索 |
| **P1** | `turn/steer` | 中途纠正代理方向 |
| **P1** | `config/read` + `model/list` | 配置/模型选择 |
| **P1** | `account/rateLimits/read` + `account/usage/read` | 额度和用量显示 |
| **P1** | `thread/compact/start` | 长对话上下文压缩 |
| **P1** | `thread/goal/set` / `get` / `clear` | OMA goal 对齐 |
| **P1** | `thread/search` | 全局搜索 session |
| **P2** | `review/start` | Code review 功能 |
| **P2** | `skills/list` | Skill 同步 |
| **P2** | `plugin/*` | 插件市场 |
| **P2** | `externalAgentConfig/*` | 配置迁移 |
| **P2** | `project/*` | Workspace 管理 |
| **P2** | `mcpServer/oauth/login` | MCP 服务对接 |

---

## 5. 统计

| 维度 | Codex 总数 | meta-harness 已用 | 使用率 |
|---|---|---|---|
| ClientRequest | ~120 | 8 | ~7% |
| ServerNotification | ~60 | 12 | ~20% |
| ServerRequest | ~10 | 5（仅 TS 子代理） | 50%（TS）/ 0%（Go/Python） |
| ClientNotification | 1 | 1（TS 侧） | 100%（TS）/ 0%（Go/Python） |

### 按客户端层统计

| 客户端层 | ClientRequest | ServerRequest 处理 | ServerNotification |
|---|---|---|---|
| Python Bridge (生产) | 7 | 0 | 9 |
| TS 子代理 (stdio) | 4 | 5 | 3 |
| PoC 脚本 | 3 | 0 | 6 |

---

## 6. 关键差异与风险

### 6.1 Go/Python Bridge 未处理 ServerRequest

TS 子代理能处理 5 种审批请求（即使是自动拒绝），但 Python Bridge 完全没有实现。这意味着：
- 如果 Codex 在 production 模式（非 ephemeral 线程）中触发命令/文件修改审批，Bridge 会挂起等待响应
- **建议**：在 `codex_client.py` 中添加 `handleServerRequest` 分发逻辑

### 6.2 ephemeral vs 持久线程

- TS 子代理使用 `ephemeral: true` 创建线程（用完即弃）
- Python Bridge 创建的是持久线程（缓存 thread_id 复用）
- 两种模式对 `thread/archive`、`thread/delete` 等清理接口的需求不同

### 6.3 重复实现

`initialize → thread/start → turn/start → wait for completion` 流程在三个客户端层各实现一次，存在不一致风险。建议长期收敛到一个核心客户端。
