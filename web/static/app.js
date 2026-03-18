let editId = null;
let providerMap = {};
let providerSearch = '';
let providerTargetFilter = '';
let lastImportResult = null;
let lastBatchTestResult = null;
let auditOffset = 0;
const auditLimit = 20;
let auditTotal = 0;
let opOffset = 0;
const opLimit = 20;
let opTotal = 0;
let opActionFilter = '';
let opTargetFilter = '';
let opFromFilter = '';
let opToFilter = '';
let auditFromFilter = '';
let auditToFilter = '';

function H(){ return {'Content-Type':'application/json'}; }
async function api(url,opt={}){ const r=await fetch(url,{...opt,credentials:'same-origin',headers:{...(opt.headers||{}),...H()}}); if(!r.ok) throw new Error(await r.text()); return r.json(); }
function setBox(id,msg){ const el=document.getElementById(id); if(!el) return; if(!msg){el.classList.add('hide');el.textContent='';return;} el.textContent=msg; el.classList.remove('hide'); }
function v(id){return document.getElementById(id).value}
function setV(id,val){ document.getElementById(id).value = val || ''; }
function resetForm(){ ['name','target','baseUrl','apiKey','model','notes'].forEach(k=>setV(k,'')); document.getElementById('target').value='openclaw'; }

function startEdit(id){
  const p = providerMap[id];
  if(!p) return;
  editId = id;
  document.getElementById('editorTitle').textContent = '编辑 Provider #' + id;
  document.getElementById('cancelEditBtn').style.display = '';
  setV('name', p.name);
  setV('target', p.target);
  setV('baseUrl', p.baseUrl);
  setV('apiKey', p.apiKey);
  setV('model', p.model);
  setV('notes', p.notes);
  window.scrollTo({top:0,behavior:'smooth'});
}

function cancelEdit(){
  editId = null;
  document.getElementById('editorTitle').textContent = '新增 Provider';
  document.getElementById('cancelEditBtn').style.display = 'none';
  resetForm();
}

async function saveProvider(){
  const body={
    name:v('name'),target:v('target'),baseUrl:v('baseUrl'),apiKey:v('apiKey'),model:v('model'),notes:v('notes')
  };
  if(editId){
    await api('/api/providers/'+editId,{method:'PUT',body:JSON.stringify(body)});
    alert('已更新');
  }else{
    await api('/api/providers',{method:'POST',body:JSON.stringify(body)});
    alert('已保存');
  }
  cancelEdit();
  await loadProviders();
}

async function loadMeta(){
  const m=await api('/api/meta');
  if(m.firstRun){
    setBox('firstRun','首次使用引导：1) 先新增一个 Provider；2) 点击“激活”写入目标配置；3) 如需回退可在 Backups 里回滚。');
  }else{
    setBox('firstRun','');
  }
  if(m.tokenWeak){
    setBox('weakToken','检测到默认 Token（change-me-please），强烈建议尽快在 systemd 环境变量里修改 LX_SWITCH_TOKEN。');
  }else{
    setBox('weakToken','');
  }
  if(m.activeProvider){
    setBox('activeProvider','当前生效 Provider ID: '+m.activeProvider);
  }else{
    setBox('activeProvider','当前尚未激活 Provider');
  }
  const ard = Number(m.auditRetentionDays||0);
  const ace = !!m.auditCleanupEnabled;
  const ar = document.getElementById('auditRetention');
  if(ar){ ar.textContent = ard>0 ? ('审计默认保留天数：'+ard+' 天；自动清理：'+(ace?'开启':'关闭')) : ''; }
  const keepEl = document.getElementById('auditKeepDays');
  if(keepEl && ard>0) keepEl.value = String(ard);
  const autoEl = document.getElementById('auditAutoEnabled');
  if(autoEl) autoEl.checked = ace;
}

