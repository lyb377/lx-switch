/**
 * Provider 管理页面
 */

import { api } from '../apiClient/index.js';
import { showToast } from '../apiClient/toast.js';
import { providerState, metaState, backupState } from '../state/index.js';
import { Card, Table, Button, Row, Actions, Input, Select, Textarea, confirm } from '../components/index.js';

// 目标选项
const TARGET_OPTIONS = ['openclaw', 'claude', 'codex', 'gemini'];

/**
 * 渲染 Provider 编辑表单
 */
function renderEditorForm(container) {
  const card = Card({ title: '新增 Provider' });
  card.id = 'providerEditorCard';

  const form = document.createElement('div');
  form.className = 'provider-form';

  // 行1: Name + Target
  const row1 = Row();
  const nameInput = Input({ id: 'name', label: 'Name' });
  const targetSelect = Select({ id: 'target', label: 'Target', options: TARGET_OPTIONS, value: 'openclaw' });
  row1.appendChild(nameInput.container);
  row1.appendChild(targetSelect.container);
  form.appendChild(row1);

  // 行2: BaseURL + Model
  const row2 = Row();
  const baseUrlInput = Input({ id: 'baseUrl', label: 'Base URL' });
  const modelInput = Input({ id: 'model', label: 'Model' });
  row2.appendChild(baseUrlInput.container);
  row2.appendChild(modelInput.container);
  form.appendChild(row2);

  // API Key
  const apiKeyInput = Input({ id: 'apiKey', label: 'API Key' });
  form.appendChild(apiKeyInput.container);

  // Notes
  const notesInput = Textarea({ id: 'notes', label: 'Notes', rows: 2 });
  form.appendChild(notesInput.container);

  // 操作按钮
  const actions = Actions(
    Button({ text: '保存', onClick: saveProvider }),
    Button({ text: '取消编辑', ghost: true, onClick: cancelEdit })
  );
  actions.querySelector('.ghost').id = 'cancelEditBtn';
  actions.querySelector('.ghost').style.display = 'none';
  form.appendChild(actions);

  card.appendChild(form);
  container.appendChild(card);
}

/**
 * 渲染 Provider 列表
 */
function renderProviderList(container) {
  const card = Card({ title: 'Providers' });

  // 筛选行
  const filterRow = Row();
  const searchInput = Input({ id: 'providerSearch', label: '搜索', placeholder: 'name/model/baseUrl/notes' });
  const targetFilterSelect = Select({ id: 'providerTargetFilter', label: 'Target 过滤', options: [{ value: '', label: '全部' }, ...TARGET_OPTIONS.map(t => ({ value: t, label: t }))] });
  filterRow.appendChild(searchInput.container);
  filterRow.appendChild(targetFilterSelect.container);
  card.appendChild(filterRow);

  // 操作按钮
  const filterActions = Actions(
    Button({ text: '应用过滤', onClick: applyProviderFilter }),
    Button({ text: '清空过滤', ghost: true, onClick: clearProviderFilter }),
    Button({ text: '批量测试当前筛选', ghost: true, onClick: testProvidersBatch }),
    Button({ text: '导出最近批测(CSV)', ghost: true, onClick: exportLastBatchTestCsv }),
    Button({ text: '导出最近批测(JSON)', ghost: true, onClick: exportLastBatchTestJson })
  );
  card.appendChild(filterActions);

  // 表格
  const { table, tbody, render } = Table({
    columns: [
      { key: 'id', label: 'ID' },
      { key: 'name', label: 'Name' },
      { key: 'target', label: 'Target' },
      { key: 'model', label: 'Model' },
    ],
    renderActions: (td, p) => {
      td.appendChild(Button({ text: '激活', onClick: () => activate(p.id) }));
      td.appendChild(Button({ text: '测试', ghost: true, onClick: () => testProvider(p.id) }));
      td.appendChild(Button({ text: '编辑', ghost: true, onClick: () => startEdit(p.id) }));
      td.appendChild(Button({ text: '删除', onClick: () => deleteProvider(p.id) }));
    },
  });
  card.appendChild(table);

  container.appendChild(card);

  // 保存渲染函数
  providerState._renderTable = render;
  providerState._tbody = tbody;
}

/**
 * 渲染备份列表
 */
function renderBackupList(container) {
  const card = Card({ title: 'Backups' });

  const actions = Actions(Button({ text: '刷新备份', onClick: loadBackups }));
  card.appendChild(actions);

  const { table, render } = Table({
    columns: [
      { key: 'name', label: 'Name' },
      { key: 'size', label: 'Size' },
    ],
    renderActions: (td, b) => {
      td.appendChild(Button({ text: '回滚', onClick: () => rollback(b.name) }));
    },
  });
  card.appendChild(table);

  container.appendChild(card);

  backupState._renderTable = render;
}

// ==================== 业务逻辑 ====================

async function loadProviders() {
  const list = await api.getProviders(providerState.search.value, providerState.targetFilter.value);
  providerState.list.value = list;
  if (providerState._renderTable) {
    providerState._renderTable(list);
  }
}

async function loadBackups() {
  const list = await api.getBackups();
  backupState.list.value = list;
  if (backupState._renderTable) {
    backupState._renderTable(list);
  }
}

