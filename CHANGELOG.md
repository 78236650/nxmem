# 变更日志

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 计划中
- `agent rm` 命令

### 计划中
- `mem_search` 可选向量后端（保留现有接口，不改调用方）
- 记忆合并：把两条相近的合成一条并留痕
- `agent rm` 命令
- 看板其余只读接口（overview/memories/...）统一加令牌校验

## [0.1.2] - 2026-11-14

### 安全（破坏性变更）
- **只读接口默认需要令牌**：`/api/overview|memories|projects|tasks|gantt|steps|audit|graph`
  以及看板本身，无令牌一律 401。此前这些接口公开，任何人能访问端口即可读走全部记忆正文与审计。
- 新增 `--public-read` 开关：确需免登录看板（内网大屏、演示）时才开，启动时会打印提示。
- 看板首次打开会弹出令牌输入框，令牌存在 localStorage；「更换令牌」可清空。
- 迁移提示：升级后浏览器打开看板需粘一次令牌（`nxmem agent add <name> --scopes mem:read` 生成）。

## [0.1.1] - 2026-11-14

### 修复
- **`--scopes admin` 的令牌无法写 `shared`**：鉴权仍按旧 `role` 字段判断，
  导致按能力模型签发的 admin 令牌被当成 writer。现在 `auth` 解析后统一归一化
  （能力含 `admin` → 角色视为 admin），REST 与 MCP 两条路径都生效
- 图谱项目节点改用项目编码作 id、name 单独存显示名，修复同名/重复节点与连线丢失

### 新增
- **图谱写口**：MCP `entity_put` / `link_put`，REST `POST /api/entities`、`POST /api/links`，
  `GET /api/entities?scope=&q=`、`GET /api/links?entity=`（此前只有 `graph` 读）
- **令牌模型统一为 `name + scopes`**：`agents.scopes` 列 + `agent add --scopes`，
  `agent list` 显示 scopes；能力 `mem:*` / `graph:*` / `map:write` / `admin`，支持 `<资源>:*` 通配
- `docs/TOKEN_MODEL.md`：与 keybaton、旧 Python 版共用的口径（能力 scope × 数据 scope）

### 变更
- **读公开、写鉴权（定稿口径）**：`GET /api/graph` 与看板只读接口免令牌（看板 JS 直连），
  写接口（`entity_put` / `link_put`、`POST /api/entities|links`、`mem_put`、项目/任务/过程）必须令牌 + 能力 scope + 数据 scope。
  代价：图谱公开——README 标明「只读公开、勿放敏感实体」
- 写记忆/项目/任务/过程、读记忆/图谱都先过能力校验，越权尝试写审计 `tool_denied`
- 实体与关系各增 `scope` 列（老库自动 `ALTER TABLE` 补列，默认 `shared`）

## [0.1.0] - 2026-11-14

首个公开版本。定位：**多机协作的记忆治理层**——单二进制 + SQLite，带身份、权限、审计。

### 新增
- **MCP 服务**（HTTP JSON-RPC）：`initialize` / `tools/list` / `tools/call`，10 个工具
- **记忆**：`mem_search` / `mem_put` / `mem_history`
- **项目与进度**：`project_list` / `project_upsert` / `task_list` / `task_upsert`
- **过程记录**：`step_log` / `step_list`（agent 由令牌自动填充）
- **身份**：`whoami`；每 agent 一个令牌，角色 `admin` / `writer`
- **只读看板**：关系图谱 / 甘特 / 项目 / 记忆 / 过程 / 审计，纯静态 HTML
- **REST**：`/api/overview`、`/api/memories`、`/api/gantt`、`/api/graph`、`/api/audit` 等
- **示例数据**：`nxmem demo`

### 治理
- scope 三档：`shared`（仅 admin）/ 项目码 / `exp-<机器>`
- 相似键（≥0.7）拒绝写入，需 `force=true`
- 覆盖时数字变化标 `conflict`，旧值存 `memory_versions`
- `ttl_days` 超期读取时标 `stale`，不删数据
- 全部写操作落 `audit`

### 已知限制
- 检索是关键词子串匹配，无语义召回
- 无 `agent rm`，掉线令牌靠重发覆盖
- 记忆明文存储（看板与审计需要可读），备份文件权限即安全边界
- 单机 SQLite，写入并发有限
