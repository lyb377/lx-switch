# LX Switch (Server Edition)

轻量的服务器版 provider 切换面板（参考 cc-switch 思路），适合无桌面 Linux。

## v0.1 功能

- Provider 管理（增/删）
- 一键激活（写入目标配置）
- 激活前自动备份
- 备份列表与回滚
- 单用户 Token 鉴权

## 支持目标

- `openclaw` -> `/root/.openclaw/config.json`
- `claude` -> `/root/.claude.json`
- `codex` -> `/root/.codex/config.toml`
- `gemini` -> `/root/.gemini/config.json`

> 注意：v0.1 是安全可用骨架，后续可迭代“真实配置格式适配器”。

## 本地运行

```bash
export LX_SWITCH_TOKEN='your-strong-token'
export LX_SWITCH_LISTEN=':18777'
export LX_SWITCH_DATA_DIR='/var/lib/lx-switch'

go run .
```

打开：`http://127.0.0.1:18777`

## API

- `GET /api/providers`
- `POST /api/providers`
- `DELETE /api/providers/{id}`
- `POST /api/activate`
- `GET /api/backups`
- `POST /api/rollback`

请求头：`X-Admin-Token: <token>`
