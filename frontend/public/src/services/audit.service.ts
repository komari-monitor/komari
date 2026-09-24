import type { AuditLogsResponse } from '@/utils/rpc'
import { requestManager } from '@/services/request.service'
import { getSharedRpc } from '@/utils/rpc'

const DEFAULT_AUDIT_LOG_LIMIT = 50

function normalizePositiveInteger(value: string | number | null | undefined, fallback: number): number {
  const numericValue = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(numericValue) || numericValue <= 0)
    return fallback
  return Math.floor(numericValue)
}

export function getAuditLogsRequestKey(page = 1, limit = DEFAULT_AUDIT_LOG_LIMIT, msgType?: string): string {
  return `audit:logs:${page}:${limit}:${msgType?.trim() || 'all'}`
}

export function abortAuditLogs(page = 1, limit = DEFAULT_AUDIT_LOG_LIMIT, msgType?: string): void {
  requestManager.abort(getAuditLogsRequestKey(page, limit, msgType))
}

export async function loadAuditLogs(params: { page?: number | string, limit?: number | string, msgType?: string } = {}): Promise<AuditLogsResponse> {
  const page = normalizePositiveInteger(params.page, 1)
  const limit = normalizePositiveInteger(params.limit, DEFAULT_AUDIT_LOG_LIMIT)
  const msgType = params.msgType?.trim() || undefined

  return requestManager.run(
    getAuditLogsRequestKey(page, limit, msgType),
    async signal => getSharedRpc().getAuditLogs(String(limit), String(page), msgType, signal),
  )
}

export async function updateVisitorAuditEnabled(enabled: boolean): Promise<void> {
  await requestManager.run(
    'audit:visitor-setting',
    async signal => getSharedRpc().updateAdminSettings({ visitor_audit_enabled: enabled }, signal),
    { retryAttempts: 0 },
  )
}
