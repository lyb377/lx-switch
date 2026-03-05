# LX Switch 架构说明

## 1. 模块划分

当前代码以 `main.go` 为主，已开始模块化拆分：

1. **HTTP 路由层**
   - 页面路由：`/`、`/login`、`/logout`
   - API 路由：providers / activate / backups / rollback / audits / settings
2. **鉴权层**
   - 页面 cookie 鉴权（`withPageAuth`）
   - API token + cookie 兼容（`withAuth`）
   - 登录限流与锁定
3. **业务层**
   - Provider CRUD
   - 激活写入 + 连通性拦截
   - 导入导出（JSON / CC-Switch SQL）
   - 备份与回滚
4. **审计层**
   - 登录审计（`login_audits`）
   - 操作审计（`op_audits`）
   - 过滤、分页、CSV 导出、清理
5. **存储层**
   - SQLite 初始化/迁移（`initDB`）
   - 事务写入（导入/覆盖场景）
6. **前端层（当前为内嵌 HTML/JS）**
   - Provider 列表管理
   - 批导入/导出
   - 审计查看与过滤

## 2. 近期重构进展

- 已完成：鉴权与通用工具辅助逻辑从 `main.go` 拆分（降低主文件耦合）
- 目标：在不破坏既有 API 的前提下，持续拆分业务与存储逻辑

## 3. 核心数据表

- `providers`：provider 主数据
- `backups`：激活前配置备份记录
- `login_audits`：登录事件审计
- `op_audits`：操作审计

## 4. 关键流程

### 4.1 激活流程

1. 校验目标 provider
2. 连通性测试（失败则阻断）
3. 读取目标配置并创建备份
4. 写入新配置
5. 记录操作审计

### 4.2 批导入流程（JSON / CC-Switch）

1. 解析请求（JSON 或 SQL）
2. 转换为统一 `SaveReq[]`
3. 逐条校验
4. 按冲突策略处理（skip/overwrite）
5. dry-run 时仅预览不落库
6. 记录审计并返回明细

### 4.3 审计查询流程

- 支持分页 + 条件过滤（action/target/from/to）
- 列表与导出共用同一查询参数链路

## 5. 现状与建议

### 现状

- 已具备可并行开发基础：后端主干重构启动，UI 可先行改造
- M3 收尾阶段仍有审计与导入链路的小幅变更

### 建议拆分（下一阶段）

- `internal/http/handlers`
- `internal/service`
- `internal/store`
- `internal/importers`（json_importer / ccswitch_importer）
- `web/`（前端静态资源）

这样可以让 UI 与后端持续并行，进一步降低冲突与回归风险。