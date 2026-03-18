# 当前进度（2026-03-18）

## 状态

- 服务：`lx-switch` 运行中（active）
- 仓库：`git@github.com:lyb377/lx-switch.git`
- 分支：`master`
- 任务统计：**总任务 24 / 已办 24 / 待办 0**（M4 全部完成）

## 最近关键提交

- `c1542ca` feat(m4): 实现审计 Dashboard 指标化
- `0c69273` docs: 更新前端组件化重构文档并添加开发指南
- `fca7e00` docs: complete project docs and clarify remaining modules + M4 assignment
- `cbdcd7d` feat(lx-switch): serve UI from static files (extract embedded HTML/CSS/JS)
- `9a7045b` feat(m3): complete login audit range filter, cc mapping report download, and SQL import tolerance

## 本次完成（M4 交付）

1. 审计 Dashboard 指标化（metrics.go + DashboardPage.js）
   - 登录/操作指标聚合（24h/7d/30d）
   - 按 target 分类统计，可视化卡片展示
   - CSV/JSON 导出支持
2. IP allowlist + TOTP 2FA（security.go, totp.go, auth.go）
   - CIDR 白名单，支持反代信任链（X-Forwarded-For）
   - TOTP RFC 6238，兼容 Google Authenticator
3. 多用户与 RBAC（rbac.go）
   - admin/operator/viewer 三内置角色
   - 细粒度权限点覆盖所有敏感操作
   - 向后兼容 legacy token
4. 前端组件化（web/src/）
   - pages/components/apiClient/state/utils 结构
   - Vite 工程化，统一错误处理与 API client

## 验证结果

- 自动化测试：`go test ./...` ✅（以本机实际为准）

## 当前未完成（后续迭代建议）

- 2FA 恢复码（用户丢失设备时的重置路径）
- 资源级权限（按 target 分域的细粒度 RBAC）
- 多实例 session 支持（Redis）
