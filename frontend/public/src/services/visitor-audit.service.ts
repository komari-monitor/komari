import type { VisitorAuditEventParams } from '@/utils/rpc'
import { requestManager } from '@/services/request.service'
import { getSharedRpc } from '@/utils/rpc'

export type VisitorAuditSubmitStatus = 'success' | 'disabled' | 'rate_limited' | 'failed'

const SESSION_STORAGE_KEY = 'komari-theme:visitor-audit-session'
const VISITOR_AUDIT_TIMEOUT_MS = 4_000
const DETAIL_JSON_LIMIT = 1_800
const SENSITIVE_KEY_PATTERN = /authorization|password|passwd|secret|token|clipboard|command|query_value|cookie_value|cookie_header/i
const COMPACT_DETAIL_KEYS = [
  'fingerprint_id',
  'webrtc',
  'platform',
  'browser_brands',
  'mobile',
  'languages',
  'timezone',
  'screen',
  'viewport',
  'pixel_ratio',
  'hardware_concurrency',
  'device_memory',
  'touch_points',
  'webdriver',
  'cookie_enabled',
  'do_not_track',
  'connection',
  'webgl',
  'theme_version',
  'color_vision_mode',
] as const
let requestSequence = 0
let memorySessionId = ''

function generateSessionId(): string {
  if (globalThis.crypto?.randomUUID)
    return globalThis.crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 14)}`
}

export function getVisitorAuditSessionId(): string {
  if (memorySessionId)
    return memorySessionId

  try {
    memorySessionId = window.sessionStorage.getItem(SESSION_STORAGE_KEY) || generateSessionId()
    window.sessionStorage.setItem(SESSION_STORAGE_KEY, memorySessionId)
  }
  catch {
    memorySessionId = generateSessionId()
  }
  return memorySessionId
}

function sanitizeDetailValue(value: unknown, depth = 0): unknown {
  if (depth > 3 || value === null || value === undefined)
    return undefined
  if (typeof value === 'boolean')
    return value
  if (typeof value === 'number')
    return Number.isFinite(value) ? value : undefined
  if (typeof value === 'string')
    return value.slice(0, 256)
  if (Array.isArray(value)) {
    return value
      .slice(0, 24)
      .map(item => sanitizeDetailValue(item, depth + 1))
      .filter(item => item !== undefined)
  }
  if (typeof value !== 'object')
    return undefined

  const result: Record<string, unknown> = {}
  for (const [key, item] of Object.entries(value).slice(0, 32)) {
    if (SENSITIVE_KEY_PATTERN.test(key))
      continue
    const sanitized = sanitizeDetailValue(item, depth + 1)
    if (sanitized !== undefined)
      result[key.slice(0, 64)] = sanitized
  }
  return result
}

function jsonByteLength(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength
}

function compactSecurityDetail(sanitized: Record<string, unknown>, originalSize: number): Record<string, unknown> {
  const compact: Record<string, unknown> = {
    session_id: getVisitorAuditSessionId(),
    truncated: true,
    original_size: originalSize,
  }
  for (const key of COMPACT_DETAIL_KEYS) {
    if (sanitized[key] !== undefined)
      compact[key] = sanitized[key]
  }

  for (const optionalKey of ['webgl', 'connection', 'browser_brands', 'languages', 'viewport', 'screen']) {
    if (jsonByteLength(compact) <= DETAIL_JSON_LIMIT)
      return compact
    delete compact[optionalKey]
  }

  if (jsonByteLength(compact) <= DETAIL_JSON_LIMIT)
    return compact

  const webRtc = compact.webrtc
  const webRtcFingerprint = webRtc && typeof webRtc === 'object' && !Array.isArray(webRtc)
    ? (webRtc as Record<string, unknown>).fingerprint_id
    : undefined
  return {
    session_id: getVisitorAuditSessionId(),
    fingerprint_id: compact.fingerprint_id,
    webrtc: webRtcFingerprint ? { fingerprint_id: webRtcFingerprint } : undefined,
    theme_version: compact.theme_version,
    color_vision_mode: compact.color_vision_mode,
    truncated: true,
    original_size: originalSize,
  }
}

function compactDetail(detail: Record<string, unknown> | undefined): Record<string, unknown> {
  const sanitized = sanitizeDetailValue({
    ...detail,
    session_id: getVisitorAuditSessionId(),
  }) as Record<string, unknown> | undefined
  if (!sanitized)
    return { session_id: getVisitorAuditSessionId() }

  const encodedSize = jsonByteLength(sanitized)
  if (encodedSize <= DETAIL_JSON_LIMIT)
    return sanitized

  return compactSecurityDetail(sanitized, encodedSize)
}

function normalizeEvent(event: VisitorAuditEventParams): VisitorAuditEventParams | null {
  const eventName = event.event.trim().slice(0, 64)
  if (!eventName)
    return null

  return {
    event: eventName,
    path: event.path?.trim().slice(0, 512),
    route: event.route?.trim().slice(0, 128),
    target: event.target?.trim().slice(0, 128),
    detail: compactDetail(event.detail),
  }
}

export async function submitVisitorAuditEvent(event: VisitorAuditEventParams, enabled: boolean): Promise<VisitorAuditSubmitStatus> {
  if (!enabled || typeof window === 'undefined')
    return 'disabled'

  const normalized = normalizeEvent(event)
  if (!normalized)
    return 'failed'

  const requestKey = `visitor-audit:${getVisitorAuditSessionId()}:${++requestSequence}`
  try {
    const response = await requestManager.run(
      requestKey,
      signal => getSharedRpc().recordPublicVisitorEvent(normalized, signal),
      {
        timeout: VISITOR_AUDIT_TIMEOUT_MS,
        retryAttempts: 0,
        shouldRetry: () => false,
      },
    )
    if (response.status === 'success' || response.status === 'disabled' || response.status === 'rate_limited')
      return response.status
    return 'failed'
  }
  catch {
    return 'failed'
  }
}
