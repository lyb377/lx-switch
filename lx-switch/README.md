# LX Switch (Server Edition)

轻量的服务器版 provider 切换面板（参考 cc-switch 思路），适合无桌面 Linux。

## 当前能力（v0.1+）

- Provider 管理（增/删/改/搜索/按 target 过滤）
- 一键激活（写入目标配置）+ 激活前连通性检测
- 激活前自动备份 + 备份列表 + 回滚
- 批量导入/导出（JSON）
  - 支持 `dryRun` 预检
  - 支持冲突策略：`skip` / `overwrite`
- CC-Switch SQLite 导出 SQL 兼容导入（第一版）
- 登录审计 + 操作审计（分页/过滤/CSV 导出）
- 审计清理（手动/自动）与运行时设置
- 单用户 Token 鉴权 + 登录防爆破

## 支持目标

- `openclaw` -> `/root/.openclaw/config.json`
- `claude` -> `/root/.claude.json`
- `codex` -> `/root/.codex/config.toml`
- `gemini` -> `/root/.gemini/config.json`

## 本地运行

```bash
export LX_SWITCH_TOKEN='your-strong-token'
export LX_SWITCH_LISTEN=':18777'
export LX_SWITCH_DATA_DIR='/var/lib/lx-switch'

go run .
```

打开：`http://127.0.0.1:18777`

## API（核心）

- `GET /api/providers?search=&target=`
- `POST /api/providers`
- `DELETE /api/providers/{id}`
- `POST /api/providers/test`
- `POST /api/providers/test-batch?search=&target=`
- `POST /api/providers/import`
- `POST /api/providers/import-cc`
- `GET /api/providers/export?search=&target=`
- `POST /api/activate`
- `GET /api/backups`
- `POST /api/rollback`
- `GET /api/login-audits`
- `GET /api/op-audits`

请求头：`X-Admin-Token: <token>`（也支持登录 cookie / token query）

## 文档

- `docs/PROJECT_OVERVIEW.md`
- `docs/ARCHITECTURE.md`
- `docs/UI_HANDOFF.md`
- `docs/ROADMAP.md`
- `docs/PROGRESS.md`
