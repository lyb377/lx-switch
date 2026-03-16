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

导入 CC-Switch SQLite SQL dump（增强版兼容）。

Body:
```json
{
  "sql": "-- sqlite dump text ...",
  "mode": "skip",
  "dryRun": true,
  "previewLimit": 30
}
```

说明：
- 解析 `INSERT INTO providers ... VALUES (...),(...);` 多行语句（兼容 `"providers"`/`` `providers` ``/`[providers]` 写法）
- 映射 `app_type -> target`
- 从 `settings_config` 提取 `baseUrl/apiKey/model`
- 返回 `mappingReport`（逐行映射结果与跳过原因）

### POST /api/providers/import-cc/report

生成并下载 CC-Switch 导入映射报告（CSV）。

Body:
```json
{
  "sql": "-- sqlite dump text ..."
}
```

Response:
- `text/csv` 附件下载，包含逐行映射状态与 summary。

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
- `from` (optional, `YYYY-MM-DD` 或 RFC3339)
- `to` (optional, `YYYY-MM-DD` 或 RFC3339)

### GET /api/login-audits/export

导出登录审计 CSV。

Query:
- `limit` (default 2000)
- `from` (optional, `YYYY-MM-DD` 或 RFC3339)
- `to` (optional, `YYYY-MM-DD` 或 RFC3339)

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

## Metrics

### GET /api/metrics/summary

获取审计指标聚合数据。

Query:
- `window` (optional, `24h|7d|30d`, default: `24h`)

Response:
```json
{
  "window": "24h",
  "login": {
    "total": 150,
    "success": 145,
    "failed": 5,
    "successRate": 96.67,
    "uniqueIPs": 12
  },
  "operations": {
    "activate": {
      "total": 50,
      "failed": 2,
      "failureRate": 4.0
    },
    "rollback": {
      "total": 3,
      "failed": 0,
      "failureRate": 0.0
    },
    "providers.import": {
      "total": 5,
      "failed": 1,
      "failureRate": 20.0
    }
  },
  "byTarget": {
    "openclaw": 25,
    "claude": 15,
    "codex": 8,
    "gemini": 2
  }
}
```

说明：
- `login`: 登录指标（总数、成功、失败、成功率、独立 IP 数）
- `operations`: 操作指标（按 action 分类，包含总数、失败数、失败率）
- `byTarget`: 按 target 分类的激活次数统计

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

---

## 联调约定（前后端）

- 列表查询与导出接口必须使用同一组筛选参数语义
- 日期过滤推荐统一入参：`YYYY-MM-DD` 或 RFC3339

---

## RBAC & Authentication (M4)

### 认证方式

系统支持两种认证方式：

1. **Legacy Token**（向后兼容）
   - Header: `X-Admin-Token: <token>`
   - Query: `?token=<token>`
   - Cookie: `lx_token=<token>`
   - 权限：管理员（所有权限）

2. **RBAC Session**（推荐）
   - Cookie: `lx_session=<session_token>`
   - 权限：基于用户角色

### POST /api/auth/login

用户登录（RBAC）。

Request Body:
```json
{
  "username": "admin",
  "password": "password123",
  "totpCode": "123456"  // 可选，启用 2FA 时必填
}
```

Response (成功):
```json
{
  "success": true,
  "user": {
    "id": 1,
    "username": "admin",
    "email": "admin@example.com",
    "role": "admin"
  }
}
```

Response (需要 2FA):
```json
{
  "error": "totp_required",
  "message": "2FA code required"
}
```

错误码：
- `401`: 用户名或密码错误、2FA 代码错误
- `403`: 用户已禁用
- `429`: 登录尝试次数过多

### POST /api/auth/logout

用户登出。

Response:
```json
{
  "success": true
}
```

---

## User Management

### GET /api/users

列出所有用户（需要 `users:read` 权限）。

Response:
```json
[
  {
    "id": 1,
    "username": "admin",
    "email": "admin@example.com",
    "roleId": 1,
    "roleName": "admin",
    "enabled": true,
    "totpEnabled": true,
    "createdAt": "2026-03-15T10:00:00Z",
    "updatedAt": "2026-03-15T10:00:00Z"
  }
]
```

### POST /api/users/create

创建新用户（需要 `users:write` 权限）。

Request Body:
```json
{
  "username": "operator1",
  "password": "SecurePass123!",
  "email": "operator@example.com",
  "roleId": 2
}
```

Response: `User` object (201 Created)

### PUT /api/users/update?id=<user_id>

更新用户信息（需要 `users:write` 权限）。

Request Body:
```json
{
  "email": "newemail@example.com",
  "roleId": 2,
  "enabled": true
}
```

