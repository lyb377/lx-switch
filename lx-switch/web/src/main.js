/**
 * LX Switch Web - 主入口
 */

import './styles/main.css';
import { api } from './apiClient/index.js';
import { showToast } from './apiClient/toast.js';
import { metaState } from './state/index.js';
import { Alert } from './components/index.js';
import { renderProviderPage, renderImportPage, renderAuditPage, renderDashboardPage } from './pages/index.js';

// 目标选项
const TARGET_OPTIONS = ['openclaw', 'claude', 'codex', 'gemini'];

/**
 * 渲染页面头部
 */
function renderHeader(container) {
  // 标题
  const h2 = document.createElement('h2');
  h2.textContent = 'LX Switch v0.2';
  container.appendChild(h2);

  // 描述
  const desc = document.createElement('p');
  desc.className = 'muted';
  desc.textContent = 'Server-native switch panel for Claude/Codex/OpenClaw/Gemini';
  container.appendChild(desc);

  // 状态提示
  container.appendChild(Alert({ id: 'firstRun', type: 'ok' }));
  container.appendChild(Alert({ id: 'weakToken', type: 'warn' }));
  container.appendChild(Alert({ id: 'activeProvider', type: 'ok' }));
  container.appendChild(Alert({ id: 'auditRetention', type: 'muted' }));

  // 登录状态
  const loginCard = document.createElement('div');
  loginCard.className = 'card';
  loginCard.innerHTML = `
    <div style="display:flex;justify-content:space-between;align-items:center;gap:10px;">
      <div class="muted">已登录，可直接管理 Provider</div>
      <button style="width:auto" onclick="location.href='/logout'">退出登录</button>
    </div>
  `;
  container.appendChild(loginCard);
}

/**
 * 渲染标签页导航
 */
function renderTabs(container) {
  const tabs = document.createElement('div');
  tabs.className = 'tabs';

  const tabBtns = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'providers', label: 'Provider 管理' },
    { id: 'import', label: '导入导出' },
    { id: 'audit', label: '审计日志' },
  ];

  tabBtns.forEach(tab => {
    const btn = document.createElement('button');
    btn.className = `tab-btn ${tab.id === 'dashboard' ? 'active' : ''}`;
    btn.textContent = tab.label;
    btn.dataset.tab = tab.id;
    btn.onclick = () => switchTab(tab.id);
    tabs.appendChild(btn);
  });

  container.appendChild(tabs);

  // 内容容器
  const content = document.createElement('div');
  content.id = 'tab-content';
  container.appendChild(content);

  return content;
}

/**
 * 切换标签页
 */
function switchTab(tabId) {
  // 更新按钮状态
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.tab === tabId);
  });

  // 渲染内容
  const content = document.getElementById('tab-content');
  content.innerHTML = '';

  switch (tabId) {
    case 'dashboard':
      renderDashboardPage(content);
      break;
    case 'providers':
      renderProviderPage(content);
      break;
    case 'import':
      renderImportPage(content);
      break;
    case 'audit':
      renderAuditPage(content);
      break;
  }
}

/**
 * 加载元数据
 */
async function loadMeta() {
  const m = await api.getMeta();

  // 首次使用提示
  if (m.firstRun) {
    document.getElementById('firstRun').textContent = '首次使用引导：1) 先新增一个 Provider；2) 点击"激活"写入目标配置；3) 如需回退可在 Backups 里回滚。';
    document.getElementById('firstRun').classList.remove('hide');
  }

  // Token 弱警告
  if (m.tokenWeak) {
    document.getElementById('weakToken').textContent = '检测到默认 Token（change-me-please），强烈建议尽快在 systemd 环境变量里修改 LX_SWITCH_TOKEN。';
    document.getElementById('weakToken').classList.remove('hide');
  }

  // 当前激活的 Provider
  if (m.activeProvider) {
    document.getElementById('activeProvider').textContent = '当前生效 Provider ID: ' + m.activeProvider;
    document.getElementById('activeProvider').classList.remove('hide');
  } else {
    document.getElementById('activeProvider').textContent = '当前尚未激活 Provider';
    document.getElementById('activeProvider').classList.remove('hide');
  }

  // 审计保留设置
  const ard = m.auditRetentionDays || 0;
  const ace = m.auditCleanupEnabled !== false;
  const arEl = document.getElementById('auditRetention');
  if (arEl && ard > 0) {
    arEl.textContent = `审计默认保留天数：${ard} 天；自动清理：${ace ? '开启' : '关闭'}`;
    arEl.classList.remove('hide');
  }

  // 保存到状态
  metaState.activeProvider.value = m.activeProvider || '';
  metaState.firstRun.value = m.firstRun;
  metaState.tokenWeak.value = m.tokenWeak;
  metaState.auditRetentionDays.value = ard;
  metaState.auditCleanupEnabled.value = ace;
}

/**
 * 初始化应用
 */
async function init() {
  const container = document.getElementById('app');

  // 渲染头部
  renderHeader(container);

  // 渲染标签页
  const tabContent = renderTabs(container);

  // 加载元数据
  await loadMeta();

  // 默认显示 Dashboard 页面
  renderDashboardPage(tabContent);

  // 监听刷新事件
  window.addEventListener('providers:refresh', async () => {
    // 刷新 provider 列表（由页面自己处理）
  });
}

// 启动
document.addEventListener('DOMContentLoaded', init);
