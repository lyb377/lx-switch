# LX Switch 架构说明

## 1. 模块划分

当前代码以 `main.go` 为主，已开始模块化拆分：

1. **HTTP 路由层**
   - 页面路由：`/`、`/login`、`/logout`
   - API 路由：providers / activate / backups / rollback / audits / settings / users / roles / totp
2. **鉴权层**
   - 页面 cookie 鉴权（`withPageAuth`）
   - API token + cookie 兼容（`withAuth`）
   - RBAC 权限鉴权（`withRBACAuth`）- **M4 新增**
   - 登录限流与锁定
   - IP 白名单（`withIPAllowlist`）
3. **业务层**
   - Provider CRUD
   - 激活写入 + 连通性拦截
   - 导入导出（JSON / CC-Switch SQL）
   - 备份与回滚
   - 用户管理（CRUD）- **M4 新增**
   - 角色与权限管理 - **M4 新增**
   - 2FA (TOTP) - **M4 新增**
4. **审计层**
   - 登录审计（`login_audits`）
   - 操作审计（`op_audits`）
   - 过滤、分页、CSV 导出、清理
5. **存储层**
   - SQLite 初始化/迁移（`initDB`, `initRBACSchema`）
   - 事务写入（导入/覆盖场景）
   - 会话管理（`sessions` 表）- **M4 新增**
6. **前端层（静态文件托管，待组件化）**
   - `lx-switch/web/index.html` / `lx-switch/web/login.html`
   - `lx-switch/web/static/*`（app.js/app.css/login.css 等）
   - Provider 列表管理、导入导出、审计查看与过滤

## 2. 近期重构进展

- 已完成：鉴权与通用工具辅助逻辑从 `main.go` 拆分（降低主文件耦合）
- 目标：在不破坏既有 API 的前提下，持续拆分业务与存储逻辑

## 3. 核心数据表

- `providers`：provider 主数据
- `backups`：激活前配置备份记录
- `login_audits`：登录事件审计
- `op_audits`：操作审计
- `ip_allowlist`：IP 白名单配置
- **M4 新增：**
  - `users`：用户表
  - `roles`：角色表
  - `role_permissions`：角色权限关联表
  - `sessions`：会话表

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

### 4.4 RBAC 认证与授权流程（M4 新增）

1. **用户登录**
   - 验证用户名和密码（bcrypt）
   - 检查用户是否启用
   - 如果启用 2FA，验证 TOTP 代码
   - 创建会话（24 小时有效期）
   - 设置 `lx_session` cookie

2. **权限检查**
   - 从请求中提取会话 token
   - 验证会话有效性（未过期）
   - 获取用户 ID 和角色
   - 检查角色是否拥有所需权限
   - 允许或拒绝访问（403 Forbidden）

3. **向后兼容**
   - 保留对 `LX_SWITCH_TOKEN` 的支持
   - Legacy token 视为管理员权限（user_id = 1）

### 4.5 IP 白名单流程（M4 新增）

1. 检查是否启用 IP 白名单
2. 获取客户端真实 IP（考虑反向代理）
3. 检查 IP 是否在白名单中
4. 拒绝访问时记录审计（`security.ip_blocked`）

## 5. 现状与建议

### 现状

- 已具备可并行开发基础：后端主干重构启动，UI 可先行改造
- M3 收尾阶段仍有审计与导入链路的小幅变更

### 建议拆分（下一阶段）

- `internal/http/handlers`
- `internal/service`
- `internal/store`
- `internal/importers`（json_importer / ccswitch_importer）
- `internal/rbac`（用户、角色、权限管理）
- `internal/security`（IP 白名单、2FA、会话管理）
- `web/`（前端静态资源）

## 6. 安全架构（M4）

### 6.1 认证层次

1. **IP 白名单**（最外层）
   - 可选启用
   - 基于 CIDR 匹配
   - 支持反向代理场景

2. **用户认证**
   - 用户名 + 密码（bcrypt）
   - 可选 2FA (TOTP)
   - 会话管理（24 小时）

3. **权限授权**
   - 基于角色的权限控制
   - 细粒度权限点
   - 三个内置角色：admin / operator / viewer

### 6.2 安全特性

- **密码安全**：bcrypt 哈希（cost 10）
- **会话安全**：HttpOnly + Secure + SameSite cookies
- **2FA**：TOTP (RFC 6238)，30 秒时间窗口
- **速率限制**：登录失败锁定（默认 6 次/15 分钟）
- **审计日志**：所有敏感操作记录
- **IP 白名单**：可信 IP 访问控制

详见 `docs/SECURITY.md`。

这样可以让 UI 与后端持续并行，进一步降低冲突与回归风险。