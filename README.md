# nxmem

[![release](https://img.shields.io/github/v/release/78236650/nxmem)](https://github.com/78236650/nxmem/releases)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![go](https://img.shields.io/badge/go-1.25%2B-00ADD8)](go.mod)

**给多个 agent 共用的一套记忆与进度。单二进制 + SQLite，带身份、权限和审计。**

```bash
nxmem serve                 # 看板 + REST + MCP，一个进程
nxmem agent add mac         # 给一台机器发令牌
nxmem demo                  # 灌一套示例数据看效果
```

---

## 为什么还要一个

agent 之间要协作，卡住的往往不是模型能力，而是三个很土的问题：

1. **同一件事被记了三遍，三份还不一样**——谁说的算？
2. **每台机器各记各的**，换个地方就得重新讲一遍背景
3. **进度散在聊天里**，没人说得清现在到哪一步

现成的记忆库（mem0 / basic-memory / Zep）解决的是「**存和查**」，解决不了「**谁能写、写了算谁的、进度在哪**」。

nxmem 的定位就一句：**多机协作时的记忆治理层**。

| | 单机记忆库 | 通用向量库 | **nxmem** |
|---|---|---|---|
| 多机共享 | 通常要自己搭 | 要自己搭 | **自带 HTTP + MCP** |
| 每台机器独立身份 | ✗ | ✗ | **✓ 每 agent 一个令牌** |
| 写权限隔离 | ✗ | ✗ | **✓ shared / 项目 / exp-机器** |
| 覆盖留版本 | 部分 | ✗ | **✓ `memory_versions`** |
| 冲突标记 | ✗ | ✗ | **✓ 数字不一致标 conflict** |
| 过期复核 | 部分 | ✗ | **✓ TTL + stale** |
| 项目 / 任务 / 甘特 | ✗ | ✗ | **✓ 只读看板** |
| 审计 | ✗ | ✗ | **✓ 谁在什么时候读了什么** |
| 部署 | 单机 | 向量库 + 服务 | **一个容器** |

它**不是**：向量检索（关键词 LIKE，够用但不语义）、笔记软件、密码库（凭据请用 [keybaton](https://github.com/78236650/keybaton)）。

---

## 30 秒上手

```bash
# 1. 装（三种任选）
#   a) 下载二进制
curl -L -o nxmem https://github.com/78236650/nxmem/releases/latest/download/nxmem-linux-amd64
chmod +x nxmem && sudo install -m 0755 nxmem /usr/local/bin/nxmem

#   b) Go 装
go install github.com/78236650/nxmem/cmd/nxmem@latest

#   c) 容器
NXMEM_ADMIN_TOKEN=$(openssl rand -hex 24) docker compose up -d

# 2. 起服务
export NXMEM_ADMIN_TOKEN=$(openssl rand -hex 24)
nxmem serve --addr 127.0.0.1:8861

# 3. 看效果（另开一个终端）
nxmem demo
open http://127.0.0.1:8861/
```

看板是**只读**的：所有写入都走 MCP，由 agent 完成。这样人能看见全貌，但不会手滑改数据。

---

## 给 agent 接入

```bash
# 每台机器一个令牌，角色分 writer / admin
nxmem agent add mac --role writer
→ agent=mac role=writer token=xxxxx
```

MCP 端点：`POST /mcp`，Header `X-Nxmem-Token`（也认 `X-Api-Key` 和 `Bearer`）。

```jsonc
{
  "url": "http://<host>:8861/mcp",
  "headers": { "X-Nxmem-Token": "<令牌>" }
}
```

只支持 stdio 的客户端，用 `scripts/mcp_stdio.py` 做桥。

### 工具清单

| 工具 | 作用 |
|---|---|
| `mem_search` | 检索记忆（query 为空返回最近） |
| `mem_put` | 写入/覆盖，自动执行权限、相似键、数字冲突三道检查 |
| `mem_history` | 某条记忆的历史版本 |
| `project_list` / `project_upsert` | 项目与进度 |
| `task_list` / `task_upsert` | 任务（甘特数据源） |
| `step_log` / `step_list` | 过程记录：谁在什么时候做了什么 |
| `whoami` | 当前身份与角色 |

---

## 治理规则（这是它和"又一个记忆库"的区别）

**scope 三档**

| scope | 谁能写 | 用途 |
|---|---|---|
| `shared` | 只有 `admin` 角色 | 全局共识：规范、口径、决策 |
| `<项目码>` | 任何 writer | 项目内上下文 |
| `exp-<机器名>` | 只有该机器 | 各机器的实验性记忆，互不污染 |

**写入时的三道闸**

1. **相似键**：同 scope 下已有相似 key（相似度 ≥0.7）→ 拒绝，避免"两套说法"；确属新事实加 `force=true`
2. **数字冲突**：覆盖时新旧内容里的数字不一致 → 标 `conflict`，旧值进 `memory_versions`，等人裁决
3. **版本留痕**：任何覆盖都保留旧内容、改动人、时间

**过期复核**：`mem_put` 支持 `ttl_days`，读取时超期自动标 `stale`（不删数据），看板顶部有计数。

---

## 看板

- **关系图谱**：谁（agent）在推进哪个项目
- **甘特**：任务时间线，按项目着色
- **项目**：进度、负责人、起止
- **记忆**：按 scope 分组，标 `有效 / 冲突待裁决 / 待复核`
- **过程**：时间线式的操作记录
- **审计**：MCP 调用流水

纯静态 HTML + 原生 JS，无构建步骤，无前端依赖。

---

## 数据与部署

单文件 SQLite，默认 `~/.nxmem/nxmem.db`，目录可用 `NXMEM_HOME` 覆盖。

```bash
# 备份
sqlite3 ~/.nxmem/nxmem.db ".backup '/backup/nxmem-$(date +%F).db'"

# 容器
docker run -d -p 127.0.0.1:8861:8861 \
  -e NXMEM_ADMIN_TOKEN=$NXMEM_ADMIN_TOKEN \
  -v ./data:/data ghcr.io/78236650/nxmem:0.1.0
```

细节见 [运维文档](docs/OPERATIONS.md)。

---

## 构建

```bash
make build    # 本机
make cross    # darwin/linux × amd64/arm64
make demo     # 灌示例数据
make docker
```

需要 Go 1.25+，SQLite 用纯 Go 驱动（`modernc.org/sqlite`），交叉编译不需要 CGO。

---

## 文档

| 文档 | 内容 |
|---|---|
| [使用文档](docs/USAGE.md) | 安装、命令、MCP 接入、scope 与治理细节 |
| [设计文档](docs/DESIGN.md) | 数据模型、治理规则为什么这么定、被否掉的方案 |
| [运维文档](docs/OPERATIONS.md) | 部署、备份、升级、令牌轮换、故障处置 |

---

## 相关

- [keybaton](https://github.com/78236650/keybaton) —— 同一套思路的另一半：**凭据**不进 agent 上下文。nxmem 管"知道什么"，keybaton 管"能开哪把锁"。

## 许可

MIT
