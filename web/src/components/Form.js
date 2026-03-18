/**
 * 表单组件
 */

/**
 * 创建输入框
 */
export function Input({ id, label, type = 'text', placeholder = '', value = '' }) {
  const container = document.createElement('div');
  container.className = 'form-group';

  if (label) {
    const labelEl = document.createElement('label');
    labelEl.textContent = label;
    if (id) labelEl.htmlFor = id;
    container.appendChild(labelEl);
  }

  const input = document.createElement('input');
  input.type = type;
  if (id) input.id = id;
  if (placeholder) input.placeholder = placeholder;
  if (value) input.value = value;
  container.appendChild(input);

  return { container, input };
}

/**
 * 创建下拉框
 */
export function Select({ id, label, options = [], value = '' }) {
  const container = document.createElement('div');
  container.className = 'form-group';

  if (label) {
    const labelEl = document.createElement('label');
    labelEl.textContent = label;
    if (id) labelEl.htmlFor = id;
    container.appendChild(labelEl);
  }

  const select = document.createElement('select');
  if (id) select.id = id;

  options.forEach(opt => {
    const option = document.createElement('option');
    if (typeof opt === 'string') {
      option.value = opt;
      option.textContent = opt;
    } else {
      option.value = opt.value;
      option.textContent = opt.label;
    }
    if (option.value === value) option.selected = true;
    select.appendChild(option);
  });

  container.appendChild(select);
  return { container, select };
}

/**
 * 创建文本域
 */
export function Textarea({ id, label, placeholder = '', value = '', rows = 4 }) {
  const container = document.createElement('div');
  container.className = 'form-group';

  if (label) {
    const labelEl = document.createElement('label');
    labelEl.textContent = label;
    if (id) labelEl.htmlFor = id;
    container.appendChild(labelEl);
  }

  const textarea = document.createElement('textarea');
  if (id) textarea.id = id;
  if (placeholder) textarea.placeholder = placeholder;
  if (value) textarea.value = value;
  textarea.rows = rows;
  container.appendChild(textarea);

  return { container, textarea };
}

/**
 * 创建复选框
 */
export function Checkbox({ id, label, checked = false }) {
  const container = document.createElement('div');
  container.className = 'form-group';

  const labelEl = document.createElement('label');
  labelEl.style.cssText = 'display: flex; align-items: center; gap: 8px;';

  const checkbox = document.createElement('input');
  checkbox.type = 'checkbox';
  if (id) checkbox.id = id;
  checkbox.checked = checked;
  checkbox.style.width = 'auto';
  labelEl.appendChild(checkbox);

  const text = document.createTextNode(label);
  labelEl.appendChild(text);

  container.appendChild(labelEl);
  return { container, checkbox };
}

/**
 * 创建行容器（两列布局）
 */
export function Row(...children) {
  const row = document.createElement('div');
  row.className = 'row';
  children.forEach(child => {
    if (child) row.appendChild(child);
  });
  return row;
}

/**
 * 创建操作按钮容器
 */
export function Actions(...buttons) {
  const div = document.createElement('div');
  div.className = 'actions';
  buttons.forEach(btn => {
    if (btn) div.appendChild(btn);
  });
  return div;
}

export default { Input, Select, Textarea, Checkbox, Row, Actions };
