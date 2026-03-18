/**
 * Toast 提示组件
 */

let toastContainer = null;

function ensureContainer() {
  if (!toastContainer) {
    toastContainer = document.createElement('div');
    toastContainer.id = 'toast-container';
    toastContainer.style.cssText = `
      position: fixed;
      top: 20px;
      right: 20px;
      z-index: 10000;
      display: flex;
      flex-direction: column;
      gap: 10px;
    `;
    document.body.appendChild(toastContainer);
  }
  return toastContainer;
}

/**
 * 显示 Toast 提示
 * @param {string} message - 提示消息
 * @param {string} type - 类型：success | error | warning | info
 * @param {number} duration - 持续时间（毫秒）
 */
export function showToast(message, type = 'info', duration = 3000) {
  const container = ensureContainer();

  const colors = {
    success: { bg: '#ecfdf5', border: '#6ee7b7', color: '#065f46' },
    error: { bg: '#fef2f2', border: '#fca5a5', color: '#991b1b' },
    warning: { bg: '#fffbeb', border: '#fcd34d', color: '#92400e' },
    info: { bg: '#eff6ff', border: '#93c5fd', color: '#1e40af' },
  };

  const style = colors[type] || colors.info;

  const toast = document.createElement('div');
  toast.style.cssText = `
    background: ${style.bg};
    border: 1px solid ${style.border};
    color: ${style.color};
    padding: 12px 20px;
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0,0,0,0.1);
    font-size: 14px;
    max-width: 360px;
    word-wrap: break-word;
    animation: toast-in 0.3s ease;
  `;
  toast.textContent = message;

  // 添加动画样式
  if (!document.getElementById('toast-style')) {
    const style = document.createElement('style');
    style.id = 'toast-style';
    style.textContent = `
      @keyframes toast-in {
        from { transform: translateX(100%); opacity: 0; }
        to { transform: translateX(0); opacity: 1; }
      }
      @keyframes toast-out {
        from { transform: translateX(0); opacity: 1; }
        to { transform: translateX(100%); opacity: 0; }
      }
    `;
    document.head.appendChild(style);
  }

  container.appendChild(toast);

  // 自动移除
  setTimeout(() => {
    toast.style.animation = 'toast-out 0.3s ease forwards';
    setTimeout(() => toast.remove(), 300);
  }, duration);
}

export default { showToast };
