# 使用文档

---

## 1. 安装

### 二进制

```bash
# Linux amd64（其它平台替换文件名：darwin-arm64 / darwin-amd64 / linux-arm64）
curl -L -o nxmem https://github.com/78236650/nxmem/releases/latest/download/nxmem-linux-amd64
chmod +x nxmem
sudo install -m 0755 nxmem /usr/local/bin/nxmem
nxmem version
```

### Go

```bash
go install github.com/78236650/nxmem/cmd/nxmem@latest
```

### 容器

```bash
export NXMEM_ADMIN_TOKEN=$(openssl rand -hex 24)
docker compose up -d
curl -s http://127.0.0.1:8861/healthz
```

---

## 2. 起服务

```bash
export NXMEM_ADMIN_TOKEN=$(openssl rand -hex 24)
nxmem serve --addr 127.0.0.1:8861
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `--addr` | `127.0.0.1:8861` | 监听地址 |
| `--dir` | `$NXMEM_HOME` 或 `~/.nxmem` | 库目录 |
| `--admin-token` | `$NXMEM_ADMIN_TOKEN` | 管理员令牌，可写 `shared` |
| `--static` | 自动探测 `./static` | 看板目录 |

**不设 admin 令牌也能起**，但所有写 `shared` 的请求都会被拒。建议总是设。

---

## 3. 发令牌

令牌 = **身份 + 能力 scope**，两个维度：

```bash
nxmem agent add mac   --scopes mem:write,graph:write     # 只能写，不能写 shared
nxmem agent add admin --scopes admin                     # 全权，可写 shared
nxmem agent add ro    --scopes mem:read                  # 只读
nxmem agent list                                         # 看谁接过、各自的能力
```

令牌只在 `add` 那一刻显示一次，库里只存 sha256。丢了就 `add` **同名覆盖**（旧令牌立即失效）。

**能力清单**

| 能力 | 允许 |
|---|---|
| `mem:read` | `mem_search` / `mem_history` |
| `mem:write` | `mem_put`、项目/任务/过程的写入 |
| `graph:read` | `graph`、`GET /api/entities`、`GET /api/links` |
| `graph:write` | `entity_put` / `link_put`、`POST /api/entities|links` |
| `map:write` | 图谱写（兼容旧口径） |
| `admin` | 全部，含写 `shared` |
| `<资源>:*` / `*` | 通配 |

**数据 scope 另外算**（两把锁都得开）：

| 数据 scope | 谁能写 |
|---|---|
| `shared` | 仅 `admin` 能力 |
| `<项目码>` | 任何有写能力、且项目在可见范围内的令牌 |
| `exp-<机器>` | 只有该机器自己 |

---

## 4. 接 MCP

### HTTP（推荐）

```jsonc
{
  "url": "http://<host>:8861/mcp",
  "headers": { "X-Nxmem-Token": "<令牌>" }
}
```

认证头三种写法都认：`X-Nxmem-Token`、`X-Api-Key`、`Authorization: Bearer <令牌>`。

### stdio 客户端

```jsonc
{
  "command": "/usr/bin/python3",
  "args": ["/path/to/scripts/mcp_stdio.py"],
  "env": {
    "NXMEM_URL": "http://<host>:8861/mcp",
    "NXMEM_TOKEN": "<令牌>"
  }
}
```

---

## 5. 工具详解

### `mem_put`

```json
{
  "scope": "atlas-web",
  "key": "design-tokens",
  "content": "主色 #0F172A，圆角 12px，间距基数 4px",
  "tags": "设计",
  "ttl_days": 90,
  "valid_for": "设计系统 v2"
}
```

返回：

```json
{"ok": true, "scope": "atlas-web", "key": "design-tokens", "status": "active", "overwrote": true}
```

字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `scope` | 否 | 默认 `shared`（需 admin） |
| `key` | 是 | 同 scope 内唯一 |
| `content` | 是 | 正文 |
| `tags` | 否 | 逗号分隔，参与检索 |
| `force` | 否 | 越过"相似键"与"数字冲突"检查 |
| `ttl_days` | 否 | >0 时到期标 `stale` |
| `valid_for` | 否 | 适用范围说明（给人看的） |

**三种失败**（返回 `isError`）：

| 报错 | 原因 | 处理 |
|---|---|---|
| `shared 只允许主 agent 写；请写 exp-<你> 或项目 scope` | 角色不够 | 换 scope，或让 admin 写 |
| `只能写自己的 exp-<机器名>` | 写了别人的实验区 | 改正 scope |
| `发现相似记忆键，可能重复…` | 同 scope 有相近 key | 用同一个 key 覆盖，或 `force=true` |

### `mem_search`

```json
{"query": "限流", "scope": "beacon-api", "limit": 10}
```

`query` 走 `key / content / tags` 的**子串匹配**（不区分大小写）。为空时返回最近更新。

### `mem_history`

```json
{"scope": "shared", "key": "release-policy"}
```

返回旧内容、改动人、时间，倒序。

### `step_log`

```json
{"project": "atlas-web", "title": "页面迁移第一批", "status": "in_progress", "detail": "首页与列表页灰度"}
```

`agent` 由令牌自动填充，不接受客户端指定——这是审计可信的前提。

### `task_upsert` / `project_upsert`

同名任务/同 code 项目会覆盖（`updated_at` 自动刷新），甘特图立即反映。

---

### `graph`

```json
{"entity": "atlas-web"}
```

不带参数返回整图；带 `entity` 只返回该实体的邻域（一跳）。也支持 `GET /api/graph`（免令牌只读）。

### `entity_put`

```json
{"scope": "shared", "type": "project", "name": "atlas-web", "meta": "{\"owner\":\"team-a\"}"}
```

`type` 建议用：`project` / `person` / `client` / `system` / `tool`。写 `shared` 需要 `admin` 能力。

### `link_put`

```json
{"scope": "shared", "src": "agent-a", "dst": "atlas-web", "rel": "推进", "note": "本季度"}
```

`src` / `dst` 用实体名。重复写同一对关系会覆盖 `note`，不会产生重复边。

---

## 6. 看板

打开 `http://<host>:8861/`。

