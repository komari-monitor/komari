<script setup lang="ts">
import type { SnapshotCsvColumn } from '@/services/snapshot.service'
import type { AuditLogEntry } from '@/utils/rpc'
import type { ParsedVisitorAuditMessage, VisitorEventMeta } from '@/utils/visitorAudit'
import { Icon } from '@iconify/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardX } from '@/components/ui/card-x'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useVisitorAudit } from '@/composables/useVisitorAudit'
import { loadAuditLogs, updateVisitorAuditEnabled } from '@/services/audit.service'
import { buildSnapshotCsvAsync, downloadText } from '@/services/snapshot.service'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/helper'
import { formatVisitorDetail, getVisitorEventMeta, parseVisitorAuditMessage, summarizeUserAgent } from '@/utils/visitorAudit'

type AuditView = 'all' | 'visitor'

interface AuditLogRow {
  log: AuditLogEntry
  visitor: ParsedVisitorAuditMessage | null
  eventMeta: VisitorEventMeta | null
  userAgentSummary: string
  detailText: string
  sessionId: string
  fingerprintId: string
  webRtcFingerprintId: string
}

const appStore = useAppStore()
const { record: recordVisitorEvent } = useVisitorAudit()
const logs = ref<AuditLogEntry[]>([])
const total = ref(0)
const page = ref(1)
const limit = ref(50)
const logView = ref<AuditView>('all')
const loading = ref(false)
const exporting = ref<'json' | 'csv' | null>(null)
const updatingVisitorAudit = ref(false)
const error = ref<string | null>(null)
let requestId = 0

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / limit.value)))
const pageStart = computed(() => total.value === 0 ? 0 : (page.value - 1) * limit.value + 1)
const pageEnd = computed(() => Math.min(total.value, page.value * limit.value))
const visitorAuditStatus = computed(() => {
  if (!appStore.visitorAuditSupported)
    return { icon: 'tabler:clock-pause', tone: 'text-warning', text: '当前核心尚未发布访客审计接口；升级到包含 PR #602 的版本后自动启用此视图。' }
  if (!appStore.visitorAuditEnabled)
    return { icon: 'tabler:shield-off', tone: 'text-warning', text: '核心已支持访客审计，但 visitor_audit_enabled 当前关闭。已有记录仍可查看。' }
  return { icon: 'tabler:shield-check', tone: 'text-success', text: '访客审计已启用；IP 与 User-Agent 由服务端可信记录，前端只提交受限操作摘要。' }
})

function buildAuditLogRow(log: AuditLogEntry): AuditLogRow {
  const visitor = parseVisitorAuditMessage(log)
  const detail = visitor?.detail ?? {}
  const webRtc = detail.webrtc && typeof detail.webrtc === 'object' && !Array.isArray(detail.webrtc)
    ? detail.webrtc as Record<string, unknown>
    : {}
  return {
    log,
    visitor,
    eventMeta: visitor ? getVisitorEventMeta(visitor.event) : null,
    userAgentSummary: visitor ? summarizeUserAgent(visitor.userAgent) : '',
    detailText: visitor ? formatVisitorDetail(detail) : '',
    sessionId: typeof detail.session_id === 'string' ? detail.session_id : '',
    fingerprintId: typeof detail.fingerprint_id === 'string' ? detail.fingerprint_id : '',
    webRtcFingerprintId: typeof webRtc.fingerprint_id === 'string' ? webRtc.fingerprint_id : '',
  }
}

const rows = computed<AuditLogRow[]>(() => logs.value.map(buildAuditLogRow))

const auditCsvColumns: Array<SnapshotCsvColumn<AuditLogRow>> = [
  { label: '日志 ID', value: row => row.log.id },
  { label: '时间', value: row => row.log.time },
  { label: '来源 IP', value: row => row.log.ip || '-' },
  { label: '用户 UUID', value: row => row.log.uuid || '-' },
  { label: '会话 ID', value: row => row.sessionId || '-' },
  { label: '日志类型', value: row => row.log.msg_type || '-' },
  { label: '访客事件', value: row => row.visitor?.event || '-' },
  { label: '路径', value: row => row.visitor?.path || '-' },
  { label: '路由', value: row => row.visitor?.route || '-' },
  { label: '目标', value: row => row.visitor?.target || '-' },
  { label: 'User-Agent', value: row => row.visitor?.userAgent || '-' },
  { label: '客户端摘要', value: row => row.userAgentSummary || '-' },
  { label: '站点指纹', value: row => row.fingerprintId || '-' },
  { label: 'WebRTC 指纹', value: row => row.webRtcFingerprintId || '-' },
  { label: '访客详情', value: row => row.visitor ? JSON.stringify(row.visitor.detail) : '-' },
  { label: '原始日志', value: row => row.log.message || '-' },
]

