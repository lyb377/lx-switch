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