六个页签：关系图谱 / 甘特 / 项目 / 记忆 / 过程 / 审计。

**只读**，写入一律走 MCP。看板数据 15 秒自动刷新。

记忆页签里的状态标记：

| 标记 | 含义 | 怎么处理 |
|---|---|---|
| `有效` | 正常 | — |
| `冲突待裁决` | 覆盖时数字变了 | 人去看新旧值，确认后 `force=true` 重写 |
| `待复核` | 超过 `ttl_days` 未更新 | 复核内容，重新 `mem_put` 刷新时间 |

---

## 7. 命令行查看

```bash
nxmem memories                      # 最近 100 条
nxmem memories --scope shared
nxmem memories --q 限流 --json
```

---

## 8. 常见问题

**Q：`401 unauthorized`**
Header 名写错，或令牌是另一个实例的（每个 `NXMEM_HOME` 一套 agent 表）。

**Q：写 shared 一直被拒**
用 `--role writer` 发的令牌。改用 admin 令牌，或写项目 scope。

**Q：两台机器写的 key 冲突了**
这正是设计要防的：同 scope 同 key 会覆盖并留版本；相近 key 直接拒绝。先协商 key 命名，再写。

**Q：看板打开是空白**
`--static` 没指对。源码目录下直接跑会自动找 `./static`；二进制单独放置时需要显式传 `--static /path/to/static`。

**Q：想让 agent 之间互读实验记忆**
把 scope 写成项目码，而不是 `exp-<机器>`。`exp-` 前缀的语义就是"这台机器的草稿"。

**Q：数据能导出吗**
直接拷 `nxmem.db`（SQLite），或 `sqlite3 nxmem.db .dump`。
