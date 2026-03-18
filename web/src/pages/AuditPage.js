/**
 * 审计页面
 */

import { api } from '../apiClient/index.js';
import { showToast } from '../apiClient/toast.js';
import { auditState, metaState } from '../state/index.js';
import { Card, Table, Button, Row, Actions, Input, Checkbox, confirm, prompt } from '../components/index.js';

const LOGIN_LIMIT = 20;
const OP_LIMIT = 20;

/**
 * 渲染登录审计
 */
function renderLoginAudit(container) {
  const card = Card({ title: '登录审计' });

  // 筛选行
  const filterRow = Row();
  const fromInput = Input({ id: 'auditFromFilter', label: '开始日期（from）', type: 'date' });
  const toInput = Input({ id: 'auditToFilter', label: '结束日期（to）', type: 'date' });
  filterRow.appendChild(fromInput.container);
  filterRow.appendChild(toInput.container);
  card.appendChild(filterRow);

  // 操作按钮
  const actions = Actions(
    Button({ text: '应用过滤', onClick: applyAuditFilter }),
    Button({ text: '清空过滤', ghost: true, onClick: clearAuditFilter }),
    Button({ text: '上一页', onClick: prevAuditPage }),
    Button({ text: '下一页', onClick: nextAuditPage }),
    Button({ text: '刷新审计日志', ghost: true, onClick: loadAudits }),
    Button({ text: '导出 CSV', ghost: true, onClick: exportAudits })
  );
  card.appendChild(actions);

  // 分页信息
  const pager = document.createElement('div');
  pager.className = 'muted';
  pager.id = 'auditPager';
  card.appendChild(pager);

  // 表格
  const { table, render } = Table({
    columns: [
      { key: 'createdAt', label: '时间' },
      { key: 'ip', label: 'IP' },
      { key: 'success', label: '结果' },
      { key: 'reason', label: '原因' },
      { key: 'userAgent', label: 'UA' },
    ],
  });
  card.appendChild(table);

  container.appendChild(card);

  auditState._renderLoginTable = render;
}

/**
 * 渲染操作审计
 */
function renderOpAudit(container) {
  const card = Card({ title: '操作审计' });

  // 设置行
  const settingsRow = Row();
  const keepDaysInput = Input({ id: 'auditKeepDays', label: '默认保留天数', type: 'number', value: '30' });
  keepDaysInput.input.min = '1';
  keepDaysInput.input.max = '3650';
  const autoCheckbox = Checkbox({ id: 'auditAutoEnabled', label: '启用自动每日清理', checked: true });
  settingsRow.appendChild(keepDaysInput.container);
  settingsRow.appendChild(autoCheckbox.container);
  card.appendChild(settingsRow);

  // 操作按钮
  const mainActions = Actions(
    Button({ text: '保存审计设置', onClick: saveAuditSettings }),
    Button({ text: '上一页', onClick: prevOpPage }),
    Button({ text: '下一页', onClick: nextOpPage }),
    Button({ text: '刷新操作日志', ghost: true, onClick: loadOpAudits }),
    Button({ text: '导出 CSV', ghost: true, onClick: exportOpAudits }),
    Button({ text: '清理旧审计', ghost: true, onClick: cleanupAudits })
  );
  card.appendChild(mainActions);

  // 筛选行1
  const filterRow1 = Row();
  const actionFilterInput = Input({ id: 'opActionFilter', label: 'Action 过滤', placeholder: '例如 provider.activate' });
  const targetFilterInput = Input({ id: 'opTargetFilter', label: 'Target 过滤', placeholder: '例如 openclaw' });
  filterRow1.appendChild(actionFilterInput.container);
  filterRow1.appendChild(targetFilterInput.container);
  card.appendChild(filterRow1);

  // 筛选行2
  const filterRow2 = Row();
  const opFromInput = Input({ id: 'opFromFilter', label: '开始日期（from）', type: 'date' });
  const opToInput = Input({ id: 'opToFilter', label: '结束日期（to）', type: 'date' });
  filterRow2.appendChild(opFromInput.container);
  filterRow2.appendChild(opToInput.container);
  card.appendChild(filterRow2);

  // 筛选按钮
  const filterActions = Actions(
    Button({ text: '应用过滤', onClick: applyOpFilter }),
    Button({ text: '清空过滤', ghost: true, onClick: clearOpFilter })
  );
  card.appendChild(filterActions);

  // 分页信息
  const pager = document.createElement('div');
  pager.className = 'muted';
  pager.id = 'opPager';
  card.appendChild(pager);

  // 表格
  const { table, render } = Table({
    columns: [
      { key: 'createdAt', label: '时间' },
      { key: 'action', label: 'Action' },
      { key: 'target', label: 'Target' },
      { key: 'detail', label: 'Detail' },
      { key: 'ip', label: 'IP' },
      { key: 'userAgent', label: 'UA' },
    ],
  });
  card.appendChild(table);

  container.appendChild(card);

  auditState._renderOpTable = render;
}

