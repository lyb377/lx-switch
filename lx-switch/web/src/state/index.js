/**
 * 状态管理 - 简单的响应式状态
 */

/**
 * 创建响应式状态
 * @param {any} initialValue - 初始值
 * @returns {{ value: any, subscribe: Function, set: Function }}
 */
export function createState(initialValue) {
  let value = initialValue;
  const subscribers = new Set();

  return {
    get value() {
      return value;
    },
    set value(newValue) {
      value = newValue;
      subscribers.forEach(fn => fn(value));
    },
    subscribe(fn) {
      subscribers.add(fn);
      return () => subscribers.delete(fn);
    },
    set(newValue) {
      this.value = newValue;
    },
  };
}

// ==================== 全局状态 ====================

// Provider 相关状态
export const providerState = {
  list: createState([]),
  search: createState(''),
  targetFilter: createState(''),
  editId: createState(null),
  lastImportResult: createState(null),
  lastBatchTestResult: createState(null),
};

// Meta 状态
export const metaState = {
  activeProvider: createState(''),
  firstRun: createState(false),
  tokenWeak: createState(false),
  auditRetentionDays: createState(30),
  auditCleanupEnabled: createState(true),
};

// 审计相关状态
export const auditState = {
  loginAudits: createState([]),
  loginTotal: createState(0),
  loginOffset: createState(0),
  loginFromFilter: createState(''),
  loginToFilter: createState(''),

  opAudits: createState([]),
  opTotal: createState(0),
  opOffset: createState(0),
  opActionFilter: createState(''),
  opTargetFilter: createState(''),
  opFromFilter: createState(''),
  opToFilter: createState(''),
};

// 备份状态
export const backupState = {
  list: createState([]),
};

// UI 状态
export const uiState = {
  loading: createState(false),
  activeTab: createState('providers'),
};

export default {
  createState,
  providerState,
  metaState,
  auditState,
  backupState,
  uiState,
};
