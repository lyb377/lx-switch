/**
 * Dashboard 页面 - 审计指标可视化
 */

import { api } from '../apiClient/index.js';
import { showToast } from '../apiClient/toast.js';
import { Card, Button, Actions } from '../components/index.js';

let currentWindow = '24h';

/**
 * 渲染 Dashboard 页面
 */
export function renderDashboardPage(container) {
  container.innerHTML = '';

  // 标题和时间窗口切换
  const header = document.createElement('div');
  header.style.cssText = 'display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px;';

  const title = document.createElement('h2');
  title.textContent = '审计指标 Dashboard';
  header.appendChild(title);

  const windowActions = Actions(
    Button({ text: '24小时', onClick: () => switchWindow('24h'), primary: currentWindow === '24h' }),
    Button({ text: '7天', onClick: () => switchWindow('7d'), primary: currentWindow === '7d' }),
    Button({ text: '30天', onClick: () => switchWindow('30d'), primary: currentWindow === '30d' })
  );
  header.appendChild(windowActions);

  container.appendChild(header);

  // 指标卡片容器
  const metricsContainer = document.createElement('div');
  metricsContainer.id = 'metricsContainer';
  metricsContainer.style.cssText = 'display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px;';
  container.appendChild(metricsContainer);

  // 加载指标
  loadMetrics();
}

/**
 * 切换时间窗口
 */
function switchWindow(window) {
  currentWindow = window;
  const container = document.querySelector('#metricsContainer').parentElement;
  renderDashboardPage(container);
}

/**
 * 加载指标数据
 */
async function loadMetrics() {
  try {
    const data = await api(`/api/metrics/summary?window=${currentWindow}`);
    renderMetrics(data);
  } catch (err) {
    showToast('加载指标失败: ' + err.message, 'error');
  }
}

/**
 * 渲染指标卡片
 */
function renderMetrics(data) {
  const container = document.querySelector('#metricsContainer');
  container.innerHTML = '';

  // 登录指标卡片
  const loginCard = createMetricCard('登录指标', [
    { label: '总登录次数', value: data.login.total },
    { label: '成功次数', value: data.login.success, color: '#52c41a' },
    { label: '失败次数', value: data.login.failed, color: '#ff4d4f' },
    { label: '成功率', value: data.login.successRate.toFixed(2) + '%', color: data.login.successRate >= 90 ? '#52c41a' : '#faad14' },
    { label: '独立 IP 数', value: data.login.uniqueIPs }
  ]);
  container.appendChild(loginCard);

  // 操作指标卡片
  const operations = data.operations || {};
  const opItems = [];

  if (operations.activate) {
    opItems.push({ label: '激活次数', value: operations.activate.total });
    opItems.push({ label: '激活失败率', value: operations.activate.failureRate.toFixed(2) + '%', color: operations.activate.failureRate > 10 ? '#ff4d4f' : '#52c41a' });
  }

  if (operations.rollback) {
    opItems.push({ label: '回滚次数', value: operations.rollback.total });
    opItems.push({ label: '回滚失败率', value: operations.rollback.failureRate.toFixed(2) + '%', color: operations.rollback.failureRate > 10 ? '#ff4d4f' : '#52c41a' });
  }

  if (operations.import || operations['providers.import']) {
    const importOp = operations.import || operations['providers.import'];
    opItems.push({ label: '导入次数', value: importOp.total });
    opItems.push({ label: '导入失败率', value: importOp.failureRate.toFixed(2) + '%', color: importOp.failureRate > 10 ? '#ff4d4f' : '#52c41a' });
  }

  if (opItems.length > 0) {
    const opCard = createMetricCard('操作指标', opItems);
    container.appendChild(opCard);
  }

  // Target 分布卡片
  const byTarget = data.byTarget || {};
  const targetItems = [];
  const targets = ['openclaw', 'claude', 'codex', 'gemini'];

  targets.forEach(target => {
    const count = byTarget[target] || 0;
    targetItems.push({ label: target, value: count });
  });

  const targetCard = createMetricCard('Target 激活分布', targetItems);
  container.appendChild(targetCard);

  // 所有操作统计卡片
  const allOpItems = [];
  Object.keys(operations).forEach(action => {
    const op = operations[action];
    allOpItems.push({
      label: `${action} (总/失败)`,
      value: `${op.total} / ${op.failed}`,
      color: op.failed > 0 ? '#faad14' : '#52c41a'
    });
  });

  if (allOpItems.length > 0) {
    const allOpCard = createMetricCard('所有操作统计', allOpItems);
    container.appendChild(allOpCard);
  }
}

/**
 * 创建指标卡片
 */
function createMetricCard(title, items) {
  const card = Card({ title });

  const list = document.createElement('div');
  list.style.cssText = 'display: flex; flex-direction: column; gap: 12px; padding: 10px 0;';

  items.forEach(item => {
    const row = document.createElement('div');
    row.style.cssText = 'display: flex; justify-content: space-between; align-items: center;';

    const label = document.createElement('span');
    label.textContent = item.label;
    label.style.color = '#666';

    const value = document.createElement('span');
    value.textContent = item.value;
    value.style.cssText = `font-size: 18px; font-weight: bold; color: ${item.color || '#1890ff'};`;

    row.appendChild(label);
    row.appendChild(value);
    list.appendChild(row);
  });

  card.appendChild(list);
  return card;
}

