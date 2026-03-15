# 当前进度（2026-03-15）

## 状态

- 服务：`lx-switch` 运行中（active）
- 仓库：`git@github.com:lyb377/lx-switch.git`
- 分支：`master`
- 任务统计：**总任务 20 / 已办 16 / 待办 4**（按 `docs/ROADMAP.md` 勾选项统计）

## 最近关键提交

- `cbdcd7d` feat(lx-switch): serve UI from static files (extract embedded HTML/CSS/JS)
- `9a7045b` feat(m3): complete login audit range filter, cc mapping report download, and SQL import tolerance
- `2122181` refactor(lx-switch): split auth and utility helpers from main.go
- `3e67729` docs: refresh architecture/progress/api/overview for M3 and UI parallel work
- `a9bccd5` docs(ui): add parallel UI handoff guidance and coordination checklist

## 本次完成（最近一轮交付）

1. UI 从静态文件提供（静态资源抽离）
   - 将原内嵌 HTML/CSS/JS 抽离到 `lx-switch/web/` 与 `lx-switch/web/static/`
   - 服务端改为直接托管静态资源（便于后续前端组件化/工程化）
2. M3 收尾（已合并）
   - 登录审计 `from/to` 时间范围过滤（列表 + 导出）
   - CC-Switch 导入映射报告下载：`POST /api/providers/import-cc/report`
   - 导入容错增强（复杂 SQL 边界）

## 验证结果

- 自动化测试：`go test ./...` ✅（以本机实际为准）

## 当前未完成（聚焦 M4 / Backlog）

- 前端组件化重构（拆出静态资源后的进一步模块化：组件拆分、状态管理、构建产物规范）
- 更细粒度安全能力：IP allowlist / 2FA 可选
- 多用户与权限控制：RBAC（角色、权限点、资源域）
- 审计 dashboard 指标化（指标口径、汇总接口、可视化）
