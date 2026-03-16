/**
 * API Client - 统一 API 调用封装
 * 特性：统一错误处理、401 重定向、Toast 提示
 */

import { showToast } from './toast.js';

const BASE_URL = '';

/**
 * 统一请求方法
 * @param {string} url - 请求路径
 * @param {RequestInit} options - fetch 选项
 * @returns {Promise<any>}
 */
export async function request(url, options = {}) {
  const defaultHeaders = {
    'Content-Type': 'application/json',
  };

  const config = {
    ...options,
    credentials: 'same-origin',
    headers: {
      ...defaultHeaders,
      ...options.headers,
    },
  };

  try {
    const response = await fetch(BASE_URL + url, config);

    // 401 未授权 - 重定向到登录页
    if (response.status === 401) {
      showToast('登录已过期，请重新登录', 'error');
      setTimeout(() => {
        window.location.href = '/login';
      }, 1000);
      throw new Error('Unauthorized');
    }

    // 429 请求过多
    if (response.status === 429) {
      const text = await response.text();
      showToast('请求过于频繁，请稍后再试', 'error');
      throw new Error(text || 'Too Many Requests');
    }

    // 其他错误
    if (!response.ok) {
      const text = await response.text();
      showToast(text || `请求失败: ${response.status}`, 'error');
      throw new Error(text || `HTTP ${response.status}`);
    }

    // 尝试解析 JSON
    const contentType = response.headers.get('content-type');
    if (contentType && contentType.includes('application/json')) {
      return response.json();
    }

    return response;
  } catch (error) {
    // 网络错误
    if (error.name === 'TypeError' && error.message === 'Failed to fetch') {
      showToast('网络连接失败，请检查网络', 'error');
    }
    throw error;
  }
}

/**
 * GET 请求
 */
export async function get(url, params = {}) {
  const query = new URLSearchParams(params);
  const fullUrl = query.toString() ? `${url}?${query}` : url;
  return request(fullUrl, { method: 'GET' });
}

/**
 * POST 请求
 */
export async function post(url, data = null) {
  return request(url, {
    method: 'POST',
    body: data ? JSON.stringify(data) : null,
  });
}

/**
 * PUT 请求
 */
export async function put(url, data = null) {
  return request(url, {
    method: 'PUT',
    body: data ? JSON.stringify(data) : null,
  });
}

/**
 * DELETE 请求
 */
export async function del(url) {
  return request(url, { method: 'DELETE' });
}

/**
 * 下载文件
 */
export async function download(url, params = {}) {
  const query = new URLSearchParams(params);
  const fullUrl = query.toString() ? `${url}?${query}` : url;
  window.open(fullUrl, '_blank');
}

/**
 * 下载 Blob
 */
export async function downloadBlob(url, data = null) {
  const response = await request(url, {
    method: 'POST',
    body: data ? JSON.stringify(data) : null,
  });

  const blob = await response.blob();
  const cd = response.headers.get('Content-Disposition') || '';
  const m = cd.match(/filename=([^;]+)/i);
  const filename = m && m[1] ? m[1].replace(/"/g, '') : `download-${Date.now()}`;

  const blobUrl = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = blobUrl;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(blobUrl);
}

// ==================== API 端点 ====================

export const api = {
  // Meta
  getMeta: () => get('/api/meta'),

  // Providers
  getProviders: (search = '', target = '') => get('/api/providers', { search, target }),
  createProvider: (data) => post('/api/providers', data),
  updateProvider: (id, data) => put(`/api/providers/${id}`, data),
  deleteProvider: (id) => del(`/api/providers/${id}`),
  testProvider: (data) => post('/api/providers/test', data),
  testProvidersBatch: (search = '', target = '') => post(`/api/providers/test-batch?search=${encodeURIComponent(search)}&target=${encodeURIComponent(target)}`),

  // Import/Export
  importProviders: (data) => post('/api/providers/import', data),
  importCCSwitch: (data) => post('/api/providers/import-cc', data),
  getCCSwitchReport: (data) => downloadBlob('/api/providers/import-cc/report', data),
  exportProviders: (search = '', target = '') => download('/api/providers/export', { search, target }),

  // Activate & Backup
  activate: (providerId) => post('/api/activate', { providerId }),
  getBackups: () => get('/api/backups'),
  rollback: (name) => post('/api/rollback', { name }),

  // Login Audits
  getLoginAudits: (params) => get('/api/login-audits', params),
  exportLoginAudits: (params) => download('/api/login-audits/export', params),

  // Operation Audits
  getOpAudits: (params) => get('/api/op-audits', params),
  exportOpAudits: (params) => download('/api/op-audits/export', params),

  // Audit Settings
  getAuditSettings: () => get('/api/audits/settings'),
  saveAuditSettings: (data) => post('/api/audits/settings', data),
  cleanupAudits: (keepDays) => post(`/api/audits/cleanup?keepDays=${encodeURIComponent(keepDays)}`),
};

export default api;
