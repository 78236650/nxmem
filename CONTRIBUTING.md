# 参与贡献

## 提 issue 前

1. 先看 [使用文档](docs/USAGE.md) 的「常见问题」
2. 附上：`nxmem version`、命令、完整输出、系统架构

安全漏洞请走私密报告，不要开 public issue。

## 提 PR

```bash
go vet ./... && go test ./...
make build && ./bin/nxmem demo     # 自检一遍示例数据
```

要求：

- 新增工具/参数 → 同步更新 `docs/USAGE.md` 与 `tools/list` 里的 description
- 改动治理规则 → 同步更新 `docs/DESIGN.md`（要写清"为什么"）
- 破坏性变更 → `CHANGELOG.md` 的 Unreleased 里写清楚
- 提交信息用 `feat:` / `fix:` / `docs:` / `refactor:` 前缀

## 不接受的方向

- 图形化编辑（看板保持只读，这是设计选择）
- 引入向量库作为**默认**依赖（可以做成可选后端）
- 把凭据管理塞进来（那是 keybaton 的职责）
- 多租户 SaaS 化
