/**
 * 工具函数集合
 */

/**
 * CSV 转义
 */
export function csvEscape(value) {
  const s = String(value ?? '');
  return '"' + s.replace(/"/g, '""') + '"';
}

/**
 * HTML 转义
 */
export function htmlEscape(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * 格式化日期时间
 */
export function formatDateTime(date) {
  if (!date) return '';
  const d = new Date(date);
  return d.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/**
 * 格式化文件大小
 */
export function formatFileSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

/**
 * 防抖函数
 */
export function debounce(fn, delay = 300) {
  let timer = null;
  return function (...args) {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => fn.apply(this, args), delay);
  };
}

/**
 * 节流函数
 */
export function throttle(fn, delay = 300) {
  let last = 0;
  return function (...args) {
    const now = Date.now();
    if (now - last >= delay) {
      last = now;
      fn.apply(this, args);
    }
  };
}

/**
 * 下载文本文件
 */
export function downloadTextFile(filename, content, type = 'text/plain;charset=utf-8') {
  const blob = new Blob([content], { type });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/**
 * 下载 JSON
 */
export function downloadJson(filename, data) {
  downloadTextFile(filename, JSON.stringify(data, null, 2), 'application/json;charset=utf-8');
}

/**
 * 下载 CSV
 */
export function downloadCsv(filename, rows) {
  downloadTextFile(filename, rows.join('\n'), 'text/csv;charset=utf-8');
}

/**
 * 确认对话框（Promise 版本）
 */
export function confirm(message) {
  return Promise.resolve(window.confirm(message));
}

/**
 * 提示输入（Promise 版本）
 */
export function prompt(message, defaultValue = '') {
  return Promise.resolve(window.prompt(message, defaultValue));
}

/**
 * 生成唯一 ID
 */
export function uid() {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

export default {
  csvEscape,
  htmlEscape,
  formatDateTime,
  formatFileSize,
  debounce,
  throttle,
  downloadTextFile,
  downloadJson,
  downloadCsv,
  confirm,
  prompt,
  uid,
};
