<script setup lang="ts">
import type { NodeData } from '@/stores/nodes'
import type { PingRecord, StatusRecord } from '@/utils/rpc'
import { Icon } from '@iconify/vue'
import { computed, ref } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardX } from '@/components/ui/card-x'
import { Spinner } from '@/components/ui/spinner'
import { LOAD_RECORD_MAX_COUNT, PING_RECORD_MAX_COUNT } from '@/constants/load'
import { loadLoadRecords, loadPingRecords } from '@/services/history.service'
import { analyzeDiskPrediction } from '@/services/prediction.service'
import { useAppStore } from '@/stores/app'
import { formatBytesPerSecondWithConfig, formatBytesWithConfig } from '@/utils/helper'
import { getTrafficUsed, getTrafficUsedPercentage, hasTrafficLimit } from '@/utils/nodeMetricsHelper'

interface HealthRangeOption {
  key: 'day' | 'week' | 'month' | 'all'
  label: string
  hours: number
}

interface NodeHealthSummary {
  uuid: string
  name: string
  online: boolean
  cpuPeak: number
  memoryPeak: number
  loadPeak: number
  trafficUsedPercentage: number
  trafficUsedBytes: number
  trafficLimitBytes: number
  diskUsagePercentage: number
  diskPredictionDays: number | null
  diskDailyGrowthBytes: number
  avgLatency: number
  avgLoss: number
  avgVolatility: number
  pingHasData: boolean
}

interface NodeRiskSummary {
  node: NodeHealthSummary
  score: number
  reasons: string[]
}

type PingRecordLike = PingRecord

interface PingHealthStats {
  avgLatency: number
  avgLoss: number
  avgVolatility: number
  hasData: boolean
}

const props = defineProps<{
  nodes: NodeData[]
}>()

const appStore = useAppStore()
const selectedRange = ref<HealthRangeOption['key']>('week')
const generatedAt = ref<string>('')
const loading = ref(false)
const historyLoading = ref(false)
const error = ref<string | null>(null)
const historyNote = ref('')
const summaries = ref<NodeHealthSummary[]>([])
let summaryRequestId = 0

const rangeOptions = computed<HealthRangeOption[]>(() => {
  const preserveHours = appStore.publicSettings?.record_preserve_time || 720
  const allHours = Math.max(1, preserveHours)
  const options: HealthRangeOption[] = [
    { key: 'day', label: '日', hours: 24 },
    { key: 'week', label: '周', hours: 168 },
    { key: 'month', label: '月', hours: 720 },
    { key: 'all', label: '有史以来', hours: allHours },
  ]
  return options.filter(option => option.key === 'all' || allHours >= option.hours)
})

const selectedHours = computed(() => rangeOptions.value.find(option => option.key === selectedRange.value)?.hours ?? 168)
const HEALTH_LIST_LIMIT = 10
const HEALTH_LOAD_MAX_COUNT = LOAD_RECORD_MAX_COUNT
const HEALTH_PING_MAX_COUNT = PING_RECORD_MAX_COUNT

function formatBytes(bytes: number): string {
  return formatBytesWithConfig(bytes, appStore.byteDecimals)
}

function formatSpeed(bytes: number): string {
  return formatBytesPerSecondWithConfig(bytes, appStore.byteDecimals)
}

function getMemoryPeak(records: StatusRecord[], fallbackTotal: number): number {
  let peak = 0
  for (const record of records) {
    const total = record.ram_total || fallbackTotal || 1
    peak = Math.max(peak, (record.ram || 0) / total * 100)
  }
  return peak
}

function getCpuPeak(records: StatusRecord[], fallback: number): number {
  let peak = fallback || 0
  for (const record of records)
    peak = Math.max(peak, record.cpu || 0)
  return peak
}

function getLoadPeak(records: StatusRecord[], fallback: number): number {
  let peak = fallback || 0
  for (const record of records)
    peak = Math.max(peak, record.load || 0)
  return peak
}

