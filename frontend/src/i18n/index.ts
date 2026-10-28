import { useUiStore, type Lang } from '@/store/ui-store'

// en 缺键回退 zh；zh 缺键回退键名本身。
const zh: Record<string, string> = {
  'common.cancel': '取消',
  'common.delete': '删除',
  'common.deleting': '删除中…',
  'common.retry': '重试',
  'common.refresh': '刷新',
  'common.loadFailed': '加载失败',
  'common.loading': '加载中…',

  'time.justNow': '刚刚',
  'time.minAgo': '{n} 分钟前',
  'time.hourAgo': '{n} 小时前',
  'time.dayAgo': '{n} 天前',

  'sessionlist.title': '会话',
  'sessionlist.refresh': '刷新会话',
  'sessionlist.newPlaceholder': '新会话标题…',
  'sessionlist.noSessions': '暂无会话',
  'sessionlist.noRuns': '暂无运行',
  'sessionlist.memoriesTitle': '会话记忆',
  'sessionlist.deleteTitle': '删除会话',
  'sessionlist.deleteConfirmTitle': '删除会话？',
  'sessionlist.deleteConfirmBody':
    '该会话及名下 {count} 个 Run（含事件与审批记录）将被一并删除，不可恢复。',
  'sessionlist.deleteActiveWarn': '存在运行中的任务，请先取消后再删除。',

  'memories.title': '会话记忆',
  'memories.description': 'Agent 在本会话中沉淀的记忆（最新在前）。',
  'memories.filterPlaceholder': '过滤记忆…',
  'memories.empty': '（无记忆）',

  'runs.title': '运行历史',
  'runs.description': '跨会话的全部顶层运行（最新在前，不含子 agent 运行）。',
  'runs.empty': '（无运行记录）',
  'runs.sessionLabel': '会话',

  'status.queued': '排队中',
  'status.running': '运行中',
  'status.waiting_approval': '待审批',
  'status.paused': '已暂停',
  'status.succeeded': '成功',
  'status.failed': '失败',
  'status.cancelled': '已取消',
  'status.timed_out': '超时',
  'status.budgetUsage': '用量 工具 {tool}/模型 {model}',
  'status.budgetUpdated': '用量更新',

  // 仅供测试的 zh-only 键
  'i18n.test.zhonly': '仅中文',
}

const en: Record<string, string> = {
  'common.cancel': 'Cancel',
  'common.delete': 'Delete',
  'common.deleting': 'Deleting…',
  'common.retry': 'Retry',
  'common.refresh': 'Refresh',
  'common.loadFailed': 'Failed to load',
  'common.loading': 'Loading…',

  'time.justNow': 'just now',
  'time.minAgo': '{n} min ago',
  'time.hourAgo': '{n} h ago',
  'time.dayAgo': '{n} d ago',

  'sessionlist.title': 'Sessions',
  'sessionlist.refresh': 'Refresh sessions',
  'sessionlist.newPlaceholder': 'New session title…',
  'sessionlist.noSessions': 'No sessions',
  'sessionlist.noRuns': 'No runs',
  'sessionlist.memoriesTitle': 'Session memories',
  'sessionlist.deleteTitle': 'Delete session',
  'sessionlist.deleteConfirmTitle': 'Delete session?',
  'sessionlist.deleteConfirmBody':
    'This session and its {count} runs (including events and approvals) will be deleted permanently.',
  'sessionlist.deleteActiveWarn': 'Active runs exist — cancel them before deleting.',

  'memories.title': 'Session Memories',
  'memories.description': 'Memories the agent accumulated in this session (newest first).',
  'memories.filterPlaceholder': 'Filter memories…',
  'memories.empty': '(no memories)',

  'runs.title': 'Run History',
  'runs.description': 'All top-level runs across sessions (newest first, sub-agent runs excluded).',
  'runs.empty': '(no runs)',
  'runs.sessionLabel': 'Session',

  'status.queued': 'Queued',
  'status.running': 'Running',
  'status.waiting_approval': 'Awaiting approval',
  'status.paused': 'Paused',
  'status.succeeded': 'Succeeded',
  'status.failed': 'Failed',
  'status.cancelled': 'Cancelled',
  'status.timed_out': 'Timed out',
  'status.budgetUsage': 'Usage tool {tool}/model {model}',
  'status.budgetUpdated': 'Usage updated',

  // i18n.test.zhonly deliberately absent from en — exercises fallback.
}

const dict: Record<Lang, Record<string, string>> = { zh, en }

export function t(key: string, params?: Record<string, string | number>): string {
  const lang = useUiStore.getState().lang
  let s = dict[lang][key] ?? dict.zh[key] ?? key
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      s = s.replaceAll(`{${name}}`, String(value))
    }
  }
  return s
}

/** 仅测试用：绕过 store 直接改当前语言（store 之外的回退验证）。 */
export function setI18nLangForTest(lang: Lang) {
  useUiStore.setState({ lang })
}
