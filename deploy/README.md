# deploy

## 架构

```
                    deepseek-harness-network (external)
                   ┌─────────────────────────────────────┐
                   │  deepseek-harness    meta-harness    │
                   │  (独立部署)           (docker.sh)     │
                   │  :3080               :8787           │
                   └─────────────────────────────────────┘
```

- **meta-harness** — Go 平台 + Console UI，由 `docker.sh` 管理
- **deepseek-harness** — DSH web 网关，独立部署
- 两者通过 `deepseek-harness-network` Docker 网络通信
- 宿主机 `/root/.dsh/.credentials.yaml` 挂载到 meta-harness 容器，用于签 DSH auth cookie

## 脚本

| 脚本 | 用途 |
|------|------|
| `docker.sh` | 部署 meta-harness + oma-auth |
| `deepseek/deepseek-auth.sh` | DSH 凭证同步 + 连通性验证 |

## 部署流程

```bash
# 1. 启动 deepseek-harness（独立部署，不由此项目管理）

# 2. 同步 DSH 凭证到宿主机
./deploy/deepseek/deepseek-auth.sh setup-auth

# 3. 启动 meta-harness
./deploy/docker.sh up

# 4. 验证
./deploy/deepseek/deepseek-auth.sh verify
```

## docker.sh

```bash
./deploy/docker.sh up              # 构建并启动（默认）
./deploy/docker.sh up --no-build   # 不构建，直接启动
./deploy/docker.sh down            # 停止并清理
./deploy/docker.sh restart         # 重启
./deploy/docker.sh logs [service]  # 查看日志
./deploy/docker.sh ps              # 容器状态
./deploy/docker.sh preflight       # 环境检查
```

服务端口：
- `8787` — meta-harness (Console UI + API)
- `8788` — oma-auth (Better Auth)

## deepseek/deepseek-auth.sh

```bash
./deploy/deepseek/deepseek-auth.sh setup-auth  # 从 DSH 容器同步凭证
./deploy/deepseek/deepseek-auth.sh verify      # 检查全链路
./deploy/deepseek/deepseek-auth.sh status      # 状态概览
```

### 凭证生成原理

1. DSH web 启动时输出一次性 token：`http://127.0.0.1:3080/?token=xxx`
2. `setup-auth` 用 curl 访问该 URL 触发浏览器会话创建
3. DSH 在容器内 `/app/.dsh/.credentials.yaml` 写入 browser-session secret
4. 脚本将其复制到宿主机 `/root/.dsh/.credentials.yaml`
5. meta-harness 容器挂载此文件，用 secret 签名 auth cookie 访问 DSH API

## 配置文件

| 文件 | 说明 |
|------|------|
| `docker-compose.yml` | meta-harness + oma-auth 服务定义 |
| `../.env` | 环境变量（数据源、模型、网关 URL 等） |
| `Dockerfile.platform` | meta-harness Go 平台镜像 |
| `Dockerfile.auth` | oma-auth Node.js 镜像 |
| `nginx-harness.conf` | harness LB（可选，默认未启用） |
| `deepseek/docker-compose.yml` | DSH 源码构建（可选，DSH 由外部部署时不需要） |

## 关键配置项（.env）

```bash
# DSH 网关地址（Docker 内部用容器名）
OMA_DEEPSEEK_GATEWAY_URL=http://deepseek-harness:3080
OMA_DEEPSEEK_ENABLED=1

# 数据源
DATABASE_URL=mysql+aiomysql://user:pass@host:3306/managed_agent

# 沙箱
SANDBOX_PROVIDER=opensandbox
OPENSANDBOX_DOMAIN=124.221.28.203:18090
```