// ==================== 业务逻辑 ====================

async function loadAudits() {
  const params = {
    limit: LOGIN_LIMIT,
    offset: auditState.loginOffset.value,
    from: auditState.loginFromFilter.value,
    to: auditState.loginToFilter.value,
  };

  const res = await api.getLoginAudits(params);
  auditState.loginAudits.value = res.items || [];
  auditState.loginTotal.value = res.total || 0;

  if (auditState._renderLoginTable) {
    auditState._renderLoginTable(
      (res.items || []).map(a => ({
        ...a,
        success: a.success ? '成功' : '失败',
        userAgent: (a.userAgent || '').substring(0, 50),
      }))
    );
  }

  // 更新分页
  const total = auditState.loginTotal.value;
  const offset = auditState.loginOffset.value;
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + LOGIN_LIMIT, total);
  const filters = [];
  if (auditState.loginFromFilter.value) filters.push(`from=${auditState.loginFromFilter.value}`);
  if (auditState.loginToFilter.value) filters.push(`to=${auditState.loginToFilter.value}`);
  document.getElementById('auditPager').textContent = `审计日志：共 ${total} 条，当前 ${from} - ${to}${filters.length ? '，过滤: ' + filters.join(', ') : ''}`;
}

async function loadOpAudits() {
  const params = {
    limit: OP_LIMIT,
    offset: auditState.opOffset.value,
    action: auditState.opActionFilter.value,
    target: auditState.opTargetFilter.value,
    from: auditState.opFromFilter.value,
    to: auditState.opToFilter.value,
  };

  const res = await api.getOpAudits(params);
  auditState.opAudits.value = res.items || [];
  auditState.opTotal.value = res.total || 0;

  if (auditState._renderOpTable) {
    auditState._renderOpTable(
      (res.items || []).map(a => ({
        ...a,
        userAgent: (a.userAgent || '').substring(0, 50),
      }))
    );
  }

  // 更新分页
  const total = auditState.opTotal.value;
  const offset = auditState.opOffset.value;
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + OP_LIMIT, total);
  const filters = [];
  if (auditState.opActionFilter.value) filters.push(`action=${auditState.opActionFilter.value}`);
  if (auditState.opTargetFilter.value) filters.push(`target=${auditState.opTargetFilter.value}`);
  if (auditState.opFromFilter.value) filters.push(`from=${auditState.opFromFilter.value}`);
  if (auditState.opToFilter.value) filters.push(`to=${auditState.opToFilter.value}`);
  document.getElementById('opPager').textContent = `操作日志：共 ${total} 条，当前 ${from} - ${to}${filters.length ? '，过滤: ' + filters.join(', ') : ''}`;
}

function prevAuditPage() {
  auditState.loginOffset.value = Math.max(0, auditState.loginOffset.value - LOGIN_LIMIT);
  loadAudits();
}

function nextAuditPage() {
  if (auditState.loginOffset.value + LOGIN_LIMIT >= auditState.loginTotal.value) return;
  auditState.loginOffset.value += LOGIN_LIMIT;
  loadAudits();
}

function prevOpPage() {
  auditState.opOffset.value = Math.max(0, auditState.opOffset.value - OP_LIMIT);
  loadOpAudits();
}

