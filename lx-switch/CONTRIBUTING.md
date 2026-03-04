# Contributing Guide

感谢参与 `lx-switch`。

## 1. 开发原则

1. **向后兼容优先**：已有 API 尽量不破坏。
2. **安全优先**：涉及 token、回滚、导入、配置写入的改动必须有保护措施。
3. **可审计**：关键写操作要补 `op_audits`。
4. **可回滚**：激活链路不得移除备份能力。

## 2. 本地开发

```bash
cd lx-switch
export LX_SWITCH_TOKEN='dev-token'
export LX_SWITCH_LISTEN=':18777'
export LX_SWITCH_DATA_DIR='/tmp/lx-switch-dev'
go run .
```

检查：
```bash
gofmt -w main.go
go build ./...
```

## 3. 提交流程

- 分支命名建议：`feat/*`、`fix/*`、`docs/*`
- 提交信息建议使用 Conventional Commit：
  - `feat(lx-switch): ...`
  - `fix(lx-switch): ...`
  - `docs(lx-switch): ...`

提交前最少完成：
1. `gofmt`
2. `go build`
3. 关键路径 smoke test（至少一个读接口 + 一个写接口）

## 4. UI 协作约定

- UI 改造请先看 `docs/UI_HANDOFF.md`
- 列表过滤和导出过滤必须一致
- 新增筛选字段必须同时补：
  - 列表查询
  - 导出查询
  - 分页文案
- 所有 destructive 操作保留确认弹窗

## 5. API 变更约定

- 先更新 `docs/API.md`
- 返回 JSON 新增字段可以，但不要删除旧字段
- 错误码保持语义稳定（400/401/405/500）

## 6. 安全注意事项

- 不要在日志、文档、示例中提交真实 key/token
- 提供外部样例时默认脱敏（`REDACTED`）
- 遇到疑似泄露，先轮换密钥再继续开发

## 7. 发布与部署

典型流程：

```bash
go build -o lx-switch .
sudo systemctl restart lx-switch
sudo systemctl is-active lx-switch
```

上线后建议检查：
- `/api/meta`
- `/api/providers`
- `/api/op-audits?limit=1`

## 8. 近期建议任务

- 登录审计 from/to 时间范围过滤
- CC 导入映射报告导出
- 前端资源拆分（减少 `main.go` 体积）
