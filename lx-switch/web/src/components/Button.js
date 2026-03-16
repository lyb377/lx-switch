/**
 * Button 组件
 */

export function Button({ text, onClick, type = 'default', ghost = false, disabled = false }) {
  const btn = document.createElement('button');
  btn.textContent = text;
  btn.type = 'button';

  if (ghost) {
    btn.className = 'ghost';
  }

  if (disabled) {
    btn.disabled = true;
  }

  if (onClick) {
    btn.addEventListener('click', onClick);
  }

  return btn;
}

export default Button;
