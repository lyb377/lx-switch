# UI Handoff（交接给前端/设计）

## 1. 现状

- 前端为内嵌在 `main.go` 的 HTML/CSS/JS
- 目标是快速可用，当前 UI 偏运维工具风格
- 功能完整性优先于视觉一致性

## 2. 页面结构

当前主页面模块：

1. Provider 管理
   - 列表、编辑、删除、激活、测试
2. Provider 批量导入导出
   - JSON 导入/预检
   - CC-Switch SQL 导入/预检
   - 导出 Provider JSON
3. 审计与维护
   - 登录审计
   - 操作审计（含 action/target/from/to 过滤）
   - 导出 CSV
   - 审计清理/设置

## 3. API 清单（供 UI 调用）

### Provider
- `GET /api/providers?search=&target=`
- `POST /api/providers`
- `DELETE /api/providers/{id}`
- `POST /api/providers/test`
- `POST /api/providers/test-batch?search=&target=`
- `POST /api/providers/import`
- `POST /api/providers/import-cc`
- `GET /api/providers/export?search=&target=`

### 激活/备份
- `POST /api/activate`
- `GET /api/backups`
- `POST /api/rollback`

### 审计
- `GET /api/login-audits?limit=&offset=`
- `GET /api/login-audits/export?limit=`
- `GET /api/op-audits?limit=&offset=&action=&target=&from=&to=`
- `GET /api/op-audits/export?limit=&action=&target=&from=&to=`
- `POST /api/audits/cleanup?keepDays=`
- `GET /api/audits/settings`
- `POST /api/audits/settings`

### 认证
- 页面登录：`/login`
- API：`token` query 或 `X-Admin-Token`，并兼容 cookie

## 4. UI 优化建议（优先级）

### P0（立即）
- 统一视觉层级（标题、卡片、按钮）
- 表格可读性优化（固定表头/行 hover/长文本折叠）
- 状态反馈标准化（loading/success/error toast）

### P1（短期）
- 暗色模式
- 操作确认弹窗统一化
- 响应式布局（移动端只保留关键操作）

### P2（中期）
- 组件化前端（Vue/React 任一）
- 前后端静态资源分离
- 更完整的权限模型（多用户 RBAC）

## 5. 与后端协作约定

- 保持现有 API 兼容；新增字段遵循向后兼容
- 列表与导出必须共用同一筛选条件
- 大字段（如 SQL 导入内容）请求体限制与错误提示要明确
- 任何破坏性改动需附迁移说明

## 6. 并行开发结论（2026-03-05）

结论：**UI 可以开始并行改造**。

- 后端主流程重构已完成（`main.go` 的鉴权与通用工具逻辑已拆分）
- 现阶段适合前端先行推进样式系统、交互一致性、页面结构整理
- 预计仍有少量接口字段/筛选参数补充，属于收尾联调范围

## 7. UI 改造范围建议（可先改 / 暂缓）

### 可先改（建议立即开工）

1. Provider 列表与编辑页面
   - 表格样式、筛选交互、按钮层级、空状态
2. 导入导出页面基础交互
   - 导入流程步骤化、dry-run 结果展示、错误提示规范化
3. 全局视觉系统
   - 颜色/间距/按钮规范、toast 反馈、弹窗统一
4. 页面布局
   - 模块分区、响应式断点、信息层级优化

### 暂缓到后端收尾后再定稿（可先做占位）

1. 登录审计 `from/to` 时间过滤（列表+导出）相关筛选 UI
2. CC-Switch 导入映射报告下载入口与结果页
3. 导入容错增强后对应的错误码/提示文案最终版

## 8. 联调检查清单（给前后端）

- 登录审计与操作审计：列表筛选条件与导出条件必须严格一致
- 日期筛选：统一支持 `YYYY-MM-DD` 与 RFC3339（前端显示与入参保持一致）
- 导入能力：dry-run 与正式导入返回结构保持字段兼容
- 错误处理：4xx 用于参数与业务校验，5xx 用于服务异常
- 兼容策略：新增字段允许前端忽略，禁止无通知删除既有字段

## 9. 版本协作建议

- 建议以「M3 收尾完成」作为一次联调冻结点
- UI 分支可先按模块拆 PR（Provider / 导入导出 / 审计）
- 若接口有变更，后端需在 `docs/API.md` 同步更新并在 PR 描述注明影响面