function nextOpPage() {
  if (auditState.opOffset.value + OP_LIMIT >= auditState.opTotal.value) return;
  auditState.opOffset.value += OP_LIMIT;
  loadOpAudits();
}

function applyAuditFilter() {
  auditState.loginFromFilter.value = (document.getElementById('auditFromFilter').value || '').trim();
  auditState.loginToFilter.value = (document.getElementById('auditToFilter').value || '').trim();
  auditState.loginOffset.value = 0;
  loadAudits();
}

function clearAuditFilter() {
  auditState.loginFromFilter.value = '';
  auditState.loginToFilter.value = '';
  document.getElementById('auditFromFilter').value = '';
  document.getElementById('auditToFilter').value = '';
  auditState.loginOffset.value = 0;
  loadAudits();
}

function applyOpFilter() {
  auditState.opActionFilter.value = (document.getElementById('opActionFilter').value || '').trim();
  auditState.opTargetFilter.value = (document.getElementById('opTargetFilter').value || '').trim();
  auditState.opFromFilter.value = (document.getElementById('opFromFilter').value || '').trim();
  auditState.opToFilter.value = (document.getElementById('opToFilter').value || '').trim();
  auditState.opOffset.value = 0;
  loadOpAudits();
}

function clearOpFilter() {
  auditState.opActionFilter.value = '';
  auditState.opTargetFilter.value = '';
  auditState.opFromFilter.value = '';
  auditState.opToFilter.value = '';
  document.getElementById('opActionFilter').value = '';
  document.getElementById('opTargetFilter').value = '';
  document.getElementById('opFromFilter').value = '';
  document.getElementById('opToFilter').value = '';
  auditState.opOffset.value = 0;
  loadOpAudits();
}

function exportAudits() {
  api.exportLoginAudits({
    limit: 2000,
    from: auditState.loginFromFilter.value,
    to: auditState.loginToFilter.value,
  });
}

function exportOpAudits() {
  api.exportOpAudits({
    limit: 2000,
    action: auditState.opActionFilter.value,
    target: auditState.opTargetFilter.value,
    from: auditState.opFromFilter.value,
    to: auditState.opToFilter.value,
  });
}

async function saveAuditSettings() {
  const keep = Number(document.getElementById('auditKeepDays').value) || 30;
  const enabled = document.getElementById('auditAutoEnabled').checked;

  if (keep < 1) {
    showToast('保留天数需 >= 1', 'error');
    return;
  }

  const r = await api.saveAuditSettings({ auditRetentionDays: Math.floor(keep), auditCleanupEnabled: enabled });
  showToast(`审计设置已保存：保留 ${r.auditRetentionDays} 天，自动清理 ${r.auditCleanupEnabled ? '开启' : '关闭'}`, 'success');
}

async function cleanupAudits() {
  const raw = await prompt('保留最近多少天审计记录？（默认 30）', '30');
  if (raw === null) return;

  const keep = Number(raw) || 30;
  if (keep < 1) {
    showToast('请输入 >= 1 的天数', 'error');
    return;
  }

  if (!(await confirm(`将删除早于 ${keep} 天的登录/操作审计，确定继续？`))) return;

  const r = await api.cleanupAudits(keep);
  showToast(`清理完成：login ${r.loginDeleted} 条，op ${r.opDeleted} 条，总计 ${r.totalDeleted}`, 'success');

  auditState.loginOffset.value = 0;
  auditState.opOffset.value = 0;
  await loadAudits();
  await loadOpAudits();
}

// ==================== 导出 ====================

/**
 * 渲染审计页面
 */
export function renderAuditPage(container) {
  renderLoginAudit(container);
  renderOpAudit(container);

  // 初始加载
  loadAudits();
  loadOpAudits();

  // 应用 meta 设置
  const keepEl = document.getElementById('auditKeepDays');
  const autoEl = document.getElementById('auditAutoEnabled');
  if (keepEl && metaState.auditRetentionDays.value > 0) {
    keepEl.value = String(metaState.auditRetentionDays.value);
  }
  if (autoEl) {
    autoEl.checked = metaState.auditCleanupEnabled.value;
  }
}

export default { renderAuditPage };
