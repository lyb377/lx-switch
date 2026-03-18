# Security TODO（M4）

## IP allowlist
- [x] 配置方式：env/config（二选一或都支持）
- [x] 真实 IP 解析：X-Forwarded-For / CF-Connecting-IP 信任边界
- [x] 适用范围：全站 or 仅敏感接口（activate/import/delete/rollback/settings）
- [x] 拒绝访问审计
- [x] 测试用例（allow/deny/边界）

## 2FA（TOTP，可选）
- [x] 用户绑定 TOTP（生成 secret + QR）
- [x] 登录二次校验流程
- [x] 关闭/重置 2FA（含管理员路径）
- [ ] 恢复码（后续迭代）
- [x] 测试用例（正确/错误/过期/重放）
