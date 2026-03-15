# M4 剩余任务拆分与四人分派建议

> 你后续“来安排”的时候可以直接按这个分工拆 PR。这里把边界、交付物和依赖说清楚。

## 分派原则

- 尽量解耦：前端组件化、RBAC、IP/2FA、指标化 可以并行
- 先定接口/模型：RBAC 与 metrics 需要后端先定 API 与数据模型，前端才能稳
- 每个方向都要补文档 + 测试（至少关键路径）

---

## A（前端负责人）：前端组件化重构（静态资源基础上继续）

范围：`lx-switch/web/**`

交付物：
- 组件化目录结构（pages/components/apiClient/state/utils）
- API Client 封装（统一错误处理、401 重定向、提示）
- Provider 管理、导入、审计页面完成组件拆分
- 构建/发布方案（若引入 Vite 等：产物输出到 `lx-switch/web/static/` 或 `dist/` 并由服务端托管）
- 更新 `docs/UI_HANDOFF.md`（新结构、开发方式、构建命令）

依赖/注意：
- 需要与 B/D 对齐：新增 API（RBAC/metrics）时的调用方式与错误语义

---

## B（后端安全）：IP allowlist + 2FA（可选）

范围：Go 后端中间件/认证流程

交付物：
- IP allowlist：
  - 配置方式（env/config）+ 中间件实现
  - 反代场景真实 IP 解析策略（含信任链说明）
  - 拒绝访问审计
- 2FA（TOTP）：
  - 绑定/验证/关闭流程
  - 恢复码或重置策略（至少给管理员可操作路径）
  - 文档：`docs/SECURITY.md`（新建）或补 `docs/ARCHITECTURE.md`
- 测试：
  - allowlist 关键用例
  - 2FA 验证关键用例

依赖/注意：
- 需要明确与现有 `LX_SWITCH_TOKEN` 的共存：是否仍支持单 token 模式作为“超级管理员后门”（建议明确开关）

---

## C（后端权限/数据）：多用户与 RBAC

范围：数据模型、鉴权/授权中间件、管理 API

交付物：
- 用户表/角色表/权限点设计与迁移
- 权限点清单（providers/audits/activate/rollback/settings/users）
- API：用户/角色/授权管理（最小可用）
- 前后端联调约定（401/403 错误语义、登录态）
- 文档：更新 `docs/API.md` + `docs/ARCHITECTURE.md`
- 测试：授权绕过、最小权限、敏感操作拒绝

依赖/注意：
- 与 B 协调认证形态（session/cookie + token）
- 与 A 协调管理 UI 的页面/接口

---

## D（全栈/数据可视化）：审计 dashboard 指标化

范围：后端 metrics 聚合接口 + 前端展示

交付物：
- 指标口径定义（文档）
- 后端聚合接口（例如 `/api/metrics/...`）
- 前端 dashboard 页面（卡片 + 简单图表）
- 导出（可选）：CSV/JSON
- 文档：补 `docs/API.md`（metrics）与 `docs/PROJECT_OVERVIEW.md`（能力摘要）

依赖/注意：
- 如果 C 引入 RBAC，需要定义 metrics 的权限点（metrics:read）

---

## 建议里程碑/验收切片

- 切片 1（基础安全）：IP allowlist 上线 + 文档
- 切片 2（协作基础）：前端完成组件化脚手架 + API client + 2-3 个页面拆分
- 切片 3（权限）：RBAC 最小可用（3 角色）+ 敏感操作权限点
- 切片 4（可观测）：指标接口 + dashboard 第一版
