# Security Features

本文档描述 lx-switch 的安全功能实现细节。

## 目录

- [IP Allowlist](#ip-allowlist)
- [Two-Factor Authentication (2FA)](#two-factor-authentication-2fa)
- [Role-Based Access Control (RBAC)](#role-based-access-control-rbac)
- [Session Management](#session-management)
- [Rate Limiting](#rate-limiting)

---

## IP Allowlist

### 功能概述

IP 白名单功能允许管理员限制只有特定 IP 地址或 CIDR 范围的客户端可以访问系统。

### 配置

通过环境变量配置：

```bash
# 启用 IP 白名单
LX_SWITCH_IP_ALLOWLIST_ENABLED=true

# 配置可信代理（用于获取真实 IP）
LX_SWITCH_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
```

### 数据库管理

IP 白名单存储在 `ip_allowlist` 表中：

```sql
CREATE TABLE ip_allowlist (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip_cidr TEXT NOT NULL,
  description TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);
```

### API 接口

- `GET /api/security/ip-allowlist` - 列出所有白名单条目
- `POST /api/security/ip-allowlist` - 添加白名单条目
- `PUT /api/security/ip-allowlist/:id` - 更新白名单条目
- `DELETE /api/security/ip-allowlist/:id` - 删除白名单条目

### 真实 IP 获取策略

在反向代理场景下，系统使用以下策略获取客户端真实 IP：

1. 检查 `RemoteAddr` 是否在可信代理列表中
2. 如果是可信代理，从 `X-Forwarded-For` 头中提取 IP 链
3. 从右向左遍历 IP 链，找到第一个不在可信代理列表中的 IP
4. 该 IP 即为客户端真实 IP

**重要**：必须正确配置 `LX_SWITCH_TRUSTED_PROXIES`，否则攻击者可以伪造 `X-Forwarded-For` 头绕过白名单。

### 审计

所有被 IP 白名单拒绝的访问都会记录到操作审计表：

```
action: security.ip_blocked
target: <client_ip>
detail: method=<HTTP_METHOD> path=<REQUEST_PATH> xff=<X-Forwarded-For>
```

---

## Two-Factor Authentication (2FA)

### 功能概述

系统支持基于 TOTP (Time-based One-Time Password) 的双因素认证，兼容 Google Authenticator、Authy 等标准 TOTP 应用。

### 实现细节

- 算法：HMAC-SHA1
- 时间步长：30 秒
- 代码长度：6 位数字
- 时钟偏移容忍：±1 个时间窗口（±30 秒）

### 启用流程

1. 用户调用 `POST /api/totp/enable` 生成 TOTP 密钥
2. 系统返回密钥和 QR 码 URI
3. 用户使用 TOTP 应用扫描 QR 码
4. 用户调用 `POST /api/totp/confirm` 并提供验证码
5. 验证成功后，2FA 正式启用

### 禁用流程

用户调用 `POST /api/totp/disable` 并提供当前密码，验证通过后禁用 2FA。

### 登录流程

启用 2FA 的用户登录时：

1. 提供用户名和密码
2. 如果密码正确但未提供 TOTP 代码，返回 `totp_required` 错误
3. 前端提示用户输入 2FA 代码
4. 用户重新提交包含 TOTP 代码的登录请求
5. 验证通过后创建会话

### 恢复策略

**当前版本**：如果用户丢失 TOTP 设备，需要管理员手动在数据库中禁用该用户的 2FA：

```sql
UPDATE users SET totp_enabled = 0, totp_secret = '' WHERE username = '<username>';
```

**未来改进**：计划添加恢复码功能。

---

## Role-Based Access Control (RBAC)

### 功能概述

RBAC 系统提供细粒度的权限控制，支持多用户协作。

### 数据模型

#### 用户表 (users)

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  email TEXT,
  role_id INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  totp_secret TEXT,
  totp_enabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (role_id) REFERENCES roles(id)
);
```

#### 角色表 (roles)

```sql
CREATE TABLE roles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  description TEXT,
  created_at TEXT NOT NULL
);
```

#### 角色权限表 (role_permissions)

```sql
CREATE TABLE role_permissions (
  role_id INTEGER NOT NULL,
  permission TEXT NOT NULL,
  PRIMARY KEY (role_id, permission),
  FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
);
```

### 内置角色

系统提供三个内置角色：

#### 1. Admin（管理员）

拥有所有权限：

- `providers:read` - 查看 Provider
- `providers:write` - 创建/编辑/删除 Provider
- `activate` - 激活 Provider
- `rollback` - 回滚配置
- `audits:read` - 查看审计日志
- `audits:export` - 导出审计日志
- `settings:write` - 修改系统设置
- `users:read` - 查看用户
- `users:write` - 管理用户
- `metrics:read` - 查看指标

#### 2. Operator（操作员）

日常操作权限：

- `providers:read`
- `providers:write`
- `activate`
- `audits:read`
- `metrics:read`

**不包含**：回滚、用户管理、系统设置

#### 3. Viewer（只读用户）

只读权限：

- `providers:read`
- `audits:read`
- `metrics:read`

### 权限检查

使用 `withRBACAuth` 中间件进行权限检查：

```go
mux.HandleFunc("/api/providers",
    a.withIPAllowlist(
        a.withRBACAuth(PermProvidersRead, a.handleProviders)))
```

### 向后兼容

系统保留对旧版 `LX_SWITCH_TOKEN` 的支持：

- 如果请求携带有效的 `LX_SWITCH_TOKEN`，视为管理员权限（user_id = 1）
- 建议迁移到 RBAC 后禁用 token 模式

### 初始化

首次启动时，系统会：

1. 创建三个内置角色
2. 为每个角色分配权限

**重要**：首次部署后，管理员需要手动创建第一个管理员用户：

```sql
-- 获取 admin 角色 ID
SELECT id FROM roles WHERE name = 'admin';  -- 假设返回 1

-- 创建管理员用户（密码需要使用 bcrypt 哈希）
INSERT INTO users (username, password_hash, email, role_id, enabled, totp_enabled, created_at, updated_at)
VALUES ('admin', '<bcrypt_hash>', 'admin@example.com', 1, 1, 0, datetime('now'), datetime('now'));
```

或通过 API 创建（需要先用 legacy token 认证）。

---

## Session Management

### 会话机制

- 会话存储在 `sessions` 表中
- 会话 token 为 32 字节随机值，Base64 URL 编码
- 默认有效期：24 小时
- 会话通过 `lx_session` cookie 传递

### Cookie 属性

```go
http.SetCookie(w, &http.Cookie{
    Name:     "lx_session",
    Value:    sessionToken,
    Path:     "/",
    HttpOnly: true,              // 防止 XSS
    Secure:   true,              // HTTPS only
    SameSite: http.SameSiteStrictMode,  // CSRF 防护
    MaxAge:   86400,             // 24 hours
})
```

### 会话清理

系统每小时自动清理过期会话：

```go
func (a *App) startSessionCleanupLoop() {
    ticker := time.NewTicker(1 * time.Hour)
    for range ticker.C {
        a.cleanupExpiredSessions()
    }
}
```

---

## Rate Limiting

### 登录速率限制

防止暴力破解攻击：

- 默认配置：6 次失败尝试后锁定 15 分钟
- 基于客户端 IP 地址
- 锁定期间返回 `429 Too Many Requests`

### 配置

```bash
LX_SWITCH_MAX_LOGIN_ATTEMPTS=6      # 最大失败次数
LX_SWITCH_LOGIN_WINDOW_SEC=300      # 统计窗口（秒）
LX_SWITCH_LOGIN_LOCK_SEC=900        # 锁定时长（秒）
```

### 实现

```go
type attemptState struct {
    Count       int
    FirstFailed time.Time
    LockUntil   time.Time
    LastFailed  time.Time
}
```

- 失败尝试存储在内存中（重启后清空）
- 成功登录后清除该 IP 的失败记录

---

## 安全最佳实践

### 部署建议

1. **启用 HTTPS**
   - 使用反向代理（Nginx/Caddy）终止 TLS
   - 配置 `X-Forwarded-Proto` 头

2. **配置 IP 白名单**
   - 生产环境强烈建议启用
   - 正确配置可信代理列表

3. **强制 2FA**
   - 为管理员账户启用 2FA
   - 定期审计未启用 2FA 的账户

4. **定期审计**
   - 检查登录审计日志
   - 监控 `security.ip_blocked` 事件
   - 定期审查用户权限

5. **密码策略**
   - 使用强密码（建议 12+ 字符）
   - 定期更换密码
   - 禁用不活跃账户

### 安全更新

- 定期更新依赖库（特别是 `golang.org/x/crypto`）
- 关注 SQLite 安全公告
- 订阅项目安全通知

---

## 已知限制

1. **会话存储**
   - 当前使用 SQLite，单机部署
   - 多实例部署需要共享会话存储（Redis/PostgreSQL）

2. **2FA 恢复**
   - 当前版本无恢复码功能
   - 用户丢失设备需管理员手动重置

3. **密码策略**
   - 无强制密码复杂度要求
   - 无密码过期机制

4. **审计日志**
   - 无防篡改机制
   - 建议定期导出到外部系统

---

## 安全报告

如发现安全漏洞，请通过以下方式报告：

- 邮件：security@example.com
- GitHub Security Advisory（私密）

**请勿公开披露未修复的漏洞。**
