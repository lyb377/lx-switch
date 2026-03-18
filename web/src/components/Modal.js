/**
 * Modal 对话框组件
 */

let modalOverlay = null;

/**
 * 显示确认对话框
 * @param {Object} options
 * @param {string} options.title - 标题
 * @param {string} options.message - 消息
 * @param {string} options.confirmText - 确认按钮文字
 * @param {string} options.cancelText - 取消按钮文字
 * @returns {Promise<boolean>}
 */
export function showModal({ title = '', message, confirmText = '确定', cancelText = '取消' }) {
  return new Promise((resolve) => {
    // 创建遮罩
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.style.cssText = `
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      background: rgba(0,0,0,0.5);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 10001;
    `;

    // 创建对话框
    const modal = document.createElement('div');
    modal.className = 'modal';
    modal.style.cssText = `
      background: #fff;
      border-radius: 12px;
      padding: 24px;
      max-width: 420px;
      min-width: 300px;
      box-shadow: 0 8px 32px rgba(0,0,0,0.2);
    `;

    // 标题
    if (title) {
      const h4 = document.createElement('h4');
      h4.textContent = title;
      h4.style.cssText = 'margin: 0 0 16px 0; font-size: 18px;';
      modal.appendChild(h4);
    }

    // 消息
    const p = document.createElement('p');
    p.textContent = message;
    p.style.cssText = 'margin: 0 0 24px 0; color: #666; line-height: 1.6;';
    modal.appendChild(p);

    // 按钮容器
    const btns = document.createElement('div');
    btns.style.cssText = 'display: flex; gap: 12px; justify-content: flex-end;';

    // 取消按钮
    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'ghost';
    cancelBtn.textContent = cancelText;
    cancelBtn.style.cssText = 'width: auto; padding: 8px 20px;';
    cancelBtn.onclick = () => {
      document.body.removeChild(overlay);
      resolve(false);
    };
    btns.appendChild(cancelBtn);

    // 确认按钮
    const confirmBtn = document.createElement('button');
    confirmBtn.textContent = confirmText;
    confirmBtn.style.cssText = 'width: auto; padding: 8px 20px;';
    confirmBtn.onclick = () => {
      document.body.removeChild(overlay);
      resolve(true);
    };
    btns.appendChild(confirmBtn);

    modal.appendChild(btns);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);

    // 点击遮罩关闭
    overlay.onclick = (e) => {
      if (e.target === overlay) {
        document.body.removeChild(overlay);
        resolve(false);
      }
    };

    // ESC 关闭
    const escHandler = (e) => {
      if (e.key === 'Escape') {
        document.removeEventListener('keydown', escHandler);
        if (document.body.contains(overlay)) {
          document.body.removeChild(overlay);
          resolve(false);
        }
      }
    };
    document.addEventListener('keydown', escHandler);
  });
}

/**
 * 显示警告框
 */
export function alert(message, title = '') {
  return showModal({ title, message, cancelText: null, confirmText: '知道了' });
}

/**
 * 显示确认框
 */
export function confirm(message, title = '') {
  return showModal({ title, message, confirmText: '确定', cancelText: '取消' });
}

/**
 * 显示输入框
 */
export function prompt(message, defaultValue = '', title = '') {
  return new Promise((resolve) => {
    const overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.style.cssText = `
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      background: rgba(0,0,0,0.5);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 10001;
    `;

    const modal = document.createElement('div');
    modal.className = 'modal';
    modal.style.cssText = `
      background: #fff;
      border-radius: 12px;
      padding: 24px;
      max-width: 420px;
      min-width: 300px;
    `;

    if (title) {
      const h4 = document.createElement('h4');
      h4.textContent = title;
      h4.style.cssText = 'margin: 0 0 16px 0;';
      modal.appendChild(h4);
    }

    const p = document.createElement('p');
    p.textContent = message;
    p.style.cssText = 'margin: 0 0 12px 0; color: #666;';
    modal.appendChild(p);

    const input = document.createElement('input');
    input.value = defaultValue;
    input.style.cssText = 'width: 100%; padding: 10px; margin-bottom: 20px;';
    modal.appendChild(input);

    const btns = document.createElement('div');
    btns.style.cssText = 'display: flex; gap: 12px; justify-content: flex-end;';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'ghost';
    cancelBtn.textContent = '取消';
    cancelBtn.style.cssText = 'width: auto;';
    cancelBtn.onclick = () => {
      document.body.removeChild(overlay);
      resolve(null);
    };
    btns.appendChild(cancelBtn);

    const confirmBtn = document.createElement('button');
    confirmBtn.textContent = '确定';
    confirmBtn.style.cssText = 'width: auto;';
    confirmBtn.onclick = () => {
      document.body.removeChild(overlay);
      resolve(input.value);
    };
    btns.appendChild(confirmBtn);

    modal.appendChild(btns);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);

    input.focus();
    input.select();
  });
}

export default { showModal, alert, confirm, prompt };
