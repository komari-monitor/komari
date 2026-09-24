import type { AuditLogEntry } from '@/utils/rpc'

export interface ParsedVisitorAuditMessage {
  event: string
  path: string
  route: string
  target: string
  userAgent: string
  detail: Record<string, unknown>
}

export interface VisitorEventMeta {
  label: string
  icon: string
}

const VISITOR_MESSAGE_PREFIX = 'visitor event: '
const EDGE_VERSION_REGEX = /Edg\/([\d.]+)/
const FIREFOX_VERSION_REGEX = /Firefox\/([\d.]+)/
const CHROME_VERSION_REGEX = /Chrome\/([\d.]+)/
const SAFARI_VERSION_REGEX = /Version\/([\d.]+)/
const SAFARI_MARKER_REGEX = /Safari\//
const ANDROID_REGEX = /Android/i
const IOS_REGEX = /iPhone|iPad|iPod/i
const WINDOWS_REGEX = /Windows/i
const MACOS_REGEX = /Macintosh|Mac OS X/i
const LINUX_REGEX = /Linux/i
const EVENT_META: Record<string, VisitorEventMeta> = {
  page_view: { label: '页面访问', icon: 'tabler:eye' },
  node_open: { label: '打开节点', icon: 'tabler:server' },
  search: { label: '节点搜索', icon: 'tabler:search' },
  search_clear: { label: '清空搜索', icon: 'tabler:search-off' },
  group_change: { label: '切换分组', icon: 'tabler:category' },
  filter_change: { label: '快捷筛选', icon: 'tabler:filter' },
  view_mode_change: { label: '切换视图', icon: 'tabler:layout-grid' },
  admin_entry_click: { label: '进入后台', icon: 'tabler:settings' },
  home_tool_open: { label: '打开工具', icon: 'tabler:tool' },
  audit_refresh: { label: '刷新审计', icon: 'tabler:refresh' },
  audit_page_change: { label: '审计翻页', icon: 'tabler:chevrons-right' },
  audit_export_json: { label: '导出审计 JSON', icon: 'tabler:braces' },
  audit_export_csv: { label: '导出审计 CSV', icon: 'tabler:file-spreadsheet' },
  export_json: { label: '导出 JSON', icon: 'tabler:braces' },
  export_csv: { label: '导出 CSV', icon: 'tabler:file-spreadsheet' },
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function readString(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  return typeof value === 'string' ? value : ''
}

export function parseVisitorAuditMessage(log: AuditLogEntry): ParsedVisitorAuditMessage | null {
  if (log.msg_type !== 'visitor' || !log.message.startsWith(VISITOR_MESSAGE_PREFIX))
    return null

  try {
    const parsed = JSON.parse(log.message.slice(VISITOR_MESSAGE_PREFIX.length)) as unknown
    if (!isRecord(parsed))
      return null

    return {
      event: readString(parsed, 'event') || 'visitor',
      path: readString(parsed, 'path'),
      route: readString(parsed, 'route'),
      target: readString(parsed, 'target'),
      userAgent: readString(parsed, 'user_agent'),
      detail: isRecord(parsed.detail) ? parsed.detail : {},
    }
  }
  catch {
    return null
  }
}

export function getVisitorEventMeta(event: string): VisitorEventMeta {
  return EVENT_META[event] ?? { label: event || '访客事件', icon: 'tabler:activity' }
}

export function summarizeUserAgent(userAgent: string): string {
  if (!userAgent)
    return '未知客户端'

  const edgeVersion = EDGE_VERSION_REGEX.exec(userAgent)?.[1]
  const firefoxVersion = FIREFOX_VERSION_REGEX.exec(userAgent)?.[1]
  const chromeVersion = CHROME_VERSION_REGEX.exec(userAgent)?.[1]
  const safariVersion = SAFARI_MARKER_REGEX.test(userAgent) ? SAFARI_VERSION_REGEX.exec(userAgent)?.[1] : undefined
  const browser = edgeVersion
    ? `Edge ${edgeVersion}`
    : firefoxVersion
      ? `Firefox ${firefoxVersion}`
      : chromeVersion
        ? `Chrome ${chromeVersion}`
        : safariVersion ? `Safari ${safariVersion}` : '其他浏览器'
  const system = ANDROID_REGEX.test(userAgent)
    ? 'Android'
    : IOS_REGEX.test(userAgent)
      ? 'iOS'
      : WINDOWS_REGEX.test(userAgent)
        ? 'Windows'
        : MACOS_REGEX.test(userAgent)
          ? 'macOS'
          : LINUX_REGEX.test(userAgent) ? 'Linux' : '未知系统'
  return `${browser} · ${system}`
}

export function formatVisitorDetail(detail: Record<string, unknown>): string {
  const entries = Object.entries(detail)
    .filter(([, value]) => value !== undefined && value !== null && value !== '')
    .slice(0, 12)
  if (!entries.length)
    return '-'

  return entries.map(([key, value]) => {
    const encoded = typeof value === 'string' ? value : JSON.stringify(value)
    return `${key}: ${encoded}`
  }).join('\n')
}