async function loadProviders(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  const list=await api('/api/providers?'+q);
  providerMap = {};
  const tb=document.getElementById('rows'); tb.innerHTML='';
  list.forEach(p=>{
    providerMap[p.id] = p;
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+p.id+'</td><td>'+p.name+'</td><td>'+p.target+'</td><td>'+(p.model||'')+'</td><td class="actions">'
      + '<button onclick="activate('+p.id+')">激活</button>'
      + '<button class="ghost" onclick="testProvider('+p.id+')">测试</button>'
      + '<button class="ghost" onclick="startEdit('+p.id+')">编辑</button>'
      + '<button onclick="delP('+p.id+')">删除</button>'
      + '</td>';
    tb.appendChild(tr);
  });
}

async function activate(id){
  const r=await api('/api/activate',{method:'POST',body:JSON.stringify({providerId:id})});
  alert('已激活，备份: '+r.backup);
  await loadBackups();
  await loadMeta();
  await loadOpAudits();
}

async function delP(id){
  if(!confirm('确定删除?')) return;
  await api('/api/providers/'+id,{method:'DELETE'});
  if(editId === id) cancelEdit();
  await loadProviders();
}

function applyProviderFilter(){
  providerSearch = (document.getElementById('providerSearch').value || '').trim();
  providerTargetFilter = (document.getElementById('providerTargetFilter').value || '').trim();
  loadProviders();
}

function clearProviderFilter(){
  providerSearch = '';
  providerTargetFilter = '';
  document.getElementById('providerSearch').value = '';
  document.getElementById('providerTargetFilter').value = '';
  loadProviders();
}

