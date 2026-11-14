# 变更日志

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 计划中
- `mem_search` 可选向量后端（保留现有接口，不改调用方）
- 记忆合并：把两条相近的合成一条并留痕
- 按 scope 的细粒度授权 + 写入审批流
- `agent rm` 命令

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
