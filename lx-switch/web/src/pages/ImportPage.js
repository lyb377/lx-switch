/**
 * 导入导出页面
 */

import { api } from '../apiClient/index.js';
import { showToast } from '../apiClient/toast.js';
import { providerState } from '../state/index.js';
import { Card, Button, Row, Actions, Select, Input, Textarea } from '../components/index.js';

/**
 * 渲染导入导出页面
 */
export function renderImportPage(container) {
  const card = Card({ title: '批量导入 Providers (JSON / CC-Switch SQL)' });

  // 说明
  const hint = document.createElement('p');
  hint.className = 'muted';
  hint.textContent = 'JSON格式：{"items":[{"name":"...","target":"openclaw","baseUrl":"https://...","apiKey":"...","model":"...","notes":"..."}]}';
  card.appendChild(hint);

  // 配置行
  const configRow = Row();
  const modeSelect = Select({
    id: 'importMode',
    label: '冲突策略',
    options: [
      { value: 'skip', label: 'skip（冲突跳过）' },
      { value: 'overwrite', label: 'overwrite（冲突覆盖）' },
    ],
    value: 'skip',
  });
  const previewLimitInput = Input({ id: 'previewLimit', label: '预检明细上限', type: 'number', value: '30' });
  previewLimitInput.input.min = '1';
  previewLimitInput.input.max = '200';
  configRow.appendChild(modeSelect.container);
  configRow.appendChild(previewLimitInput.container);
  card.appendChild(configRow);

  // JSON 导入区
  const jsonTextarea = Textarea({
    id: 'importJson',
    label: '导入 JSON',
    placeholder: '{"items":[{"name":"demo","target":"openclaw","baseUrl":"https://example.com/v1","model":"gpt-4"}]}',
    rows: 5,
  });
  card.appendChild(jsonTextarea.container);

  const jsonActions = Actions(
    Button({ text: 'JSON预检（dry-run）', onClick: previewImportProviders }),
    Button({ text: 'JSON导入', onClick: importProviders }),
    Button({ text: '导出当前筛选 JSON', ghost: true, onClick: exportProviders }),
    Button({ text: '导出最近导入结果(JSON)', ghost: true, onClick: exportLastImportJson }),
    Button({ text: '导出最近导入结果(CSV)', ghost: true, onClick: exportLastImportCsv })
  );
  card.appendChild(jsonActions);

  // CC-Switch 导入区
  const ccTextarea = Textarea({
    id: 'ccSwitchSql',
    label: '导入 CC-Switch SQLite 导出 SQL（粘贴文本）',
    placeholder: '-- CC Switch SQLite 导出...\nINSERT INTO "providers" ...',
    rows: 5,
  });
  card.appendChild(ccTextarea.container);

  const ccActions = Actions(
    Button({ text: 'CC预检（dry-run）', onClick: previewImportCCSwitch }),
    Button({ text: 'CC导入', onClick: importCCSwitch }),
    Button({ text: '下载CC映射报告(CSV)', ghost: true, onClick: downloadCCSwitchMappingReport })
  );
  card.appendChild(ccActions);

  container.appendChild(card);
}

// ==================== 业务逻辑 ====================

async function previewImportProviders() {
  const raw = (document.getElementById('importJson').value || '').trim();
  if (!raw) {
    showToast('请先粘贴 JSON', 'warning');
    return;
  }

  let obj;
  try {
    obj = JSON.parse(raw);
  } catch (e) {
    showToast(`JSON 格式错误: ${e.message}`, 'error');
    return;
  }

  obj.mode = document.getElementById('importMode').value || 'skip';
  obj.previewLimit = Number(document.getElementById('previewLimit').value) || 30;
  obj.dryRun = true;

  const r = await api.importProviders(obj);
  providerState.lastImportResult.value = r;

  const details = (r.details || []).slice(0, 10)
    .map(x => `[${x.action}] ${x.target}/${x.name}${x.existingId ? ' -> #' + x.existingId : ''}`)
    .join('\n');

  showToast(`预检完成：新增 ${r.imported}，覆盖 ${r.overwritten}，跳过 ${r.skipped}`, 'success');
}

