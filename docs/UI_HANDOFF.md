# UI Handoff（交接给前端/设计）

## 1. 现状

- 前端已完成组件化重构，位于 `lx-switch/web/` 目录
- 使用 Vite 构建工具，原生 JavaScript 模块化开发
- 产物输出到 `lx-switch/web/static/` 由服务端托管
- 功能完整性优先于视觉一致性

## 1.1 新目录结构（2026-03 重构后）

```
lx-switch/web/
├── src/
│   ├── apiClient/          # API 调用封装
│   │   ├── index.js        # 统一请求、错误处理、401 重定向
│   │   └── toast.js        # Toast 提示组件
│   ├── components/         # 通用组件
│   │   ├── Button.js       # 按钮
│   │   ├── Card.js         # 卡片、Alert
│   │   ├── Form.js         # 表单元素（Input/Select/Textarea/Checkbox）
│   │   ├── Modal.js        # 对话框（alert/confirm/prompt）
│   │   ├── Table.js        # 表格、分页器
│   │   └── index.js        # 组件导出
│   ├── pages/              # 页面组件
│   │   ├── ProviderPage.js # Provider 管理页面
│   │   ├── ImportPage.js   # 导入导出页面
│   │   ├── AuditPage.js    # 审计页面
│   │   └── index.js        # 页面导出
│   ├── state/              # 状态管理
│   │   └── index.js        # 简单响应式状态
│   ├── styles/             # 样式文件
│   │   ├── main.css        # 主样式
│   │   └── login.css       # 登录页样式
│   ├── utils/              # 工具函数
│   │   └── index.js        # CSV 转义、日期格式化等
│   └── main.js             # 入口文件
├── static/                 # 构建产物（服务端托管）
│   ├── index.html
│   ├── login.html
│   ├── main.js
│   └── main.css
├── index.html              # 开发入口
├── login.html              # 登录页
├── package.json            # 依赖配置
├── vite.config.js          # Vite 配置
└── .gitignore
```

## 2. 页面结构

当前主页面模块：

1. Provider 管理
   - 列表、编辑、删除、激活、测试
2. Provider 批量导入导出
   - JSON 导入/预检
   - CC-Switch SQL 导入/预检
   - 导出 Provider JSON
3. 审计与维护
   - 登录审计
   - 操作审计（含 action/target/from/to 过滤）
   - 导出 CSV
   - 审计清理/设置

## 3. API 清单（供 UI 调用）

### Provider
- `GET /api/providers?search=&target=`
- `POST /api/providers`
- `DELETE /api/providers/{id}`
- `POST /api/providers/test`
- `POST /api/providers/test-batch?search=&target=`
- `POST /api/providers/import`
- `POST /api/providers/import-cc`
- `GET /api/providers/export?search=&target=`

### 激活/备份
- `POST /api/activate`
- `GET /api/backups`
- `POST /api/rollback`

### 审计
- `GET /api/login-audits?limit=&offset=`
- `GET /api/login-audits/export?limit=`
- `GET /api/op-audits?limit=&offset=&action=&target=&from=&to=`
- `GET /api/op-audits/export?limit=&action=&target=&from=&to=`
- `POST /api/audits/cleanup?keepDays=`
- `GET /api/audits/settings`
- `POST /api/audits/settings`

### 认证
- 页面登录：`/login`
- API：`token` query 或 `X-Admin-Token`，并兼容 cookie

## 4. UI 优化建议（优先级）

### P0（立即）
- 统一视觉层级（标题、卡片、按钮）
- 表格可读性优化（固定表头/行 hover/长文本折叠）
- 状态反馈标准化（loading/success/error toast）

### P1（短期）
- 暗色模式
- 操作确认弹窗统一化
- 响应式布局（移动端只保留关键操作）

### P2（中期）
- 组件化前端（Vue/React 任一）
- 前后端静态资源分离
- 更完整的权限模型（多用户 RBAC）

## 5. 开发指南（新增）

### 5.1 环境准备

```bash
cd lx-switch/web
npm install
```

### 5.2 开发模式

```bash
# 启动开发服务器（代理到后端 :18777）
npm run dev
# 访问 http://localhost:3000
```

### 5.3 构建发布

```bash
# 构建产物到 static/ 目录
npm run build
# 重启后端服务即可
```

### 5.4 API Client 使用

