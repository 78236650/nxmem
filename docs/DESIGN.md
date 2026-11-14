# 设计文档

记录数据模型、治理规则背后的判断，以及被否掉的方案。

---

## 1. 要解决的问题

多 agent 协作真实卡住的三个点：

| 现象 | 后果 |
|---|---|
| 同一事实被多台机器各记一份 | 三份互相矛盾，没人知道哪份有效 |
| 换台机器就要重新交代背景 | 上下文断层，重复劳动 |
| 进度散在对话里 | 说不清"现在到哪一步"，也无法复盘 |

结论：需要的不是更大的记忆库，而是**写入规则 + 身份 + 可见性**。

---

## 2. 数据模型

```
memories(id, scope, key, content, tags, source, agent,
         created_at, updated_at, status, verified_at, ttl_days, valid_for)
         UNIQUE(scope, key)

memory_versions(id, scope, key, old_content, changed_by, changed_at)

projects(id, code, name, client, status, owner, start_date, due_date, progress, summary)
tasks(id, project_code, name, start_date, end_date, status, progress, assignee, notes)
steps(id, project_code, seq, title, status, detail, agent, created_at)

agents(id, name, token_hash, role, created_at, last_seen)
audit(id, ts, agent, action, target, ok, detail)
```

**关键约束**：`UNIQUE(scope, key)`。

这是整套设计的地基——一条事实在某个范围内只有**一个**位置。想改就覆盖同一个 key，而不是新增一条"更新的说法"。没有这个约束，任何冲突检测都无从谈起。

---

## 3. 为什么是 scope 三档

```
shared          全局共识（规范、口径、决策）     仅 admin 可写
<项目码>        项目上下文                        任何 writer
exp-<机器名>    该机器的实验记忆                  仅该机器
```

**为什么不让所有人写 shared**：三台机器同时改一条全局规范，谁最后写谁赢——这就是"记忆混乱"的根源。把 shared 收归 admin（通常是人在驱动的那台），其他机器只能提案到自己的 `exp-` 或项目 scope，冲突面立刻收敛。

**为什么保留 exp-**：agent 需要草稿区。没有草稿区，它会直接往项目 scope 塞未验证的结论。

### 被否掉的：给每条记忆加 `confidence` 分数

听起来更精细，实际上没人会给准数，agent 更是随手填 0.8。**用结构和权限约束，比用自评分数可靠**。

---

## 4. 三道写入闸

### ① 相似键拦截

同 scope 下新 key 与已有 key 相似度 ≥ 0.7 → 拒绝，并返回候选键。

```
"api-paging" vs "api-pagination"  → 0.75 → 拒绝
"api-paging" vs "design-tokens"   → 0.07 → 放行
```

相似度用最长公共子串 / 较长者长度（无第三方依赖，够用）。

**为什么不用编辑距离**：key 通常是"名词-名词"结构，公共子串更能抓住"同一件事换个说法"。

### ② 数字冲突标记

覆盖时比较新旧内容里的数字序列，不一致就标 `conflict`：

```
旧：免费档 60 次/分钟
新：免费档 100 次/分钟
→ status=conflict，旧值进 memory_versions，两条都留
```

**为什么标而不拒**：新值可能是对的（真的改了配额）。系统没有裁决权，只负责让分歧可见。

**已知局限**：只比数字。语义层面的矛盾（"支持 A" vs "不支持 A"）识别不了。

### ③ 版本留痕

任何覆盖都写 `memory_versions`。表只增不删——它是"为什么会变成现在这样"的唯一线索。

---

## 5. TTL 与 stale

`ttl_days > 0` 时，读取时按 `verified_at` 计算是否超期，超期把 `status` 动态置为 `stale`，**不落库、不删数据**。

**为什么不自动过期删除**：记忆的价值往往在"过期了但能看出它曾经成立"。自动删等于把历史抹掉。

看板顶部常驻 `conflict` 与 `stale` 计数——这两个数字就是"需要人看一眼"的队列。

---

## 6. 身份与审计

- 令牌存 **sha256 哈希**，不存明文
- `step_log` 的 `agent` 由服务端从令牌解出，**客户端不能指定**
- 每个写操作落 `audit`

**为什么不用 JWT/SSO**：目标场景是几台自有机器，静态令牌 + 角色两档足够；引入身份提供商会让部署复杂度翻倍，收益为零。

**已知局限**：令牌泄露即身份泄露，无轮换提醒、无按 scope 的细粒度授权（v0.3 再说）。

---

## 7. 存储与依赖

| 决定 | 取舍 |
|---|---|
| SQLite 单文件 | 不需要运维数据库；代价是并发写有限（WAL 下够用） |
| `modernc.org/sqlite`（纯 Go） | 免 CGO，交叉编译四平台零配置；代价是比 C 版慢一点 |
| 关键词检索（LIKE） | 零依赖、结果可解释；代价是没有语义召回 |
| 单二进制 + 内嵌静态页 | 部署 = 拷一个文件；代价是前端只能手写原生 JS |

### 被否掉的：接向量库做语义检索

在几百条记忆的规模下，关键词匹配 + 有意识的 key 命名已经够用；向量库会引入嵌入模型、索引维护、模型漂移三个新问题。**等真有"搜不动"的痛感再加**——接口留好了，`mem_search` 换实现不影响调用方。

---

## 8. 与 keybaton 的分工

```
nxmem      知道什么（记忆、项目、过程）      明文存储，靠 scope 隔离
keybaton   能开哪把锁（凭据）                AES-GCM 加密，凭据不进上下文
```

两个项目**故意不合并**：记忆要能被人看见（看板、审计、导出），凭据必须尽量少人看见。安全等级不同，混在一起只会两头不讨好。

---

## 9. 后续

- v0.2：`mem_search` 加可选向量后端；记忆合并（把两条相近的合成一条）
- v0.3：按 scope 的细粒度授权；写入审批流
- 不做：图形化编辑器（看板保持只读）、多租户 SaaS
