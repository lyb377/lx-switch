# LX Switch 项目总览

> Server-native 的 provider 管理与切换面板（headless 服务器可用），用于替代桌面版 CC Switch 的核心运维能力。

## 1. 项目目标

- 在无桌面 Linux 服务器上提供可视化 provider 管理能力
- 支持多目标配置写入：openclaw / claude / codex / gemini
- 提供可回滚、可审计、可批量导入导出的安全运维流程
- 逐步兼容 CC-Switch 数据迁移（当前已支持 SQL dump 导入第一版）

## 2. 技术栈

- 语言：Go
- Web：`net/http`（单二进制）
- 存储：SQLite（数据目录默认 `/var/lib/lx-switch`）
- 部署：systemd 服务（`lx-switch.service`）
- 反向代理：Nginx + Cloudflare（外部 HTTPS）

## 3. 运行方式

- 监听：`LX_SWITCH_LISTEN`（默认 `:18777`）
- 鉴权：`LX_SWITCH_TOKEN`（必填）
- 数据目录：`LX_SWITCH_DATA_DIR`
- 审计清理：
  - `LX_SWITCH_AUDIT_RETENTION_DAYS`
  - `LX_SWITCH_AUDIT_CLEANUP_ENABLED`

## 4. 目标配置映射

- `openclaw` -> `/root/.openclaw/config.json`
- `claude` -> `/root/.claude.json`
- `codex` -> `/root/.codex/config.toml`
- `gemini` -> `/root/.gemini/config.json`

## 5. 当前能力（摘要）

- Provider：增删改查、搜索筛选、激活、连通性测试
- 导入导出：JSON 导入导出、dry-run、冲突策略（skip/overwrite）
- CC-Switch 兼容：SQLite SQL dump 导入（`providers` 表 INSERT）
- 备份回滚：激活前备份、查看备份、按备份回滚
- 审计：登录审计、操作审计、CSV 导出、时间范围过滤（操作审计已完成）
- 指标 Dashboard：登录/操作指标聚合（24h/7d/30d），按 target 分类统计，可视化展示
- 维护：手动清理审计、自动每日清理、运行时审计设置

## 6. 当前里程碑状态（M4 规划中）

- 已完成（M1-M3）：
  - Provider 管理：搜索/过滤、导入导出、连通性测试
  - 激活/备份/回滚：激活前校验 + 备份、回滚能力
  - 审计：登录审计、操作审计、CSV 导出、时间范围过滤
  - CC-Switch 兼容：SQL dump 导入（含映射报告下载、复杂 SQL 容错）
  - 代码结构：鉴权与工具逻辑从 `main.go` 拆分（持续演进中）

- 待完成（M4 / Backlog）：
  - 前端组件化重构（拆出静态资源，推进模块化与可维护性）
  - 更细粒度安全能力（IP allowlist / 2FA 可选）
  - 多用户与权限控制（RBAC）

## 7. 协作结论（给产品/前端/后端）

- **UI 已可并行推进**（样式、交互、布局先行）
- 后端 M3 收尾阶段仍会有小幅接口/字段补充
- 建议以 M3 收尾完成作为一次联调冻结点

## 8. 文档索引

- `docs/ARCHITECTURE.md`：模块与数据流
- `docs/UI_HANDOFF.md`：UI 接手与改造指引
- `docs/API.md`：接口定义与错误语义
- `docs/ROADMAP.md`：里程碑计划
- `docs/PROGRESS.md`：当前进度/已完成/待办