async function importProviders() {
  const raw = (document.getElementById('importJson').value || '').trim();
  if (!raw) {
    showToast('请先粘贴 JSON', 'warning');
    return;
  }

  let obj;
  try {
    obj = JSON.parse(raw);
  } catch (e) {
    showToast(`JSON 格式错误: ${e.message}`, 'error');
    return;
  }

  obj.mode = document.getElementById('importMode').value || 'skip';
  obj.previewLimit = Number(document.getElementById('previewLimit').value) || 30;
  obj.dryRun = false;

  const r = await api.importProviders(obj);
  providerState.lastImportResult.value = r;
  showToast(`导入完成: 新增 ${r.imported}，覆盖 ${r.overwritten}，跳过 ${r.skipped}`, 'success');

  // 刷新列表
  window.dispatchEvent(new CustomEvent('providers:refresh'));
}

async function previewImportCCSwitch() {
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if (!sql) {
    showToast('请先粘贴 CC-Switch SQLite 导出 SQL', 'warning');
    return;
  }

  const mode = document.getElementById('importMode').value || 'skip';
  const previewLimit = Number(document.getElementById('previewLimit').value) || 30;

  const r = await api.importCCSwitch({ sql, mode, dryRun: true, previewLimit });
  providerState.lastImportResult.value = r;

  showToast(`CC 预检完成：解析 ${r.parsed}，新增 ${r.imported}，覆盖 ${r.overwritten}，跳过 ${r.skipped}`, 'success');
}

async function importCCSwitch() {
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if (!sql) {
    showToast('请先粘贴 CC-Switch SQLite 导出 SQL', 'warning');
    return;
  }

  const mode = document.getElementById('importMode').value || 'skip';
  const previewLimit = Number(document.getElementById('previewLimit').value) || 30;

  const r = await api.importCCSwitch({ sql, mode, dryRun: false, previewLimit });
  providerState.lastImportResult.value = r;
  showToast(`CC 导入完成：解析 ${r.parsed}，新增 ${r.imported}，覆盖 ${r.overwritten}，跳过 ${r.skipped}`, 'success');

  // 刷新列表
  window.dispatchEvent(new CustomEvent('providers:refresh'));
}

function exportProviders() {
  api.exportProviders(providerState.search.value, providerState.targetFilter.value);
}

async function downloadCCSwitchMappingReport() {
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if (!sql) {
    showToast('请先粘贴 CC-Switch SQLite 导出 SQL', 'warning');
    return;
  }
  await api.getCCSwitchReport({ sql });
}

function exportLastImportJson() {
  if (!providerState.lastImportResult.value) {
    showToast('暂无导入结果', 'warning');
    return;
  }
  const data = providerState.lastImportResult.value;
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `import-result-${Date.now()}.json`;
  a.click();
  URL.revokeObjectURL(url);
}

function exportLastImportCsv() {
  if (!providerState.lastImportResult.value) {
    showToast('暂无导入结果', 'warning');
    return;
  }
  const data = providerState.lastImportResult.value;
  const rows = ['index,action,target,name,existing_id'];
  (data.details || []).forEach(d => {
    rows.push(`${d.index},"${d.action}","${d.target}","${d.name}",${d.existingId || ''}`);
  });
  rows.push('', 'summary_key,summary_value');
  rows.push(`mode,"${data.mode || ''}"`);
  rows.push(`dryRun,${data.dryRun}`);
  rows.push(`imported,${data.imported || 0}`);
  rows.push(`overwritten,${data.overwritten || 0}`);
  rows.push(`skipped,${data.skipped || 0}`);

  const blob = new Blob([rows.join('\n')], { type: 'text/csv' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `import-result-${Date.now()}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

export default { renderImportPage };
