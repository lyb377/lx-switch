# Security（M4）

本文描述 `lx-switch` 的安全能力与推荐部署方式：IP allowlist、真实 IP 解析策略、RBAC/会话、2FA(TOTP) 与恢复路径。

## 1) 鉴权模式与兼容性

系统同时支持两种模式（可共存）：

- **Legacy 管理员 Token**：通过 `LX_SWITCH_TOKEN`（`X-Admin-Token` / `?token=` / `lx_token` cookie）。
  - 用于向后兼容旧 UI/脚本。
  - 在 RBAC 体系中会被视为内置管理员用户（用户名 `admin`，角色 `admin`）。
- **RBAC 多用户会话**：`POST /api/auth/login` 登录后设置 `lx_session`（HttpOnly cookie）。
  - 支持按角色/权限点控制访问（401/403 语义见 `docs/API.md`）。

建议：生产环境仍保留 `LX_SWITCH_TOKEN` 作为“超级管理员恢复通道”，并确保强度足够（随机长串）。

## 2) IP allowlist（IP 白名单）

### 2.1 启用方式

- 环境变量（启动时生效）：
  - `LX_SWITCH_IP_ALLOWLIST_ENABLED=true|false`
  - `LX_SWITCH_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8,...`
- 运行时设置（写入 DB state，立即生效）：
  - `GET/POST /api/security/settings`
  - `GET/POST/PUT/DELETE /api/security/ip-allowlist`

注意：
- 如果你仅通过 env 启用 allowlist，但没有提前写入可用条目，可能会把自己锁在门外。
- 通过 `POST /api/security/settings` 启用 allowlist 时，服务会校验：必须存在至少一个 enabled 条目，并且当前请求 IP 必须在 allowlist 内（避免误操作锁死）。

### 2.2 真实 IP 解析（反代信任链）

**原则：只有当 `RemoteAddr` 属于 `trustedProxies` 时，才会信任任何转发头。**

解析优先级（在 `RemoteAddr` 被信任时）：

1. `CF-Connecting-IP`（Cloudflare 场景）
2. `X-Forwarded-For`（按链路从右到左回溯，找到第一个“非受信代理”的 IP）
3. `X-Real-IP`
4. 否则回退到 `RemoteAddr`

你需要把“最后一跳反代”的 IP/CIDR（例如 Nginx/Traefik 所在网段或 `127.0.0.1`）加入 `LX_SWITCH_TRUSTED_PROXIES`，否则 `X-Forwarded-For` 会被忽略。

### 2.3 拒绝访问审计

当 allowlist 拒绝请求时，会记录：

- `op_audits.action = security.ip_blocked`（包含 ip/path 等信息）
- `login_audits.reason = ip_not_allowed`（便于统计/追踪）

## 3) 2FA（TOTP，可选）

### 3.1 用户绑定与验证

- `POST /api/totp/enable`：为当前登录用户生成 secret + otpauth URL + QR（PNG data URL）
- `POST /api/totp/confirm`：提交 `secret + code` 完成绑定（需要与当前用户匹配）
- `POST /api/totp/disable`：提交当前 TOTP code 关闭

登录时：

- `POST /api/auth/login`：若用户启用了 TOTP，必须额外提供 `totpCode` 或 `recoveryCode`（二选一）。

### 3.2 恢复/重置策略（管理员路径）

启用 TOTP 后（`POST /api/totp/confirm` 成功），接口会返回 **一次性展示** 的 `recoveryCodes`（建议立即离线保存）。登录时若无法提供 `totpCode`，可改用 `recoveryCode`。

恢复/重置相关接口：

- `POST /api/totp/recovery/regenerate`：为当前用户重新生成恢复码（需要提供当前 `code`，恢复码不能用于再生成）。
- `POST /api/totp/admin-disable`（需要 `users:write`）：为指定用户强制关闭 TOTP（同时清理恢复码与会话），用于“设备丢失/无法验证”的管理员恢复路径。

相关操作会记录 `op_audits.action = security.totp.*`。

## 4) RBAC 权限点（摘要）

权限点清单见代码常量（`rbac.go`）与 `docs/API.md`。内置角色（admin/editor/operator/viewer）在首次启动时自动初始化。

## 5) 部署建议（生产）

- 强制 HTTPS（反代终止 TLS 也可），避免在明文链路传输 token/密码。
- 设置高强度 `LX_SWITCH_TOKEN`，并定期轮换。
- 仅在反代确实可控时设置 `trustedProxies`；不要随意信任公网网段。
- 开启 allowlist 前先写入至少一个 **enabled** 条目，并确认当前出口 IP 会被允许。
- 关注 `op_audits` 中的 `rbac.denied` / `security.ip_blocked` / `security.totp.*` 作为安全告警线索。
