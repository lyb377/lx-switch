/**
 * Table 组件
 */

/**
 * 创建表格
 * @param {Object} options
 * @param {Array<{key: string, label: string}>} options.columns - 列定义
 * @param {Array} options.data - 数据
 * @param {Function} options.renderActions - 渲染操作列
 */
export function Table({ columns, data, renderActions }) {
  const table = document.createElement('table');

  // 表头
  const thead = document.createElement('thead');
  const headerRow = document.createElement('tr');
  columns.forEach(col => {
    const th = document.createElement('th');
    th.textContent = col.label;
    headerRow.appendChild(th);
  });
  if (renderActions) {
    const th = document.createElement('th');
    th.textContent = '操作';
    headerRow.appendChild(th);
  }
  thead.appendChild(headerRow);
  table.appendChild(thead);

  // 表体
  const tbody = document.createElement('tbody');
  table.appendChild(tbody);

  // 渲染数据
  const render = (items) => {
    tbody.innerHTML = '';
    items.forEach(item => {
      const tr = document.createElement('tr');
      columns.forEach(col => {
        const td = document.createElement('td');
        td.textContent = item[col.key] ?? '';
        tr.appendChild(td);
      });
      if (renderActions) {
        const td = document.createElement('td');
        td.className = 'actions';
        renderActions(td, item);
        tr.appendChild(td);
      }
      tbody.appendChild(tr);
    });
  };

  if (data) {
    render(data);
  }

  return { table, tbody, render };
}

/**
 * 创建分页器
 */
export function Pager({ total, offset, limit, onPrev, onNext, containerId, filters = [] }) {
  const pager = document.createElement('div');
  pager.className = 'muted pager';

  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + limit, total);

  let text = `共 ${total} 条，当前 ${from} - ${to}`;
  if (filters.length) {
    text += `，过滤: ${filters.join(', ')}`;
  }

  pager.textContent = text;
  pager.id = containerId;

  return pager;
}

export default { Table, Pager };