Response: Updated `User` object

### DELETE /api/users/delete?id=<user_id>

删除用户（需要 `users:write` 权限）。

Response:
```json
{
  "success": true
}
```

---

## Role Management

### GET /api/roles

列出所有角色及其权限（需要 `users:read` 权限）。

Response:
```json
[
  {
    "id": 1,
    "name": "admin",
    "description": "Built-in admin role",
    "permissions": [
      "providers:read",
      "providers:write",
      "activate",
      "rollback",
      "audits:read",
      "audits:export",
      "settings:write",
      "users:read",
      "users:write",
      "metrics:read"
    ],
    "createdAt": "2026-03-15T10:00:00Z"
  },
  {
    "id": 2,
    "name": "operator",
    "description": "Built-in operator role",
    "permissions": [
      "providers:read",
      "providers:write",
      "activate",
      "audits:read",
      "metrics:read"
    ],
    "createdAt": "2026-03-15T10:00:00Z"
  },
  {
    "id": 3,
    "name": "viewer",
    "description": "Built-in viewer role",
    "permissions": [
      "providers:read",
      "audits:read",
      "metrics:read"
    ],
    "createdAt": "2026-03-15T10:00:00Z"
  }
]
```

---

## Two-Factor Authentication (2FA)

### POST /api/totp/enable

为当前用户生成 TOTP 密钥（需要登录）。

Response:
```json
{
  "secret": "JBSWY3DPEHPK3PXP",
  "uri": "otpauth://totp/lx-switch:admin?secret=JBSWY3DPEHPK3PXP&issuer=lx-switch"
}
```

前端应将 `uri` 生成 QR 码供用户扫描。

### POST /api/totp/confirm

确认并启用 2FA（需要登录）。

Request Body:
```json
{
  "code": "123456"
}
```

Response:
```json
{
  "success": true
}
```

错误码：
- `400`: 2FA 未初始化
- `401`: 验证码错误

### POST /api/totp/disable

禁用 2FA（需要登录）。

Request Body:
```json
{
  "password": "current_password"
}
```

Response:
```json
{
  "success": true
}
```

错误码：
- `401`: 密码错误

---

## Permission Points

系统定义的权限点：

| 权限点 | 说明 |
|--------|------|
| `providers:read` | 查看 Provider 列表 |
| `providers:write` | 创建/编辑/删除 Provider |
| `activate` | 激活 Provider |
| `rollback` | 回滚配置 |
| `audits:read` | 查看审计日志 |
| `audits:export` | 导出审计日志 |
| `settings:write` | 修改系统设置 |
| `users:read` | 查看用户列表 |
| `users:write` | 管理用户（创建/编辑/删除） |
| `metrics:read` | 查看指标数据 |

---

## Security Settings

### GET /api/security/settings

获取安全设置。

Response:
```json
{
  "ipAllowlistEnabled": true,
  "trustedProxies": ["10.0.0.0/8", "172.16.0.0/12"]
}
```

### POST /api/security/settings

更新安全设置（需要 `settings:write` 权限）。

Request Body:
```json
{
  "ipAllowlistEnabled": true,
  "trustedProxies": ["10.0.0.0/8"]
}
```

### GET /api/security/ip-allowlist

列出 IP 白名单条目（需要 `settings:write` 权限）。

Response:
```json
[
  {
    "id": 1,
    "ipCidr": "192.168.1.0/24",
    "description": "Office network",
    "enabled": true,
    "createdAt": "2026-03-15T10:00:00Z"
  }
]
```

### POST /api/security/ip-allowlist

添加 IP 白名单条目（需要 `settings:write` 权限）。

Request Body:
```json
{
  "ipCidr": "203.0.113.0/24",
  "description": "Remote office",
  "enabled": true
}
```

### PUT /api/security/ip-allowlist/{id}

更新 IP 白名单条目（需要 `settings:write` 权限）。

### DELETE /api/security/ip-allowlist/{id}

删除 IP 白名单条目（需要 `settings:write` 权限）。

---

## Error Codes (RBAC)

| 状态码 | 说明 |
|--------|------|
| `401 Unauthorized` | 未登录或会话过期 |
| `403 Forbidden` | 无权限执行该操作 |
| `429 Too Many Requests` | 登录尝试次数过多，账户已锁定 |

错误响应格式：
```json
{
  "error": "forbidden"
}
```

或

```json
{
  "error": "totp_required",
  "message": "2FA code required"
}
```

- 新增字段保持向后兼容（前端可忽略未知字段）
- 破坏性变更需提前在文档与 PR 中声明