function getDiskUsagePeak(records: StatusRecord[], fallbackUsed: number, fallbackTotal: number): number {
  let peak = fallbackTotal > 0 ? fallbackUsed / fallbackTotal * 100 : 0
  for (const record of records) {
    const total = record.disk_total || fallbackTotal || 1
    peak = Math.max(peak, (record.disk || 0) / total * 100)
  }
  return peak
}

function getTrafficBurnSpeed(node: NodeData): number {
  if (!hasTrafficLimit(node))
    return 0
  const used = getTrafficUsed(node)
  const uptimeSeconds = Math.max(1, node.uptime || 0)
  return used / uptimeSeconds
}

function average(values: number[]): number {
  return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : 0
}

function percentile(values: number[], p: number): number | null {
  if (!values.length)
    return null
  const sorted = [...values].sort((left, right) => left - right)
  const index = Math.min(sorted.length - 1, Math.max(0, Math.round((sorted.length - 1) * p)))
  return sorted[index] ?? null
}

function buildPingHealthStats(records: PingRecordLike[]): PingHealthStats {
  const byTask = new Map<number, PingRecordLike[]>()
  for (const record of records) {
    const taskRecords = byTask.get(record.task_id) ?? []
    taskRecords.push(record)
    byTask.set(record.task_id, taskRecords)
  }

  const latencyAverages: number[] = []
  const lossValues: number[] = []
  const volatilityValues: number[] = []

  for (const taskRecords of byTask.values()) {
    const validValues = taskRecords.map(record => record.value).filter(value => value >= 0)
    if (!taskRecords.length)
      continue
    lossValues.push((taskRecords.length - validValues.length) / taskRecords.length * 100)
    if (!validValues.length)
      continue
    latencyAverages.push(average(validValues))
    const p50 = percentile(validValues, 0.5)
    const p99 = percentile(validValues, 0.99)
    if (p50 && p99 && p50 > 0)
      volatilityValues.push(p99 / p50)
  }

  return {
    avgLatency: average(latencyAverages),
    avgLoss: average(lossValues),
    avgVolatility: average(volatilityValues),
    hasData: records.length > 0,
  }
}

async function loadPingRecordsByClient(hours: number): Promise<Map<string, PingRecordLike[]>> {
  const records = await loadPingRecords(hours, HEALTH_PING_MAX_COUNT)
  const map = new Map<string, PingRecordLike[]>()
  for (const record of records) {
    if (!record.client)
      continue
    const clientRecords = map.get(record.client) ?? []
    clientRecords.push(record)
    map.set(record.client, clientRecords)
  }
  return map
}

function buildCurrentLoadRecordsByClient(): Map<string, StatusRecord[]> {
  const map = new Map<string, StatusRecord[]>()
  const now = new Date().toISOString()
  for (const node of props.nodes) {
    map.set(node.uuid, [{
      client: node.uuid,
      time: now,
      cpu: node.cpu || 0,
      gpu: node.gpu || 0,
      ram: node.ram || 0,
      ram_total: node.mem_total || 0,
      swap: node.swap || 0,
      swap_total: node.swap_total || 0,
      load: node.load || 0,
      load5: node.load5 || 0,
      load15: node.load15 || 0,
      temp: node.temp || 0,
      disk: node.disk || 0,
      disk_total: node.disk_total || 0,
      net_in: node.net_in || 0,
      net_out: node.net_out || 0,
      net_total_up: node.net_total_up || 0,
      net_total_down: node.net_total_down || 0,
      process: node.process || 0,
      connections: node.connections || 0,
      connections_udp: node.connections_udp || 0,
    }])
  }
  return map
}

async function loadLoadRecordsByClient(hours: number): Promise<Map<string, StatusRecord[]>> {
  const records = await loadLoadRecords(undefined, hours, HEALTH_LOAD_MAX_COUNT)
  const map = new Map<string, StatusRecord[]>()
  for (const record of records) {
    if (!record.client)
      continue
    const clientRecords = map.get(record.client) ?? []
    clientRecords.push(record)
    map.set(record.client, clientRecords)
  }
  return map
}

