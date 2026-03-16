/**
 * Card 组件
 */

export function Card({ title, className = '' }) {
  const card = document.createElement('div');
  card.className = `card ${className}`.trim();

  if (title) {
    const h3 = document.createElement('h3');
    h3.textContent = title;
    card.appendChild(h3);
  }

  return card;
}

/**
 * 创建信息提示框
 */
export function Alert({ message, type = 'info', id = '' }) {
  const alert = document.createElement('div');
  alert.className = type; // ok | warn | muted
  if (id) alert.id = id;
  alert.textContent = message;
  return alert;
}

export default { Card, Alert };