async function importProviders(){
  const raw = (document.getElementById('importJson').value || '').trim();
  if(!raw){ alert('请先粘贴 JSON'); return; }
  let obj;
  try{ obj = JSON.parse(raw); }catch(e){ alert('JSON 格式错误: '+e.message); return; }
  obj.mode = (document.getElementById('importMode').value || 'skip');
  obj.previewLimit = Number(document.getElementById('previewLimit').value || 30);
  obj.dryRun = false;
  const r = await api('/api/providers/import',{method:'POST',body:JSON.stringify(obj)});
  lastImportResult = r;
  alert('导入完成: 新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||''));
  await loadProviders();
  await loadOpAudits();
}

async function previewImportProviders(){
  const raw = (document.getElementById('importJson').value || '').trim();
  if(!raw){ alert('请先粘贴 JSON'); return; }
  let obj;
  try{ obj = JSON.parse(raw); }catch(e){ alert('JSON 格式错误: '+e.message); return; }
  obj.mode = (document.getElementById('importMode').value || 'skip');
  obj.previewLimit = Number(document.getElementById('previewLimit').value || 30);
  obj.dryRun = true;
  const r = await api('/api/providers/import',{method:'POST',body:JSON.stringify(obj)});
  lastImportResult = r;
  const details = (r.details||[]).slice(0,10).map(x=>('['+x.action+'] '+x.target+'/'+x.name+(x.existingId?(' -> #'+x.existingId):''))).join('\n');
  alert('预检完成（不落库）\n新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||'') + (details?('\n\n样例:\n'+details):''));
  await loadOpAudits();
}

async function previewImportCCSwitch(){
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if(!sql){ alert('请先粘贴 CC-Switch SQLite 导出 SQL'); return; }
  const mode = (document.getElementById('importMode').value || 'skip');
  const previewLimit = Number(document.getElementById('previewLimit').value || 30);
  const r = await api('/api/providers/import-cc',{method:'POST',body:JSON.stringify({sql,mode,dryRun:true,previewLimit})});
  lastImportResult = r;
  const details = (r.details||[]).slice(0,10).map(x=>('['+x.action+'] '+x.target+'/'+x.name+(x.existingId?(' -> #'+x.existingId):''))).join('\n');
  alert('CC 预检完成（不落库）\n解析 '+(r.parsed||0)+' 条，新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||'') + (details?('\n\n样例:\n'+details):''));
  await loadOpAudits();
}

async function importCCSwitch(){
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if(!sql){ alert('请先粘贴 CC-Switch SQLite 导出 SQL'); return; }
  const mode = (document.getElementById('importMode').value || 'skip');
  const previewLimit = Number(document.getElementById('previewLimit').value || 30);
  const r = await api('/api/providers/import-cc',{method:'POST',body:JSON.stringify({sql,mode,dryRun:false,previewLimit})});
  lastImportResult = r;
  alert('CC 导入完成：解析 '+(r.parsed||0)+'，新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||''));
  await loadProviders();
  await loadOpAudits();
}

async function downloadCCSwitchMappingReport(){
  const sql = (document.getElementById('ccSwitchSql').value || '').trim();
  if(!sql){ alert('请先粘贴 CC-Switch SQLite 导出 SQL'); return; }
  const r = await fetch('/api/providers/import-cc/report',{
    method:'POST',
    credentials:'same-origin',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({sql})
  });
  if(!r.ok){ alert(await r.text()); return; }
  const blob = await r.blob();
  const cd = r.headers.get('Content-Disposition') || '';
  const m = cd.match(/filename=([^;]+)/i);
  const filename = (m && m[1] ? m[1].replace(/"/g,'') : ('cc-switch-mapping-report-'+Date.now()+'.csv'));
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function exportProviders(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  window.open('/api/providers/export?'+q,'_blank');
}

function downloadTextFile(name, text, type='text/plain;charset=utf-8'){
  const blob = new Blob([text], {type});
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function exportLastImportJson(){
  if(!lastImportResult){ alert('暂无导入结果，请先执行预检或导入'); return; }
  downloadTextFile('import-result-'+Date.now()+'.json', JSON.stringify(lastImportResult, null, 2), 'application/json;charset=utf-8');
}

function csvEscJs(s){
  s = String(s ?? '');
  return '"'+s.replace(/"/g,'""')+'"';
}

function exportLastImportCsv(){
  if(!lastImportResult){ alert('暂无导入结果，请先执行预检或导入'); return; }
  const rows = [];
  rows.push('index,action,target,name,existing_id');
  (lastImportResult.details||[]).forEach(d=>{
    rows.push([d.index, csvEscJs(d.action), csvEscJs(d.target), csvEscJs(d.name), d.existingId||''].join(','));
  });
  rows.push('');
  rows.push('summary_key,summary_value');
  rows.push('mode,'+csvEscJs(lastImportResult.mode||''));
  rows.push('dryRun,'+csvEscJs(lastImportResult.dryRun===true));
  rows.push('imported,'+(lastImportResult.imported||0));
  rows.push('overwritten,'+(lastImportResult.overwritten||0));
  rows.push('skipped,'+(lastImportResult.skipped||0));
  rows.push('detailCount,'+(lastImportResult.detailCount||0));
  downloadTextFile('import-result-'+Date.now()+'.csv', rows.join('\n'), 'text/csv;charset=utf-8');
}

async function testProvider(id){
  const r = await api('/api/providers/test',{method:'POST',body:JSON.stringify({providerId:id})});
  if(r.ok){
    alert('连通性测试通过，HTTP '+(r.statusCode||0));
  }else{
    alert('连通性测试失败，HTTP '+(r.statusCode||0)+'\n'+(r.detail||''));
  }
  await loadOpAudits();
}

async function testProvidersBatch(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  const r = await api('/api/providers/test-batch?'+q,{method:'POST'});
  lastBatchTestResult = r;
  let msg = '批量测试完成：总计 '+(r.total||0)+'，通过 '+(r.okCount||0)+'，失败 '+(r.failCount||0);
  const fail = (r.items||[]).filter(x=>!x.ok).slice(0,5);
  if(fail.length){
    msg += '\n失败样例：\n' + fail.map(x=>('#'+x.providerId+' '+x.name+' ['+x.target+'] code='+x.statusCode)).join('\n');
  }
  alert(msg);
  await loadOpAudits();
}

function exportLastBatchTestJson(){
  if(!lastBatchTestResult){ alert('暂无批量测试结果，请先执行批量测试'); return; }
  downloadTextFile('provider-batch-test-'+Date.now()+'.json', JSON.stringify(lastBatchTestResult, null, 2), 'application/json;charset=utf-8');
}

function exportLastBatchTestCsv(){
  if(!lastBatchTestResult){ alert('暂无批量测试结果，请先执行批量测试'); return; }
  const rows = [];
  rows.push('provider_id,name,target,ok,status_code,detail');
  (lastBatchTestResult.items||[]).forEach(it=>{
    rows.push([it.providerId, csvEscJs(it.name), csvEscJs(it.target), it.ok ? 1 : 0, it.statusCode||0, csvEscJs(it.detail||'')].join(','));
  });
  rows.push('');
  rows.push('summary_key,summary_value');
  rows.push('total,'+(lastBatchTestResult.total||0));
  rows.push('okCount,'+(lastBatchTestResult.okCount||0));
  rows.push('failCount,'+(lastBatchTestResult.failCount||0));
  downloadTextFile('provider-batch-test-'+Date.now()+'.csv', rows.join('\n'), 'text/csv;charset=utf-8');
}

async function loadBackups(){
  const list=await api('/api/backups');
  const tb=document.getElementById('backs'); tb.innerHTML='';
  list.forEach(b=>{
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+b.name+'</td><td>'+b.size+'</td><td><button onclick="rollback(\''+b.name+'\')">回滚</button></td>';
    tb.appendChild(tr);
  });
}

async function rollback(name){
  if(!confirm('回滚到 '+name+' ?')) return;
  await api('/api/rollback',{method:'POST',body:JSON.stringify({name})});
  alert('回滚完成');
}

async function loadAudits(){
  const q='limit='+auditLimit+'&offset='+auditOffset+'&from='+encodeURIComponent(auditFromFilter)+'&to='+encodeURIComponent(auditToFilter);
  const res=await api('/api/login-audits?'+q);
  const list=res.items||[];
  auditTotal = res.total||0;
  const tb=document.getElementById('audits'); tb.innerHTML='';
  const pager=document.getElementById('auditPager');
  const from = auditTotal===0 ? 0 : (auditOffset+1);
  const to = Math.min(auditOffset+auditLimit, auditTotal);
  const f = [];
  if(auditFromFilter) f.push('from='+auditFromFilter);
  if(auditToFilter) f.push('to='+auditToFilter);
  pager.textContent='审计日志：共 '+auditTotal+' 条，当前 '+from+' - '+to + (f.length?('，过滤：'+f.join(', ')): '');
  list.forEach(a=>{
    const tr=document.createElement('tr');
    const ua=(a.userAgent||'').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    tr.innerHTML='<td>'+(a.createdAt||'')+'</td><td>'+(a.ip||'')+'</td><td>'+(a.success?'成功':'失败')+'</td><td>'+(a.reason||'')+'</td><td>'+ua+'</td>';
    tb.appendChild(tr);
  });
}

function prevAuditPage(){
  auditOffset = Math.max(0, auditOffset - auditLimit);
  loadAudits();
}

function nextAuditPage(){
  if(auditOffset + auditLimit >= auditTotal) return;
  auditOffset += auditLimit;
  loadAudits();
}

function applyAuditFilter(){
  auditFromFilter = (document.getElementById('auditFromFilter').value || '').trim();
  auditToFilter = (document.getElementById('auditToFilter').value || '').trim();
  auditOffset = 0;
  loadAudits();
}

function clearAuditFilter(){
  auditFromFilter = '';
  auditToFilter = '';
  document.getElementById('auditFromFilter').value = '';
  document.getElementById('auditToFilter').value = '';
  auditOffset = 0;
  loadAudits();
}

function exportAudits(){
  const q='limit=2000&from='+encodeURIComponent(auditFromFilter)+'&to='+encodeURIComponent(auditToFilter);
  window.open('/api/login-audits/export?'+q,'_blank');
}

async function loadOpAudits(){
  const q='limit='+opLimit+'&offset='+opOffset+'&action='+encodeURIComponent(opActionFilter)+'&target='+encodeURIComponent(opTargetFilter)+'&from='+encodeURIComponent(opFromFilter)+'&to='+encodeURIComponent(opToFilter);
  const res=await api('/api/op-audits?'+q);
  const list=res.items||[];
  opTotal = res.total||0;
  const tb=document.getElementById('ops'); tb.innerHTML='';
  const pager=document.getElementById('opPager');
  const from = opTotal===0 ? 0 : (opOffset+1);
  const to = Math.min(opOffset+opLimit, opTotal);
  const f = [];
  if(opActionFilter) f.push('action='+opActionFilter);
  if(opTargetFilter) f.push('target='+opTargetFilter);
  if(opFromFilter) f.push('from='+opFromFilter);
  if(opToFilter) f.push('to='+opToFilter);
  pager.textContent='操作日志：共 '+opTotal+' 条，当前 '+from+' - '+to + (f.length?('，过滤：'+f.join(', ')):'');
  list.forEach(a=>{
    const tr=document.createElement('tr');
    const ua=(a.userAgent||'').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    tr.innerHTML='<td>'+(a.createdAt||'')+'</td><td>'+(a.action||'')+'</td><td>'+(a.target||'')+'</td><td>'+(a.detail||'')+'</td><td>'+(a.ip||'')+'</td><td>'+ua+'</td>';
    tb.appendChild(tr);
  });
}

function prevOpPage(){
  opOffset = Math.max(0, opOffset - opLimit);
  loadOpAudits();
}

function nextOpPage(){
  if(opOffset + opLimit >= opTotal) return;
  opOffset += opLimit;
  loadOpAudits();
}

function applyOpFilter(){
  opActionFilter = (document.getElementById('opActionFilter').value || '').trim();
  opTargetFilter = (document.getElementById('opTargetFilter').value || '').trim();
  opFromFilter = (document.getElementById('opFromFilter').value || '').trim();
  opToFilter = (document.getElementById('opToFilter').value || '').trim();
  opOffset = 0;
  loadOpAudits();
}

function clearOpFilter(){
  opActionFilter = '';
  opTargetFilter = '';
  opFromFilter = '';
  opToFilter = '';
  document.getElementById('opActionFilter').value = '';
  document.getElementById('opTargetFilter').value = '';
  document.getElementById('opFromFilter').value = '';
  document.getElementById('opToFilter').value = '';
  opOffset = 0;
  loadOpAudits();
}

function exportOpAudits(){
  const q='limit=2000&action='+encodeURIComponent(opActionFilter)+'&target='+encodeURIComponent(opTargetFilter)+'&from='+encodeURIComponent(opFromFilter)+'&to='+encodeURIComponent(opToFilter);
  window.open('/api/op-audits/export?'+q,'_blank');
}

async function saveAuditSettings(){
  const keep = Number((document.getElementById('auditKeepDays').value || '30').trim());
  const enabled = !!document.getElementById('auditAutoEnabled').checked;
  if(!Number.isFinite(keep) || keep < 1){ alert('保留天数需 >= 1'); return; }
  const r = await api('/api/audits/settings',{method:'POST',body:JSON.stringify({auditRetentionDays:Math.floor(keep),auditCleanupEnabled:enabled})});
  alert('审计设置已保存：保留 '+(r.auditRetentionDays||0)+' 天，自动清理 '+(r.auditCleanupEnabled?'开启':'关闭'));
  await loadMeta();
  await loadOpAudits();
}

async function cleanupAudits(){
  const raw = prompt('保留最近多少天审计记录？（默认 30）','30');
  if(raw===null) return;
  const keep = Number(raw||30);
  if(!Number.isFinite(keep) || keep<1){ alert('请输入 >=1 的天数'); return; }
  if(!confirm('将删除早于 '+keep+' 天的登录/操作审计，确定继续？')) return;
  const r = await api('/api/audits/cleanup?keepDays='+encodeURIComponent(String(Math.floor(keep))),{method:'POST'});
  alert('清理完成：login '+(r.loginDeleted||0)+' 条，op '+(r.opDeleted||0)+' 条，总计 '+(r.totalDeleted||0));
  auditOffset = 0;
  opOffset = 0;
  await loadAudits();
  await loadOpAudits();
}

async function loadAll(){
  cancelEdit();
  await loadMeta();
  await loadProviders();
  await loadBackups();
  await loadAudits();
  await loadOpAudits();
}

window.addEventListener('DOMContentLoaded', loadAll);