function buildNodeSummary(node: NodeData, recordsByClient: Map<string, StatusRecord[]>, pingRecordsByClient: Map<string, PingRecordLike[]>): NodeHealthSummary {
  const records = recordsByClient.get(node.uuid) ?? []
  const pingStats = buildPingHealthStats(pingRecordsByClient.get(node.uuid) ?? [])
  const diskPredictionState = analyzeDiskPrediction(records, node.disk_total)
  const diskPrediction = diskPredictionState.prediction

  return {
    uuid: node.uuid,
    name: node.name,
    online: node.online,
    cpuPeak: getCpuPeak(records, node.cpu || 0),
    memoryPeak: getMemoryPeak(records, node.mem_total),
    loadPeak: getLoadPeak(records, node.load || 0),
    trafficUsedPercentage: getTrafficUsedPercentage(node),
    trafficUsedBytes: getTrafficUsed(node),
    trafficLimitBytes: node.traffic_limit || 0,
    diskUsagePercentage: getDiskUsagePeak(records, node.disk || 0, node.disk_total || 0),
    diskPredictionDays: diskPrediction ? diskPrediction.daysUntilFull : null,
    diskDailyGrowthBytes: diskPrediction?.dailyGrowthBytes ?? 0,
    avgLatency: pingStats.avgLatency,
    avgLoss: pingStats.avgLoss,
    avgVolatility: pingStats.avgVolatility,
    pingHasData: pingStats.hasData,
  }
}

async function generateSummary(): Promise<void> {
  const granted = await appStore.requireLoginPermission('healthSummary', { force: true })
  if (!granted) {
    error.value = '登录状态已过期，请重新登录后生成健康摘要。'
    window.$message?.warning(error.value)
    return
  }

  const requestId = ++summaryRequestId
  loading.value = true
  historyLoading.value = false
  error.value = null
  historyNote.value = ''
  generatedAt.value = new Date().toLocaleString('zh-CN')

  const realtimeRecordsByClient = buildCurrentLoadRecordsByClient()
  summaries.value = props.nodes.map(node => buildNodeSummary(node, realtimeRecordsByClient, new Map<string, PingRecordLike[]>()))
  loading.value = false
  historyLoading.value = true
  historyNote.value = '已先用实时数据生成摘要，历史趋势后台补全中。'

  try {
    const hours = selectedHours.value
    const [recordsByClient, pingRecordsByClient] = await Promise.all([
      loadLoadRecordsByClient(hours).catch(() => realtimeRecordsByClient),
      loadPingRecordsByClient(hours).catch(() => new Map<string, PingRecordLike[]>()),
    ])

    if (requestId !== summaryRequestId)
      return

    summaries.value = props.nodes.map(node => buildNodeSummary(node, recordsByClient, pingRecordsByClient))
    historyNote.value = recordsByClient === realtimeRecordsByClient
      ? '历史负载拉取失败，当前结果使用实时数据兜底。'
      : `历史样本最多读取 ${HEALTH_LOAD_MAX_COUNT.toLocaleString('zh-CN')} 条，保证生成速度。`
  }
  catch (err) {
    if (requestId !== summaryRequestId)
      return
    error.value = err instanceof Error ? err.message : '生成健康摘要失败'
  }
  finally {
    if (requestId === summaryRequestId) {
      loading.value = false
      historyLoading.value = false
    }
  }
}

