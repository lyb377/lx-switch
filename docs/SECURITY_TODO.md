# Security TODO（M4）

## IP allowlist
- [ ] 配置方式：env/config（二选一或都支持）
- [ ] 真实 IP 解析：X-Forwarded-For / CF-Connecting-IP 信任边界
- [ ] 适用范围：全站 or 仅敏感接口（activate/import/delete/rollback/settings）
- [ ] 拒绝访问审计
- [ ] 测试用例（allow/deny/边界）

## 2FA（TOTP，可选）
- [ ] 用户绑定 TOTP（生成 secret + QR）
- [ ] 登录二次校验流程
- [ ] 关闭/重置 2FA（含管理员路径）
- [ ] 恢复码（可选）
- [ ] 测试用例（正确/错误/过期/重放）