```javascript
import { api, get, post } from './apiClient/index.js';

// 使用封装好的 API
const providers = await api.getProviders('search', 'target');
await api.createProvider({ name: 'demo', target: 'openclaw' });

// 或直接使用通用方法
const data = await get('/api/meta');
await post('/api/activate', { providerId: 1 });
```

### 5.5 状态管理

```javascript
import { providerState, metaState, createState } from './state/index.js';

// 读取值
console.log(providerState.search.value);

// 设置值（会触发订阅回调）
providerState.search.value = 'new search';

// 订阅变化
providerState.list.subscribe((newList) => {
  console.log('Provider list updated:', newList);
});
```

### 5.6 组件使用

```javascript
import { Card, Table, Button, Row, Actions, Input, Select, confirm, showToast } from './components/index.js';

// 创建卡片
const card = Card({ title: '标题' });
container.appendChild(card);

// 创建表格
const { table, render } = Table({
  columns: [
    { key: 'id', label: 'ID' },
    { key: 'name', label: 'Name' },
  ],
  data: [{ id: 1, name: 'test' }],
  renderActions: (td, item) => {
    td.appendChild(Button({ text: '删除', onClick: () => del(item.id) }));
  },
});

// 确认对话框
if (await confirm('确定删除?')) {
  // 执行删除
}

// Toast 提示
showToast('操作成功', 'success');
```

---

## 6. 与后端协作约定

- 保持现有 API 兼容；新增字段遵循向后兼容
- 列表与导出必须共用同一筛选条件
- 大字段（如 SQL 导入内容）请求体限制与错误提示要明确
- 任何破坏性改动需附迁移说明

## 7. 迁移说明（旧版 → 新版）

### 7.1 文件变更

| 旧文件 | 新文件 | 说明 |
|--------|--------|------|
| `lx-switch/web/static/app.js` | `src/` 模块化拆分 | 拆分为 apiClient/components/pages/state/utils |
| `lx-switch/web/static/app.css` | `src/styles/main.css` | 主样式 |
| `lx-switch/web/static/login.css` | `src/styles/login.css` | 登录页样式 |
| `lx-switch/web/index.html` | `index.html` + `src/main.js` | HTML 模板 + JS 入口 |

### 7.2 功能对应

| 旧函数 | 新位置 |
|--------|--------|
| `api()` | `apiClient/index.js` → `request()` |
| `loadProviders()` | `pages/ProviderPage.js` |
| `saveProvider()` | `pages/ProviderPage.js` |
| `importProviders()` | `pages/ImportPage.js` |
| `loadAudits()` | `pages/AuditPage.js` |
| 全局变量 | `state/index.js` 响应式状态 |

### 7.3 兼容性

- 后端无需修改，API 路径和行为保持不变
- 新版构建产物覆盖 `static/` 目录后直接生效
- 登录页仍使用服务端渲染（`{{ERROR_BLOCK}}` 替换）

---

## 8. UI 改造范围建议（可先改 / 暂缓）

### 可先改（建议立即开工）

1. Provider 列表与编辑页面
   - 表格样式、筛选交互、按钮层级、空状态
2. 导入导出页面基础交互
   - 导入流程步骤化、dry-run 结果展示、错误提示规范化
3. 全局视觉系统
   - 颜色/间距/按钮规范、toast 反馈、弹窗统一
4. 页面布局
   - 模块分区、响应式断点、信息层级优化

### 暂缓到后端收尾后再定稿（可先做占位）

1. 登录审计 `from/to` 时间过滤（列表+导出）相关筛选 UI
2. CC-Switch 导入映射报告下载入口与结果页
3. 导入容错增强后对应的错误码/提示文案最终版

## 9. 联调检查清单（给前后端）

- 登录审计与操作审计：列表筛选条件与导出条件必须严格一致
- 日期筛选：统一支持 `YYYY-MM-DD` 与 RFC3339（前端显示与入参保持一致）
- 导入能力：dry-run 与正式导入返回结构保持字段兼容
- 错误处理：4xx 用于参数与业务校验，5xx 用于服务异常
- 兼容策略：新增字段允许前端忽略，禁止无通知删除既有字段

## 10. 版本协作建议

- 建议以「M3 收尾完成」作为一次联调冻结点
- UI 分支可先按模块拆 PR（Provider / 导入导出 / 审计）
- 若接口有变更，后端需在 `docs/API.md` 同步更新并在 PR 描述注明影响面
