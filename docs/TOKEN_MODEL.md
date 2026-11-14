# 令牌模型：name + scopes

本文是 **nxmem / keybaton / 旧 Python 版** 三个组件共用的令牌口径。
v0.1.1 之前三套写法并存（Go 版只有 `role`、Python 版叫 `scopes`、keybaton 是单令牌），
调用方必须猜"这个令牌能干什么"。现在统一成一条：

```
令牌 = { name, token_hash, scopes[] }
```

- `name`：谁在调用（写进审计的 `agent` 字段）
- `scopes`：能干什么，字符串数组，`<资源>:<动作>`
- 令牌本身只在签发时显示一次，库里只存 `sha256(token)`

## 1. 能力 scope（capability）

| 组件 | scope | 含义 |
|---|---|---|
| nxmem | `mem:read` | 检索/读记忆历史 |
| nxmem | `mem:write` | 写记忆（含 `exp-<agent>`、项目 scope） |
| nxmem | `graph:read` | 读图谱：MCP `graph` 工具、`GET /api/entities|links`（`GET /api/graph` 与看板只读接口免令牌） |
| nxmem | `graph:write` | 写图谱：`entity_put` / `link_put`、`POST /api/entities|links` |
| nxmem | `map:write` | 项目 / 任务 / 过程记录 |
| keybaton | `kb:read` | `kb_list`、`GET /api/secrets`（只有名称/账号/用途） |
| keybaton | `kb:http` | `kb_http`：由服务端代发请求、注入凭据 |
| keybaton | `kb:audit` | `kb_audit` 读调用记录 |
| keybaton | `kb:reveal` | 取明文（还要求服务端 `--allow-get`） |
| 通用 | `<资源>:*` | 该资源全部动作，如 `mem:*` |
| 通用 | `admin` / `*` | 全部能力；nxmem 里还额外允许写 `shared` |

判定顺序：`role=admin` → 放行；否则精确匹配 → `<资源>:*` 前缀匹配 → 拒绝（403，审计记 `tool_denied`）。

## 2. 数据 scope（data）

能力管"能不能写"，数据 scope 管"写哪一格"，两者都要过：

| scope | 谁能写 |
|---|---|
| `shared` | 只有 `admin`（主 agent） |
| `<项目码>` | 任意有写能力的调用方 |
| `exp-<agent>` | 只有该 agent 自己（admin 例外） |

scope 格式校验：`^(shared|exp-[A-Za-z0-9._-]{1,32}|[A-Za-z0-9][A-Za-z0-9._-]{0,31})$`。
写实体时若同名节点已属于别的 scope，非 admin 直接拒绝（防止顺手改掉别人节点）。

## 3. 传输

三种头等价，任选其一：

```
X-Nxmem-Token: <token>      # nxmem
X-Keybaton-Token: <token>   # keybaton
Authorization: Bearer <token>
```

`X-Api-Key` 也认（历史兼容）。

## 4. 各组件落地

### nxmem

```sql
agents(id, name, token_hash, role, scopes, created_at, last_seen)
```

- `scopes` 逗号分隔；空串时按 `role` 派生默认值（`admin` → `admin`；其它 → `mem:read,mem:write,graph:read,graph:write,map:write`），
  因此老库不用改数据也能升级。
- 签发：`nxmem agent add <name> --scopes a,b,c`；查看：`nxmem agent list`（NAME / ROLE / SCOPES / LAST_SEEN）。
- 环境变量 `NXMEM_ADMIN_TOKEN` 等价于 `name=admin, scopes=admin`。

### keybaton

- `KEYBATON_TOKEN` 单令牌保留（等价 `name=legacy, scopes=admin`），老部署不用改。
- 多令牌：`--tokens <file.json>` 或 `KEYBATON_TOKENS`，格式：

```json
[
  {"name": "hermes", "token": "<token>", "scopes": ["kb:read", "kb:http", "kb:audit"]},
  {"name": "readonly-bot", "token": "<token>", "scopes": ["kb:read"]}
]
```

- 明文取用需要 `kb:reveal` **且** 服务端带 `--allow-get`，两个条件缺一不可。

### 旧 Python 版（nx-memshare）

`scopes` 字段沿用同一套命名（`mem:read/mem:write/admin`），迁移到 Go 版时直接按字符串搬运；
`role` 只在没有 `scopes` 时作为兜底。

## 5. 轮换与审计

- 轮换：`agent add` 同名覆盖令牌哈希（旧令牌立即失效）；keybaton 改 tokens.json 后重启容器。
- 审计：nxmem `audit` 表记录 `agent / action / target / ok / detail`；写图谱、写记忆、越权尝试（`tool_denied`）都留痕。
- 令牌不进日志、不进模型上下文；nxmem 不做凭据存储（用 keybaton）。
