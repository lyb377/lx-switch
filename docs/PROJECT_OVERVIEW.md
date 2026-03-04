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
- 审计：登录审计、操作审计、CSV 导出、时间范围过滤
- 维护：手动清理审计、自动每日清理、运行时审计设置

## 6. 文档索引

- `docs/ARCHITECTURE.md`：模块与数据流
- `docs/UI_HANDOFF.md`：UI 接手与改造指引
- `docs/ROADMAP.md`：里程碑计划
- `docs/PROGRESS.md`：当前进度/已完成/待办
