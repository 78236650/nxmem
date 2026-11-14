# 运维文档

---

## 1. 部署形态

| 形态 | 场景 | 要点 |
|---|---|---|
| 前台进程 | 本机试用 | `nxmem serve --addr 127.0.0.1:8861` |
| systemd | Linux 服务器 | 见 §2 |
| Docker | NAS / 现场 | 见 §3 |
| launchd | macOS 常驻 | 见 §4 |

**通用原则**：默认只监听 `127.0.0.1`。跨机访问走内网 / VPN，不要直接映射公网。

---

## 2. systemd

```ini
[Unit]
Description=nxmem shared memory service
After=network-online.target

[Service]
Type=simple
User=nxmem
Group=nxmem
Environment=NXMEM_HOME=/var/lib/nxmem
EnvironmentFile=/etc/nxmem.env
ExecStart=/usr/local/bin/nxmem serve --addr 0.0.0.0:8861 --static /opt/nxmem/static
Restart=on-failure
RestartSec=3

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/nxmem

[Install]
WantedBy=multi-user.target
```

`/etc/nxmem.env`（权限 600）：

```
NXMEM_ADMIN_TOKEN=<openssl rand -hex 24>
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now nxmem
journalctl -u nxmem -f
```

---

## 3. Docker

```yaml
services:
  nxmem:
    image: ghcr.io/78236650/nxmem:0.1.1
    restart: unless-stopped
    ports:
      - "127.0.0.1:8861:8861"
    environment:
      NXMEM_HOME: /data
      NXMEM_ADMIN_TOKEN: ${NXMEM_ADMIN_TOKEN:?需要设置}
    volumes:
      - ./data:/data
```

**注意**：

- 容器内 UID 是 **10001**，宿主目录要 `chown 10001:10001`
- `./data` 必须持久化，否则重建容器 = 数据没了
- 别把 `8861` 直接映射到公网；要外网访问用端口转发 + 令牌

---

## 4. macOS launchd

```xml
<key>ProgramArguments</key>
<array>
  <string>/usr/local/bin/nxmem</string>
  <string>serve</string>
  <string>--addr</string><string>127.0.0.1:8861</string>
  <string>--static</string><string>/usr/local/share/nxmem/static</string>
</array>
<key>EnvironmentVariables</key>
<dict>
  <key>NXMEM_ADMIN_TOKEN</key><string>替换成随机值</string>
</dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
```

---

## 5. 备份

只有一个东西要备份：

```
$NXMEM_HOME/nxmem.db      （WAL 模式下同时有 -wal / -shm）
```

```bash
# 热备（推荐）
sqlite3 /var/lib/nxmem/nxmem.db ".backup '/backup/nxmem-$(date +%F).db'"

# 冷备
systemctl stop nxmem && tar czf /backup/nxmem-$(date +%F).tgz /var/lib/nxmem && systemctl start nxmem
```

**没有主密钥**——nxmem 不加密存储（记忆要能被看板和审计读取）。所以**备份文件的访问权限就是安全边界**，别随手丢在共享盘上。

---

## 6. 升级

```bash
# 1. 备份（见 §5）
systemctl stop nxmem

# 2. 换二进制（静态页有更新时一起换）
sudo install -m 0755 nxmem-linux-amd64 /usr/local/bin/nxmem
sudo rsync -a static/ /usr/local/share/nxmem/static/

# 3. 起服务 + 自检
systemctl start nxmem
nxmem version
curl -s http://127.0.0.1:8861/healthz
curl -s http://127.0.0.1:8861/api/overview | head -c 200
```

库结构变更在启动时自动执行（`CREATE TABLE IF NOT EXISTS` + 幂等迁移）。升级前看 CHANGELOG 有无破坏性变更。

---

## 7. 令牌管理

```bash
nxmem agent list                     # 谁接过令牌、最后一次访问时间
nxmem agent add mac --role writer    # 重新发（同名覆盖，旧令牌立即失效）
```

**轮换建议**：换机器、疑似泄露、季度巡检时重发。**没有"删除某个 agent"的命令**（v0.1），需要立即失效就重发同名令牌，或直接改库里的 `token_hash`。

`last_seen` 是判断"这台机器还在不在用"的唯一线索——长期不更新的可以清理。

---

## 8. 故障处置

| 症状 | 原因 | 处置 |
|---|---|---|
| `401 unauthorized` | Header 名错 / 令牌属于另一个实例 | 核对 `X-Nxmem-Token`；确认令牌是在同一 `NXMEM_HOME` 发出的 |
| `shared 只允许主 agent 写` | 用了 writer 令牌 | 换 admin，或写项目 scope |
| 看板空白 | `--static` 路径不对 | 显式传 `--static` |
| 看板能开但数据不刷新 | 反向代理缓存 | 给 `/api/*` 加 `Cache-Control: no-store` |
| `database is locked` | 并发写过多 | 确认 WAL 已开（默认开）；必要时串行化写入 |
| 甘特图空白 | 任务缺 `start_date` | `task_upsert` 补上起止日期 |
| 图谱只有一个孤立点 | 过程记录里没有 `agent` | 用 MCP 写入（自动带 agent），不要直接改库 |
| 容器起来就退出 | 没设 `NXMEM_ADMIN_TOKEN` 且 compose 用了 `:?` | 设环境变量 |

日志：systemd 看 `journalctl -u nxmem`，Docker 看 `docker logs nxmem`。

---

## 9. 安全检查清单（上线前）

- [ ] 监听地址不是 `0.0.0.0`，或前面有反代 + 令牌
- [ ] `NXMEM_ADMIN_TOKEN` 是随机值
- [ ] 每个 agent 用**独立令牌**，没有共用
- [ ] `nxmem agent list` 里没有陌生的 agent
- [ ] 备份文件权限 600，且不在公开共享目录
- [ ] 端口没有映射到公网
- [ ] 看板挂在只读位置（它本来就没有写接口，但别让其他人误以为能改）