const offlineNodes = computed(() => props.nodes.filter(node => !node.online))
const cpuRankNodes = computed(() => [...summaries.value].sort((a, b) => b.cpuPeak - a.cpuPeak).slice(0, HEALTH_LIST_LIMIT))
const memoryRankNodes = computed(() => [...summaries.value].sort((a, b) => b.memoryPeak - a.memoryPeak).slice(0, HEALTH_LIST_LIMIT))
const diskGrowthRankNodes = computed(() => summaries.value.filter(item => item.diskDailyGrowthBytes > 0).sort((a, b) => b.diskDailyGrowthBytes - a.diskDailyGrowthBytes).slice(0, HEALTH_LIST_LIMIT))
const diskUsageRankNodes = computed(() => summaries.value.filter(item => item.diskUsagePercentage > 0).sort((a, b) => b.diskUsagePercentage - a.diskUsagePercentage).slice(0, HEALTH_LIST_LIMIT))
const diskRankMode = computed<'growth' | 'usage'>(() => diskGrowthRankNodes.value.length ? 'growth' : 'usage')
const diskRankNodes = computed(() => diskRankMode.value === 'growth' ? diskGrowthRankNodes.value : diskUsageRankNodes.value)
const diskFullSoon = computed(() => summaries.value.filter(item => item.diskPredictionDays !== null && item.diskPredictionDays <= appStore.diskPredictionThresholdDays).sort((a, b) => (a.diskPredictionDays ?? Infinity) - (b.diskPredictionDays ?? Infinity)))
const trafficWarnings = computed(() => summaries.value.filter(item => item.trafficLimitBytes > 0 && item.trafficUsedPercentage >= appStore.homeTrafficWarningThreshold).sort((a, b) => b.trafficUsedPercentage - a.trafficUsedPercentage))
const pingWarnings = computed(() => summaries.value.filter(item => item.pingHasData && (item.avgLoss >= 5 || item.avgLatency >= 200 || item.avgVolatility >= 3)).sort((a, b) => b.avgLoss - a.avgLoss))
const fastestTrafficBurn = computed(() => props.nodes.filter(hasTrafficLimit).map(node => ({ node, speed: getTrafficBurnSpeed(node) })).sort((a, b) => b.speed - a.speed).slice(0, HEALTH_LIST_LIMIT))

function thresholdRisk(value: number, threshold: number, weight: number): number {
  if (value < threshold)
    return 0
  const span = Math.max(1, 100 - threshold)
  return Math.min(weight, weight * (value - threshold) / span + weight * 0.4)
}

const riskRankNodes = computed<NodeRiskSummary[]>(() => summaries.value.map((node) => {
  if (!node.online)
    return { node, score: 100, reasons: ['节点离线'] }

  const reasons: string[] = []
  let score = 0
  const loadThreshold = appStore.homeHighLoadThreshold
  const trafficThreshold = appStore.homeTrafficWarningThreshold

  const cpuRisk = thresholdRisk(node.cpuPeak, loadThreshold, 25)
  if (cpuRisk > 0) {
    score += cpuRisk
    reasons.push(`CPU ${node.cpuPeak.toFixed(0)}%`)
  }
  const memoryRisk = thresholdRisk(node.memoryPeak, loadThreshold, 20)
  if (memoryRisk > 0) {
    score += memoryRisk
    reasons.push(`内存 ${node.memoryPeak.toFixed(0)}%`)
  }
  const diskRisk = thresholdRisk(node.diskUsagePercentage, loadThreshold, 15)
  if (diskRisk > 0) {
    score += diskRisk
    reasons.push(`磁盘 ${node.diskUsagePercentage.toFixed(0)}%`)
  }
  const trafficRisk = thresholdRisk(node.trafficUsedPercentage, trafficThreshold, 15)
  if (node.trafficLimitBytes > 0 && trafficRisk > 0) {
    score += trafficRisk
    reasons.push(`流量 ${node.trafficUsedPercentage.toFixed(0)}%`)
  }
  if (node.avgLoss >= 5) {
    score += Math.min(15, node.avgLoss * 0.75)
    reasons.push(`丢包 ${node.avgLoss.toFixed(1)}%`)
  }
  if (node.avgLatency >= 200) {
    score += Math.min(10, node.avgLatency / 100)
    reasons.push(`延迟 ${Math.round(node.avgLatency)}ms`)
  }
  if (node.diskPredictionDays !== null && node.diskPredictionDays <= appStore.diskPredictionThresholdDays) {
    score += 10
    reasons.push(`磁盘约 ${Math.max(0, Math.ceil(node.diskPredictionDays))} 天后满`)
  }

  return { node, score: Math.min(100, Math.round(score)), reasons }
}).filter(item => item.score > 0).sort((a, b) => b.score - a.score).slice(0, HEALTH_LIST_LIMIT))

