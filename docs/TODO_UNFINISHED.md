# 未完成事项清单（M4 / Backlog）

> 目标：把“剩下哪些模块/事项没做”讲清楚，便于你后续分派给多人并行推进。

## 0. 总览

当前未完成的 4 个大项（与 `docs/ROADMAP.md` 一致）：

1. 前端组件化重构（拆出静态资源）
2. 更细粒度安全能力（IP allowlist / 2FA 可选）
3. 多用户与权限控制（RBAC）
4. 审计 dashboard 指标化

---

## 1. 前端组件化重构（静态资源已抽离，后续组件化未完成）

### 已有基础
- 当前 UI 已从静态文件提供：`lx-switch/web/index.html`、`lx-switch/web/login.html`
- 静态资源：`lx-switch/web/static/app.js|app.css|login.css`

### 未完成模块/事项
- 前端工程化：
  - 目录结构与模块拆分（例如：pages/components/apiClient/state/utils）
  - 请求层封装（统一错误处理、401 跳转登录、toast/提示）
  - 类型/数据结构整理（至少在 JS 层做 schema 约束，或逐步引入 TS）
- 组件拆分与可维护性：
  - Provider 列表/筛选/详情/编辑弹窗拆组件
  - 导入（JSON/CC-Switch）工作流拆组件：预检/冲突/结果展示
  - 审计列表（登录/操作）复用组件：分页、日期范围、导出
- 资产构建与发布：
  - 若采用构建工具（Vite/webpack/esbuild），需要确定产物落点与服务端静态目录对接
  - 缓存策略（ETag/Cache-Control）与版本指纹（hash）策略
- UI/UX 基线：
  - 表单校验、loading/disabled 状态
  - 错误提示一致化（与后端错误语义对齐）

交付定义建议：
- 以“可持续迭代”为标准：能让前端多人协作改 UI，而不是在一个巨大 app.js 里改。

---

## 2. 更细粒度安全能力（IP allowlist / 2FA 可选）

### 未完成模块/事项
- IP allowlist：
  - 配置来源：env / config file / 管理 UI（建议先 env 或 config file）
  - 适用范围：全站/仅管理 API/仅敏感操作（activate/import/delete/rollback）
  - 可信 IP 获取：`X-Forwarded-For` 与反代信任链（Nginx/Cloudflare 场景必须定义信任策略）
  - 审计：拒绝访问也要记审计（ip、path、reason）
- 2FA（可选）：
  - 方案选择：TOTP（Google Authenticator）优先
  - 秘钥绑定/恢复码/重置流程
  - 登录态：是否二次校验、remember device
  - UI：绑定、验证、关闭 2FA

风险点：
- 反向代理下真实 IP 的信任边界；实现不当会导致 allowlist 形同虚设。

---

## 3. 多用户与权限控制（RBAC）

### 未完成模块/事项
- 用户体系：
  - 用户表、密码策略、锁定/重置
  - 初始管理员引导与迁移策略（已有单 token 模式如何过渡）
- RBAC 模型：
  - 角色（admin/operator/viewer 等）
  - 权限点（providers:read/write, activate, rollback, audits:read/export, settings:write...）
  - 资源域（按 target 分域？按 provider 分组？）
- 会话与鉴权：
  - session/cookie 机制与 API token 共存策略
  - 审计：用户维度记录到 login/op audit
- 管理 UI：
  - 用户/角色/授权管理页面

交付定义建议：
- 最小可用：2-3 个内置角色 + 权限点覆盖敏感操作，先不做复杂资源域。

---

## 4. 审计 dashboard 指标化

### 未完成模块/事项
- 指标口径定义：
  - 登录次数、失败次数、失败原因分布
  - 激活次数、回滚次数、导入次数、导入失败率
  - 按时间窗口（24h/7d/30d）聚合
- 后端聚合接口：
  - metrics endpoints（建议只读）
  - SQL 聚合查询与性能（索引、limit）
- 前端可视化：
  - 简单图表（折线/柱状）或至少卡片化数值
  - 筛选（时间范围、target、action）
- 导出：
  - 指标导出 CSV/JSON（可选）

交付定义建议：
- 第一版只做 4-8 个核心指标 + 2 个图，避免做成 BI。
