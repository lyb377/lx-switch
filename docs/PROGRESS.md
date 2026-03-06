# 当前进度（2026-03-06）

## 状态

- 服务：`lx-switch` 运行中（active）
- 仓库：`git@github.com:lyb377/lx-switch.git`
- 分支：`master`
- 任务统计：**总任务 19 / 已办 15 / 待办 4**（按 `docs/ROADMAP.md` 勾选项统计）

## 最近关键提交

- `2122181` refactor(lx-switch): split auth and utility helpers from main.go
- `90c5284` feat: add cc-switch SQL import compatibility
- `331ae46` feat: add op-audit date range filter and export support
- `33b5dd7` feat: add runtime audit cleanup settings API and UI
- `79a554c` feat: add automatic daily audit cleanup with env toggle

## 本次完成（M3 收尾）

1. 登录审计 `from/to` 时间范围过滤（列表 + 导出）
   - 后端：`/api/login-audits`、`/api/login-audits/export` 支持 `from/to`（`YYYY-MM-DD` 或 RFC3339）
   - 前端：登录审计面板新增 from/to 输入、应用/清空过滤、导出沿用同一筛选参数
2. CC-Switch 导入映射报告下载
   - 新增接口：`POST /api/providers/import-cc/report`
   - 新增前端按钮：下载 CC 映射报告（CSV）
   - `import-cc` 响应新增 `mappingReport` 字段（逐行映射结果）
3. 导入容错增强（复杂 SQL 边界）
   - 支持多行 values 语句：`VALUES (...),(...);`
   - 支持 providers 表名多种引号写法：`"providers"` / `` `providers` `` / `[providers]`
   - 列解析与值解析增强（逗号、单双引号、括号嵌套）
   - 明细化跳过原因：`missing_app_type` / `unsupported_app_type` / `missing_name` / `missing_base_url` / `columns_values_mismatch`

## 验证结果

- 自动化测试：`go test ./...` ✅
- 新增测试：
  - `TestLoginAuditsFromToFilter`
  - `TestParseCCSwitchProvidersFromSQL_MultiRowAndReport`

## 当前风险与注意

- 前端仍为内嵌实现，M4 组件化改造尚未开始
- `main.go` 仍较大，但本次按“交付优先”未做额外重构

## 下一步（建议）

1. 推进 M4：前端组件化与静态资源拆分
2. 基于导入映射报告补充前端结果可视化（非仅下载）
3. 继续补齐更多 SQL 方言样本回归用例