function formatLogTime(time: string): string {
  return formatDateTime(time)
}

function normalizeLogLevel(type: string): string {
  return type?.trim() || '-'
}

function logLevelClass(type: string): string {
  const normalized = type.toLowerCase()
  if (normalized === 'visitor')
    return 'border-info/40 text-info'
  if (normalized.includes('error') || normalized.includes('fail') || normalized.includes('delete'))
    return 'border-destructive/40 text-destructive'
  if (normalized.includes('warn') || normalized.includes('update') || normalized.includes('edit'))
    return 'border-warning/40 text-warning'
  return 'border-success/40 text-success'
}

function shortId(value: string): string {
  if (!value)
    return '-'
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

function yieldToBrowser(): Promise<void> {
  return new Promise((resolve) => {
    if (typeof window !== 'undefined' && 'requestIdleCallback' in window) {
      window.requestIdleCallback(() => resolve(), { timeout: 80 })
      return
    }
    setTimeout(resolve, 0)
  })
}

async function verifyAuditExportPermission(view: AuditView): Promise<boolean> {
  const granted = await appStore.requireLoginPermission('auditLog', { force: true })
  if (!granted) {
    window.$message?.warning('登录状态已过期，请重新登录后导出审计日志。')
    return false
  }
  if (view === 'visitor' && !appStore.visitorAuditSupported) {
    window.$message?.warning('当前核心尚未支持访客审计筛选。')
    return false
  }
  return true
}

async function loadAllFilteredLogs(view: AuditView): Promise<AuditLogEntry[]> {
  const exportLimit = 200
  const collected: AuditLogEntry[] = []
  const seenIds = new Set<number>()
  let exportPage = 1

  while (true) {
    const result = await loadAuditLogs({
      page: exportPage,
      limit: exportLimit,
      msgType: view === 'visitor' ? 'visitor' : undefined,
    })
    const pageLogs = result.logs ?? []
    let added = 0
    for (const log of pageLogs) {
      if (seenIds.has(log.id))
        continue
      seenIds.add(log.id)
      collected.push(log)
      added++
    }

    const resultTotal = Number.isFinite(result.total) ? result.total : 0
    if (!pageLogs.length || !added || (resultTotal > 0 && collected.length >= resultTotal) || pageLogs.length < exportLimit)
      break

    exportPage++
    await yieldToBrowser()
  }

  return collected
}

function buildAuditJsonRecord(row: AuditLogRow): Record<string, unknown> {
  return {
    id: row.log.id,
    time: row.log.time,
    ip: row.log.ip || null,
    user_uuid: row.log.uuid || null,
    session_id: row.sessionId || null,
    msg_type: row.log.msg_type,
    visitor: row.visitor
      ? {
          event: row.visitor.event,
          path: row.visitor.path || null,
          route: row.visitor.route || null,
          target: row.visitor.target || null,
          user_agent: row.visitor.userAgent || null,
          user_agent_summary: row.userAgentSummary,
          fingerprint_id: row.fingerprintId || null,
          webrtc_fingerprint_id: row.webRtcFingerprintId || null,
          detail: row.visitor.detail,
        }
      : null,
    message: row.log.message,
  }
}

async function exportAudit(format: 'json' | 'csv'): Promise<void> {
  if (exporting.value)
    return

  const exportView = logView.value
  if (!await verifyAuditExportPermission(exportView) || exporting.value)
    return

  exporting.value = format
  try {
    const exportLogs = await loadAllFilteredLogs(exportView)
    const exportRows = exportLogs.map(buildAuditLogRow)
    const timestamp = Date.now()
    const filterName = exportView === 'visitor' ? 'visitor' : 'all'

    if (format === 'json') {
      const content = JSON.stringify({
        generated_at: new Date().toISOString(),
        filter: filterName,
        total: exportRows.length,
        logs: exportRows.map(buildAuditJsonRecord),
      }, null, 2)
      downloadText(`komari-audit-${filterName}-${timestamp}.json`, content, 'application/json;charset=utf-8')
    }
    else {
      const content = await buildSnapshotCsvAsync(auditCsvColumns, exportRows, yieldToBrowser)
      downloadText(`komari-audit-${filterName}-${timestamp}.csv`, content, 'text/csv;charset=utf-8', { bom: true })
    }

    void recordVisitorEvent({
      event: format === 'json' ? 'audit_export_json' : 'audit_export_csv',
      path: '/',
      route: 'home',
      target: exportView,
      detail: { record_count: exportRows.length, filter: filterName },
    })
    window.$message?.success(`已导出 ${exportRows.length} 条审计日志。`)
  }
  catch (err) {
    window.$message?.error(err instanceof Error ? err.message : '导出审计日志失败')
  }
  finally {
    exporting.value = null
  }
}

async function fetchLogs(): Promise<void> {
  const granted = await appStore.requireLoginPermission('auditLog', { force: false })
  if (!granted) {
    error.value = '登录状态已过期，请重新登录后查看审计日志。'
    logs.value = []
    total.value = 0
    return
  }

  if (logView.value === 'visitor' && !appStore.visitorAuditSupported) {
    logs.value = []
    total.value = 0
    error.value = null
    return
  }

  const currentRequestId = ++requestId
  loading.value = true
  error.value = null

  try {
    const result = await loadAuditLogs({
      page: page.value,
      limit: limit.value,
      msgType: logView.value === 'visitor' ? 'visitor' : undefined,
    })
    if (currentRequestId !== requestId)
      return
    logs.value = result.logs ?? []
    total.value = Number.isFinite(result.total) ? result.total : logs.value.length
    if (page.value > totalPages.value)
      page.value = totalPages.value
  }
  catch (err) {
    if (currentRequestId !== requestId)
      return
    error.value = err instanceof Error ? err.message : '获取审计日志失败'
    logs.value = []
    total.value = 0
  }
  finally {
    if (currentRequestId === requestId)
      loading.value = false
  }
}

async function setVisitorAuditEnabled(enabled: boolean): Promise<void> {
  if (!appStore.visitorAuditSupported || updatingVisitorAudit.value)
    return

  const granted = await appStore.requireLoginPermission('auditLog', { force: true })
  if (!granted) {
    window.$message?.warning('登录状态已过期，请重新登录后修改访客审计设置。')
    return
  }

  updatingVisitorAudit.value = true
  try {
    await updateVisitorAuditEnabled(enabled)
    if (appStore.publicSettings) {
      appStore.publicSettings = {
        ...appStore.publicSettings,
        visitor_audit_enabled: enabled,
      }
    }
    window.$message?.success(enabled ? '访客审计采集已启用。' : '访客审计采集已暂停。')
  }
  catch (err) {
    window.$message?.error(err instanceof Error ? err.message : '更新访客审计设置失败')
  }
  finally {
    updatingVisitorAudit.value = false
  }
}

function refreshLogs(): void {
  void recordVisitorEvent({
    event: 'audit_refresh',
    path: '/',
    route: 'home',
    target: logView.value,
  })
  void fetchLogs()
}

function setPage(nextPage: number): void {
  const normalized = Math.min(Math.max(1, nextPage), totalPages.value)
  if (normalized === page.value)
    return
  page.value = normalized
  void recordVisitorEvent({
    event: 'audit_page_change',
    path: '/',
    route: 'home',
    target: logView.value,
    detail: { page: normalized },
  })
}

watch([page, limit], () => {
  void fetchLogs()
})

watch(logView, () => {
  if (page.value !== 1)
    page.value = 1
  else
    void fetchLogs()
})

onMounted(() => {
  void fetchLogs()
})
</script>

<template>
  <div class="space-y-4">
    <CardX class="border-none bg-background/50">
      <template #header>
        <div>
          <div class="font-semibold">
            安全审计日志
          </div>
          <div class="text-xs text-muted-foreground">
            管理员操作与访客访问记录，默认每页 {{ limit }} 条。
          </div>
        </div>
      </template>
      <div class="flex flex-col gap-3">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <Tabs v-model="logView">
            <TabsList class="h-8 rounded-md bg-background/60">
              <TabsTrigger value="all" class="h-6.5 rounded-sm px-3 text-xs">
                全部日志
              </TabsTrigger>
              <TabsTrigger value="visitor" class="h-6.5 rounded-sm px-3 text-xs" :disabled="!appStore.visitorAuditSupported">
                访客安全
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <div class="flex flex-wrap items-center justify-between gap-2 sm:justify-end">
            <div class="text-sm text-muted-foreground">
              <template v-if="total > 0">
                {{ pageStart }}-{{ pageEnd }} / {{ total }} 条
              </template>
              <template v-else>
                暂无记录
              </template>
            </div>
            <Button size="sm" variant="outline" class="bg-background/60" :disabled="loading || Boolean(exporting) || total === 0" @click="exportAudit('json')">
              <Icon :icon="exporting === 'json' ? 'tabler:loader-2' : 'tabler:braces'" width="14" height="14" :class="exporting === 'json' && 'animate-spin'" />
              JSON
            </Button>
            <Button size="sm" variant="outline" class="bg-background/60" :disabled="loading || Boolean(exporting) || total === 0" @click="exportAudit('csv')">
              <Icon :icon="exporting === 'csv' ? 'tabler:loader-2' : 'tabler:file-spreadsheet'" width="14" height="14" :class="exporting === 'csv' && 'animate-spin'" />
              CSV
            </Button>
            <Button size="sm" variant="outline" class="bg-background/60" :disabled="loading || Boolean(exporting)" @click="refreshLogs">
              <Icon :icon="loading ? 'tabler:loader-2' : 'tabler:refresh'" width="14" height="14" :class="loading && 'animate-spin'" />
              {{ loading ? '刷新中' : '刷新' }}
            </Button>
          </div>
        </div>
        <div class="flex flex-col gap-2 rounded-md bg-background/45 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex items-start gap-2 text-xs text-muted-foreground">
            <Icon :icon="visitorAuditStatus.icon" width="15" height="15" class="mt-0.5 shrink-0" :class="visitorAuditStatus.tone" />
            <span>{{ visitorAuditStatus.text }}</span>
          </div>
          <Button
            v-if="appStore.visitorAuditSupported"
            size="sm"
            :variant="appStore.visitorAuditEnabled ? 'outline' : 'default'"
            class="h-7 shrink-0 self-end sm:self-auto"
            :disabled="updatingVisitorAudit"
            @click="setVisitorAuditEnabled(!appStore.visitorAuditEnabled)"
          >
            <Icon :icon="updatingVisitorAudit ? 'tabler:loader-2' : appStore.visitorAuditEnabled ? 'tabler:shield-pause' : 'tabler:shield-check'" width="14" height="14" :class="updatingVisitorAudit && 'animate-spin'" />
            {{ updatingVisitorAudit ? '更新中' : appStore.visitorAuditEnabled ? '暂停采集' : '启用采集' }}
          </Button>
        </div>
      </div>
    </CardX>

    <CardX class="border-none bg-background/50" content-class="overflow-x-auto">
      <template #header>
        <div class="font-semibold">
          {{ logView === 'visitor' ? '访客安全事件' : '日志列表' }}
        </div>
      </template>
      <Spinner :show="loading">
        <div v-if="error" class="py-8 text-center text-sm text-destructive">
          {{ error }}
        </div>
        <table v-else-if="rows.length" class="min-w-[1120px] w-full table-fixed text-left text-sm">
          <thead class="text-xs text-muted-foreground">
            <tr class="border-b border-border/60">
              <th class="w-36 px-2 py-2 font-medium">
                时间
              </th>
              <th class="w-36 px-2 py-2 font-medium">
                来源 IP
              </th>
              <th class="w-44 px-2 py-2 font-medium">
                身份 / 会话
              </th>
              <th class="w-40 px-2 py-2 font-medium">
                事件 / 类型
              </th>
              <th class="w-56 px-2 py-2 font-medium">
                路径 / 目标
              </th>
              <th class="px-2 py-2 font-medium">
                客户端 / 详情
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.log.id" class="border-b border-border/40 align-top last:border-0">
              <td class="whitespace-nowrap px-2 py-3 text-xs tabular-nums text-muted-foreground">
                {{ formatLogTime(row.log.time) }}
              </td>
              <td class="px-2 py-3 text-xs tabular-nums">
                <span class="break-all">{{ row.log.ip || '-' }}</span>
              </td>
              <td class="px-2 py-3 text-xs">
                <div class="flex items-center gap-1.5">
                  <Icon :icon="row.log.uuid ? 'tabler:user-shield' : 'tabler:user-question'" width="14" height="14" class="shrink-0 text-muted-foreground" />
                  <span>{{ row.log.uuid ? '已登录用户' : row.visitor ? '匿名访客' : '-' }}</span>
                </div>
                <div v-if="row.log.uuid" class="mt-1 truncate font-mono text-[10px] text-muted-foreground" :title="row.log.uuid">
                  {{ shortId(row.log.uuid) }}
                </div>
                <div v-if="row.sessionId" class="mt-1 truncate font-mono text-[10px] text-muted-foreground" :title="row.sessionId">
                  session {{ shortId(row.sessionId) }}
                </div>
              </td>
              <td class="px-2 py-3">
                <Badge v-if="row.visitor && row.eventMeta" variant="outline" class="max-w-full gap-1 rounded-md border-info/40 text-[11px] text-info">
                  <Icon :icon="row.eventMeta.icon" width="13" height="13" class="shrink-0" />
                  <span class="truncate">{{ row.eventMeta.label }}</span>
                </Badge>
                <Badge v-else variant="outline" class="max-w-full rounded-md text-[11px]" :class="logLevelClass(row.log.msg_type)">
                  <span class="truncate">{{ normalizeLogLevel(row.log.msg_type) }}</span>
                </Badge>
                <div v-if="row.visitor" class="mt-1 truncate font-mono text-[10px] text-muted-foreground" :title="row.visitor.event">
                  {{ row.visitor.event }}
                </div>
              </td>
              <td class="px-2 py-3 text-xs">
                <template v-if="row.visitor">
                  <div class="break-all font-mono text-[11px]">
                    {{ row.visitor.path || '-' }}
                  </div>
                  <div v-if="row.visitor.target" class="mt-1 break-all text-muted-foreground">
                    目标：{{ row.visitor.target }}
                  </div>
                  <div v-if="row.visitor.route" class="mt-1 text-[10px] text-muted-foreground">
                    路由：{{ row.visitor.route }}
                  </div>
                </template>
                <span v-else class="whitespace-pre-wrap break-words">{{ row.log.message || '-' }}</span>
              </td>
              <td class="px-2 py-3 text-xs">
                <template v-if="row.visitor">
                  <div class="flex items-center gap-1.5" :title="row.visitor.userAgent">
                    <Icon icon="tabler:device-desktop" width="14" height="14" class="shrink-0 text-muted-foreground" />
                    <span class="truncate">{{ row.userAgentSummary }}</span>
                  </div>
                  <div v-if="row.fingerprintId" class="mt-1 truncate font-mono text-[10px] text-muted-foreground" :title="row.fingerprintId">
                    fp {{ shortId(row.fingerprintId) }}
                  </div>
                  <div v-if="row.webRtcFingerprintId" class="mt-1 truncate font-mono text-[10px] text-muted-foreground" :title="row.webRtcFingerprintId">
                    webrtc {{ shortId(row.webRtcFingerprintId) }}
                  </div>
                  <div class="mt-1 max-h-20 overflow-hidden whitespace-pre-wrap break-all text-[10px] leading-4 text-muted-foreground" :title="row.detailText">
                    {{ row.detailText }}
                  </div>
                </template>
                <span v-else class="text-muted-foreground">原始内容见路径 / 目标列</span>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="py-10 text-center text-sm text-muted-foreground">
          {{ logView === 'visitor' && !appStore.visitorAuditSupported ? '等待核心发布访客审计能力。' : '暂无审计日志。' }}
        </div>
      </Spinner>
    </CardX>

    <div class="flex items-center justify-end gap-2">
      <Button size="sm" variant="outline" class="bg-background/50" :disabled="loading || page <= 1" @click="setPage(page - 1)">
        <Icon icon="tabler:chevron-left" width="14" height="14" />
        上一页
      </Button>
      <span class="text-xs text-muted-foreground tabular-nums">{{ page }} / {{ totalPages }}</span>
      <Button size="sm" variant="outline" class="bg-background/50" :disabled="loading || page >= totalPages" @click="setPage(page + 1)">
        下一页
        <Icon icon="tabler:chevron-right" width="14" height="14" />
      </Button>
    </div>
  </div>
</template>
