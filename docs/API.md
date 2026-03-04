# API Reference (lx-switch)

## 认证

支持三种方式：

1. Header：`X-Admin-Token: <token>`
2. Query：`?token=<token>`
3. 登录后 Cookie（页面会话）

---

## Providers

### GET /api/providers

查询 provider 列表。

Query:
- `search` (optional)
- `target` (optional, `openclaw|claude|codex|gemini`)

Response: `Provider[]`

### POST /api/providers

新增或更新 provider。

Body:
```json
{
  "id": 0,
  "name": "demo",
  "target": "openclaw",
  "baseUrl": "https://cli.lyb123.top/v1",
  "apiKey": "sk-xxx",
  "model": "gpt-5.3-codex",
  "notes": "optional"
}
```

### DELETE /api/providers/{id}

删除 provider。

### GET /api/providers/export

导出 provider JSON。

Query:
- `search` (optional)
- `target` (optional)

### POST /api/providers/import

JSON 批量导入。

Body:
```json
{
  "items": [
    {
      "name": "demo",
      "target": "openclaw",
      "baseUrl": "https://cli.lyb123.top/v1",
      "apiKey": "sk-xxx",
      "model": "gpt-5.3-codex",
      "notes": ""
    }
  ],
  "mode": "skip",
  "dryRun": true,
  "previewLimit": 30
}
```

`mode`: `skip|overwrite`

### POST /api/providers/import-cc

导入 CC-Switch SQLite SQL dump（第一版兼容）。

Body:
```json
{
  "sql": "-- sqlite dump text ...",
  "mode": "skip",
  "dryRun": true,
  "previewLimit": 30
}
```

说明：解析 `INSERT INTO "providers" ...`，映射 `app_type -> target`，从 `settings_config` 提取 `baseUrl/apiKey/model`。

### POST /api/providers/test

测试单个 provider 连通性。

Body:
```json
{ "providerId": 1 }
```

### POST /api/providers/test-batch

按当前过滤批量测试连通性。

Query:
- `search` (optional)
- `target` (optional)

---

## 激活/回滚

### POST /api/activate

激活指定 provider（会先做连通性校验，失败会阻断）。

Body:
```json
{ "providerId": 1 }
```

### GET /api/backups

查询备份列表。

### POST /api/rollback

按备份回滚。

Body:
```json
{ "backupId": 10 }
```

---

## 审计

### GET /api/login-audits

查询登录审计。

Query:
- `limit` (default 50)
- `offset` (default 0)

### GET /api/login-audits/export

导出登录审计 CSV。

Query:
- `limit` (default 2000)

### GET /api/op-audits

查询操作审计。

Query:
- `limit`
- `offset`
- `action` (optional)
- `target` (optional)
- `from` (optional, `YYYY-MM-DD` 或 RFC3339)
- `to` (optional, `YYYY-MM-DD` 或 RFC3339)

### GET /api/op-audits/export

导出操作审计 CSV（支持与列表一致过滤）。

Query:
- `limit`
- `action`
- `target`
- `from`
- `to`

### POST /api/audits/cleanup

清理旧审计。

Query:
- `keepDays` (>=1)

### GET /api/audits/settings

读取审计保留设置。

### POST /api/audits/settings

更新审计保留设置。

Body:
```json
{
  "auditRetentionDays": 30,
  "auditCleanupEnabled": true
}
```

---

## 元信息与认证页面

- `GET /api/meta`：系统元信息（activeProvider、firstRun、tokenWeak 等）
- `GET /login` / `POST /login` / `POST /logout`

---

## 错误语义（通用）

- `400`：参数错误/校验失败
- `401`：未授权
- `405`：方法不允许
- `500`：服务内部错误