async function saveProvider() {
  const body = {
    name: document.getElementById('name').value,
    target: document.getElementById('target').value,
    baseUrl: document.getElementById('baseUrl').value,
    apiKey: document.getElementById('apiKey').value,
    model: document.getElementById('model').value,
    notes: document.getElementById('notes').value,
  };

  if (providerState.editId.value) {
    await api.updateProvider(providerState.editId.value, body);
    showToast('已更新', 'success');
  } else {
    await api.createProvider(body);
    showToast('已保存', 'success');
  }

  cancelEdit();
  await loadProviders();
}

function startEdit(id) {
  const p = providerState.list.value.find(x => x.id === id);
  if (!p) return;

  providerState.editId.value = id;
  document.querySelector('#providerEditorCard h3').textContent = '编辑 Provider #' + id;
  document.getElementById('cancelEditBtn').style.display = '';

  document.getElementById('name').value = p.name || '';
  document.getElementById('target').value = p.target || 'openclaw';
  document.getElementById('baseUrl').value = p.baseUrl || '';
  document.getElementById('apiKey').value = p.apiKey || '';
  document.getElementById('model').value = p.model || '';
  document.getElementById('notes').value = p.notes || '';

  window.scrollTo({ top: 0, behavior: 'smooth' });
}

function cancelEdit() {
  providerState.editId.value = null;
  document.querySelector('#providerEditorCard h3').textContent = '新增 Provider';
  document.getElementById('cancelEditBtn').style.display = 'none';
  document.getElementById('name').value = '';
  document.getElementById('target').value = 'openclaw';
  document.getElementById('baseUrl').value = '';
  document.getElementById('apiKey').value = '';
  document.getElementById('model').value = '';
  document.getElementById('notes').value = '';
}

async function deleteProvider(id) {
  if (!(await confirm('确定删除?'))) return;
  await api.deleteProvider(id);
  if (providerState.editId.value === id) cancelEdit();
  await loadProviders();
  showToast('已删除', 'success');
}

async function activate(id) {
  const r = await api.activate(id);
  showToast(`已激活，备份: ${r.backup}`, 'success');
  await loadBackups();
  await loadMeta();
}

async function testProvider(id) {
  const r = await api.testProvider({ providerId: id });
  if (r.ok) {
    showToast(`连通性测试通过，HTTP ${r.statusCode || 0}`, 'success');
  } else {
    showToast(`连通性测试失败: HTTP ${r.statusCode || 0}\n${r.detail || ''}`, 'error');
  }
}

async function testProvidersBatch() {
  const r = await api.testProvidersBatch(providerState.search.value, providerState.targetFilter.value);
  providerState.lastBatchTestResult.value = r;
  showToast(`批量测试完成：总计 ${r.total}，通过 ${r.okCount}，失败 ${r.failCount}`, r.failCount > 0 ? 'warning' : 'success');
}

function applyProviderFilter() {
  providerState.search.value = (document.getElementById('providerSearch').value || '').trim();
  providerState.targetFilter.value = (document.getElementById('providerTargetFilter').value || '').trim();
  loadProviders();
}

function clearProviderFilter() {
  providerState.search.value = '';
  providerState.targetFilter.value = '';
  document.getElementById('providerSearch').value = '';
  document.getElementById('providerTargetFilter').value = '';
  loadProviders();
}

async function rollback(name) {
  if (!(await confirm(`回滚到 ${name} ?`))) return;
  await api.rollback(name);
  showToast('回滚完成', 'success');
}

function exportLastBatchTestJson() {
  if (!providerState.lastBatchTestResult.value) {
    showToast('暂无批量测试结果', 'warning');
    return;
  }
  const data = providerState.lastBatchTestResult.value;
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `provider-batch-test-${Date.now()}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function exportLastBatchTestCsv() {
  if (!providerState.lastBatchTestResult.value) {
    showToast('暂无批量测试结果', 'warning');
    return;
  }
  const data = providerState.lastBatchTestResult.value;
  const rows = ['provider_id,name,target,ok,status_code,detail'];
  data.items.forEach(it => {
    rows.push(`${it.providerId},"${it.name}","${it.target}",${it.ok ? 1 : 0},${it.statusCode || 0},"${(it.detail || '').replace(/"/g, '""')}"`);
  });
  const blob = new Blob([rows.join('\n')], { type: 'text/csv' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `provider-batch-test-${Date.now()}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

async function loadMeta() {
  const m = await api.getMeta();
  metaState.activeProvider.value = m.activeProvider || '';
  metaState.firstRun.value = m.firstRun;
  metaState.tokenWeak.value = m.tokenWeak;
  metaState.auditRetentionDays.value = m.auditRetentionDays || 30;
  metaState.auditCleanupEnabled.value = m.auditCleanupEnabled !== false;
}

// ==================== 导出 ====================

/**
 * 渲染 Provider 管理页面
 */
export function renderProviderPage(container) {
  renderEditorForm(container);
  renderProviderList(container);
  renderBackupList(container);

  // 初始加载
  loadMeta();
  loadProviders();
  loadBackups();
}

export default { renderProviderPage };