function riskScoreClass(score: number): string {
  if (score >= 70)
    return 'text-destructive'
  if (score >= 35)
    return 'text-warning'
  return 'text-muted-foreground'
}

const summaryLines = computed(() => {
  if (!generatedAt.value)
    return ['选择时间范围后点击生成摘要，不会在首页自动重算。']

  const lines: string[] = []
  lines.push(`当前范围：${rangeOptions.value.find(option => option.key === selectedRange.value)?.label ?? '-'}，节点 ${props.nodes.length} 台，离线 ${offlineNodes.value.length} 台。`)
  if (offlineNodes.value.length)
    lines.push(`离线节点：${offlineNodes.value.slice(0, 6).map(node => node.name).join('、')}${offlineNodes.value.length > 6 ? '…' : ''}`)
  if (diskFullSoon.value.length)
    lines.push(`${diskFullSoon.value[0]?.name} 磁盘风险最高，预计 ${Math.ceil(diskFullSoon.value[0]?.diskPredictionDays ?? 0)} 天后满。`)
  if (trafficWarnings.value.length)
    lines.push(`${trafficWarnings.value[0]?.name} 流量使用率最高：${trafficWarnings.value[0]?.trafficUsedPercentage.toFixed(1)}%。`)
  if (pingWarnings.value.length)
    lines.push(`${pingWarnings.value[0]?.name} 网络质量异常：平均丢包 ${pingWarnings.value[0]?.avgLoss.toFixed(1)}%，平均延迟 ${Math.round(pingWarnings.value[0]?.avgLatency ?? 0)}ms。`)
  if (lines.length === 1)
    lines.push('未发现明显 CPU、内存、磁盘、流量或 Ping 异常。')
  return lines
})
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <Button
        v-for="option in rangeOptions"
        :key="option.key"
        size="sm"
        variant="ghost"
        class="bg-background/50"
        :class="selectedRange === option.key && 'bg-background text-selection'"
        @click="selectedRange = option.key"
      >
        {{ option.label }}
      </Button>
      <Button size="sm" class="ml-auto" :disabled="loading || historyLoading" @click="generateSummary">
        <Icon :icon="historyLoading ? 'tabler:loader-2' : 'tabler:sparkles'" width="14" height="14" :class="historyLoading && 'animate-spin'" />
        {{ historyLoading ? '补全历史中' : '生成摘要' }}
      </Button>
    </div>

    <Spinner :show="loading">
      <div class="space-y-4">
        <CardX class="border-none bg-background/50">
          <template #header>
            <div>
              <div class="font-semibold">
                周期健康摘要
              </div>
              <div class="text-xs text-muted-foreground">
                {{ generatedAt ? `生成于 ${generatedAt}` : '按需生成，避免首页自动拉取全部历史。' }}
              </div>
              <div v-if="historyNote" class="mt-1 text-[11px] text-muted-foreground">
                {{ historyNote }}
              </div>
            </div>
          </template>
          <div v-if="error" class="text-sm text-destructive">
            {{ error }}
          </div>
          <ul v-else class="space-y-2 text-sm">
            <li v-for="line in summaryLines" :key="line" class="flex gap-2">
              <Icon icon="tabler:point-filled" width="14" height="14" class="mt-0.5 text-success" />
              <span>{{ line }}</span>
            </li>
          </ul>
        </CardX>

        <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          <CardX title="综合异常排行" size="small" class="border-none bg-background/50 md:col-span-2 xl:col-span-3">
            <div v-if="riskRankNodes.length" class="grid gap-1.5 md:grid-cols-2 xl:grid-cols-3">
              <div v-for="(item, index) in riskRankNodes" :key="item.node.uuid" class="flex min-w-0 items-center gap-2 rounded-md bg-slate-500/5 px-2.5 py-2">
                <span class="w-5 shrink-0 text-center text-xs font-semibold tabular-nums text-muted-foreground">{{ index + 1 }}</span>
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-medium">
                    {{ item.node.name }}
                  </div>
                  <div class="truncate text-[11px] text-muted-foreground" :title="item.reasons.join('、')">
                    {{ item.reasons.join(' · ') }}
                  </div>
                </div>
                <span class="shrink-0 text-xs font-semibold tabular-nums" :class="riskScoreClass(item.score)">{{ item.score }}</span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              {{ generatedAt ? '当前阈值下未发现明显异常' : '生成后显示综合异常排行' }}
            </div>
          </CardX>

          <CardX title="离线 / 可用性" size="small" class="border-none bg-background/50">
            <div class="text-2xl font-bold text-destructive">
              {{ offlineNodes.length }}
            </div>
            <div class="mt-2 flex flex-wrap gap-1">
              <Badge v-for="node in offlineNodes.slice(0, HEALTH_LIST_LIMIT)" :key="node.uuid" variant="outline" class="rounded-md text-[11px]">
                {{ node.name }}
              </Badge>
            </div>
          </CardX>

          <CardX title="CPU 峰值排行" size="small" class="border-none bg-background/50">
            <div v-if="cpuRankNodes.length" class="space-y-1 text-sm">
              <div v-for="item in cpuRankNodes" :key="item.uuid" class="flex justify-between gap-2">
                <span class="truncate">{{ item.name }}</span>
                <span class="tabular-nums" :class="item.cpuPeak >= appStore.homeHighLoadThreshold ? 'text-warning' : 'text-muted-foreground'">{{ item.cpuPeak.toFixed(1) }}%</span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              生成后显示 CPU 峰值排行
            </div>
          </CardX>

          <CardX title="内存峰值排行" size="small" class="border-none bg-background/50">
            <div v-if="memoryRankNodes.length" class="space-y-1 text-sm">
              <div v-for="item in memoryRankNodes" :key="item.uuid" class="flex justify-between gap-2">
                <span class="truncate">{{ item.name }}</span>
                <span class="tabular-nums" :class="item.memoryPeak >= appStore.homeHighLoadThreshold ? 'text-warning' : 'text-muted-foreground'">{{ item.memoryPeak.toFixed(1) }}%</span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              生成后显示内存峰值排行
            </div>
          </CardX>

          <CardX :title="diskRankMode === 'growth' ? '磁盘增长最快' : '磁盘占用最高'" size="small" class="border-none bg-background/50">
            <div v-if="diskRankNodes.length" class="space-y-1 text-sm">
              <div v-for="item in diskRankNodes" :key="item.uuid" class="flex justify-between gap-2">
                <span class="truncate">{{ item.name }}</span>
                <span class="tabular-nums" :class="diskRankMode === 'growth' ? 'text-warning' : 'text-muted-foreground'">
                  {{ diskRankMode === 'growth' ? `${formatBytes(item.diskDailyGrowthBytes)}/天` : `${item.diskUsagePercentage.toFixed(1)}%` }}
                </span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              生成后显示磁盘排行
            </div>
          </CardX>

          <CardX title="流量预警" size="small" class="border-none bg-background/50">
            <div v-if="trafficWarnings.length" class="space-y-1 text-sm">
              <div v-for="item in trafficWarnings.slice(0, HEALTH_LIST_LIMIT)" :key="item.uuid" class="flex justify-between gap-2">
                <span class="truncate">{{ item.name }}</span>
                <span class="tabular-nums text-destructive">{{ item.trafficUsedPercentage.toFixed(1) }}%</span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              暂无流量预警
            </div>
          </CardX>

          <CardX title="流量消耗速度" size="small" class="border-none bg-background/50">
            <div v-if="fastestTrafficBurn.length" class="space-y-1 text-sm">
              <div v-for="item in fastestTrafficBurn" :key="item.node.uuid" class="flex justify-between gap-2">
                <span class="truncate">{{ item.node.name }}</span>
                <span class="tabular-nums text-muted-foreground">{{ formatSpeed(item.speed) }}</span>
              </div>
            </div>
            <div v-else class="text-sm text-muted-foreground">
              暂无配额节点
            </div>
          </CardX>
        </div>
      </div>
    </Spinner>
  </div>
</template>
