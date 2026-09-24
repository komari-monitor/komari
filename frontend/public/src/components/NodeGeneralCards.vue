<script setup lang="ts">
import type { GeneralCardKey } from '@/stores/app'
import type { NodeData } from '@/stores/nodes'
import type { CurrencyCode, ExchangeRateSource } from '@/utils/financeHelper'
import type { TopNodeMetric } from '@/utils/nodeMetricsHelper'
import { Icon } from '@iconify/vue'
import { useNow } from '@vueuse/core'
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import NodeEarthGlobe from '@/components/NodeEarthGlobe.vue'
import { CardX } from '@/components/ui/card-x'
import { DataTooltip } from '@/components/ui/data-tooltip'
import { UI_CONFIG } from '@/constants/ui'
import { useAppStore } from '@/stores/app'
import { useNodesStore } from '@/stores/nodes'
import * as financeHelper from '@/utils/financeHelper'
import { gpuUsageFromStatus } from '@/utils/gpuHelper'
import { formatBytesPerSecondSplit, formatBytesSplit } from '@/utils/helper'
import {
  getConnectionCount,
  getExpiryDays,
  getHighLoadMetrics,
  getRealtimeTotalSpeed,
  getTrafficCounters,
  getTrafficUsed,
  getTrafficUsedPercentage,
  isExpiringNode,
  isHighLoadNode,
  isTrafficWarningNode,
} from '@/utils/nodeMetricsHelper'
import { getRegionDisplayName } from '@/utils/regionHelper'
import { isFreeNode } from '@/utils/tagHelper'

interface GeneralMetricCard {
  key: GeneralCardKey
  label: string
  icon: string
  value: string
  unit?: string
  tooltip?: string
  action?: 'financeDetails'
}

interface OnlineStats {
  count: number
  totalSpeed: { up: number, down: number }
  avgCpu: number
  totalGpu: number
  gpuNodeCount: number
  avgLoad: number
  avgLoad5: number
  avgLoad15: number
  totalProcesses: number
  totalConnectionsTcp: number
  totalConnectionsUdp: number
  trafficPeak: TopNodeMetric | null
  uploadPeakNode: TopNodeMetric | null
  downloadPeakNode: TopNodeMetric | null
  gpuPeakNode: TopNodeMetric | null
  connectionPeakNode: TopNodeMetric | null
  highLoadNodes: NodeData[]
}

const props = defineProps<{
  nodes?: NodeData[]
  globeNodes?: NodeData[]
  transitionKey?: string
}>()
const appStore = useAppStore()
const nodesStore = useNodesStore()
const FinanceDetailsDialog = defineAsyncComponent(() => import('@/components/FinanceDetailsDialog.vue'))
// 未登录且开启「未登录隐藏价格」时，屏蔽金额类信息
const showPrice = computed(() => appStore.privateFeaturesAllowed || !appStore.hidePriceWhenLoggedOut)
const exchangeRates = ref(financeHelper.DEFAULT_EXCHANGE_RATES)
const dailyExchangeRates = ref(financeHelper.DEFAULT_EXCHANGE_RATES)
const exchangeRateSource = ref<ExchangeRateSource | 'loading'>('loading')
const exchangeRateUpdatedAt = ref<number | null>(null)
const financeCurrency = ref<CurrencyCode>('CNY')
const excludeFreeNodes = ref(true)
const financeDetailsOpen = ref(false)
const currentTime = useNow({ interval: 1000 })
const summaryNodes = computed(() => props.nodes ?? nodesStore.visibleNodes)
const summaryTransitionKey = computed(() => props.transitionKey ?? nodesStore.visibleNodes.length)
const metricSwitchTransitionProps = computed(() => ({
  ...(appStore.disablePageAnimation
    ? { css: false }
    : { name: 'metric-switch', mode: 'out-in' as const }),
}))

function getMetricSwitchStyle(index: number): Record<string, string> {
  return {
    '--metric-switch-delay': `${index * UI_CONFIG.motion.staggerMs}ms`,
  }
}

function formatBytesText(bytes: number): string {
  const formatted = formatBytesSplit(bytes, appStore.byteDecimals)
  return `${formatted.value} ${formatted.unit}`
}

function formatSpeedText(bytes: number): string {
  const formatted = formatBytesPerSecondSplit(bytes, appStore.byteDecimals)
  return `${formatted.value} ${formatted.unit}`
}

function formatCount(value: number): string {
  return Math.round(value).toLocaleString('zh-CN')
}

function formatDecimal(value: number, digits = 1): string {
  if (!Number.isFinite(value))
    return '0'
  return value.toFixed(digits)
}

function formatTopNodeSpeed(metric: TopNodeMetric | null, fallback = '-'): { value: string, unit?: string, tooltip?: string } {
  if (!metric || metric.value <= 0)
    return { value: fallback }

  const formatted = formatBytesPerSecondSplit(metric.value, appStore.byteDecimals)
  return {
    value: formatted.value,
    unit: formatted.unit,
    tooltip: `${metric.node.name}\n↑ ${formatSpeedText(metric.node.net_out || 0)}\n↓ ${formatSpeedText(metric.node.net_in || 0)}`,
  }
}

function formatTopNodePercentage(metric: TopNodeMetric | null): { value: string, unit?: string, tooltip?: string } {
  if (!metric)
    return { value: '-' }

  const gpuName = metric.node.gpu_name?.trim()
  return {
    value: formatDecimal(metric.value),
    unit: '%',
    tooltip: [metric.node.name, gpuName, `GPU ${formatDecimal(metric.value)}%`].filter(Boolean).join('\n'),
  }
}

function formatNodeNames(nodes: NodeData[], formatter?: (node: NodeData) => string, max = 8): string {
  if (nodes.length === 0)
    return '暂无节点'

  const lines = nodes.slice(0, max).map(node => formatter ? formatter(node) : node.name)
  if (nodes.length > max)
    lines.push(`… 还有 ${nodes.length - max} 台`)
  return lines.join('\n')
}

function getDistribution(nodes: NodeData[], selector: (node: NodeData) => string | null | undefined): Array<[string, number]> {
  const map = new Map<string, number>()
  for (const node of nodes) {
    const key = selector(node)?.trim() || '未知'
    map.set(key, (map.get(key) || 0) + 1)
  }

  return Array.from(map.entries()).sort((a, b) => b[1] - a[1])
}

function getKnownDistribution(nodes: NodeData[], selector: (node: NodeData) => string | null | undefined): Array<[string, number]> {
  const map = new Map<string, number>()
  for (const node of nodes) {
    const key = selector(node)?.trim()
    if (!key)
      continue
    map.set(key, (map.get(key) || 0) + 1)
  }

  return Array.from(map.entries()).sort((a, b) => b[1] - a[1])
}

function formatDistributionTooltip(entries: Array<[string, number]>): string {
  if (entries.length === 0)
    return '暂无数据'

  return entries.slice(0, 8).map(([key, count]) => `${key}: ${count} 台`).join('\n')
}

function formatExpiryNode(node: NodeData): string {
  const days = getExpiryDays(node)
  if (days === null)
    return `${node.name}: 未知`
  if (days <= 0)
    return `${node.name}: 已过期`
  return `${node.name}: ${days} 天`
}

function getNodePeriodCostCNY(node: NodeData, periodDays: number): number {
  if (excludeFreeNodes.value && isFreeNode(node))
    return 0

  return financeHelper.calculatePeriodCostCNY(node, exchangeRates.value, periodDays)
}

function formatCostCard(amountCNY: number): { value: string, unit?: string } {
  if (!showPrice.value)
    return { value: '***' }

  const targetRate = exchangeRates.value[financeCurrency.value] || 1
  const formatted = financeHelper.formatFinanceAmount(amountCNY * targetRate, financeCurrency.value)
  return {
    value: `${formatted.symbol}${formatted.value}`,
  }
}

function updateTopMetric(current: TopNodeMetric | null, node: NodeData, value: number): TopNodeMetric | null {
  if (!Number.isFinite(value))
    return current

  if (!current || value > current.value)
    return { node, value: Math.max(0, value) }

  return current
}

const onlineStats = computed<OnlineStats>(() => {
  const stats: OnlineStats = {
    count: 0,
    totalSpeed: { up: 0, down: 0 },
    avgCpu: 0,
    totalGpu: 0,
    gpuNodeCount: 0,
    avgLoad: 0,
    avgLoad5: 0,
    avgLoad15: 0,
    totalProcesses: 0,
    totalConnectionsTcp: 0,
    totalConnectionsUdp: 0,
    trafficPeak: null,
    uploadPeakNode: null,
    downloadPeakNode: null,
    gpuPeakNode: null,
    connectionPeakNode: null,
    highLoadNodes: [],
  }

  for (const node of summaryNodes.value) {
    if (!node.online)
      continue

    stats.count += 1
    stats.totalSpeed.up += node.net_out || 0
    stats.totalSpeed.down += node.net_in || 0
    stats.avgCpu += node.cpu || 0
    stats.avgLoad += node.load || 0
    stats.avgLoad5 += node.load5 || 0
    stats.avgLoad15 += node.load15 || 0
    stats.totalProcesses += node.process || 0
    stats.totalConnectionsTcp += node.connections || 0
    stats.totalConnectionsUdp += node.connections_udp || 0
    stats.trafficPeak = updateTopMetric(stats.trafficPeak, node, getRealtimeTotalSpeed(node))
    stats.uploadPeakNode = updateTopMetric(stats.uploadPeakNode, node, node.net_out || 0)
    stats.downloadPeakNode = updateTopMetric(stats.downloadPeakNode, node, node.net_in || 0)
    stats.connectionPeakNode = updateTopMetric(stats.connectionPeakNode, node, getConnectionCount(node))
    const gpu = gpuUsageFromStatus(node)
    const hasGpu = Boolean(node.gpu_name?.trim()) || gpu > 0
    if (hasGpu) {
      stats.totalGpu += gpu
      stats.gpuNodeCount += 1
      stats.gpuPeakNode = updateTopMetric(stats.gpuPeakNode, node, gpu)
    }
    if (isHighLoadNode(node, appStore.homeHighLoadThreshold))
      stats.highLoadNodes.push(node)
  }

  if (stats.count > 0) {
    stats.avgCpu /= stats.count
    stats.avgLoad /= stats.count
    stats.avgLoad5 /= stats.count
    stats.avgLoad15 /= stats.count
  }

  return stats
})

const totalSpeed = computed(() => onlineStats.value.totalSpeed)

const totalTraffic = computed(() => {
  const up = summaryNodes.value.reduce((sum, node) => sum + getTrafficCounters(node).up, 0)
  const down = summaryNodes.value.reduce((sum, node) => sum + getTrafficCounters(node).down, 0)
  return { up, down }
})
const hasCycleTraffic = computed(() => summaryNodes.value.some(node => node.traffic_cycle_ready))

const formattedTrafficUp = computed(() => formatBytesSplit(totalTraffic.value.up, appStore.byteDecimals))
const formattedTrafficDown = computed(() => formatBytesSplit(totalTraffic.value.down, appStore.byteDecimals))
const totalTrafficTooltip = computed(() => formatBytesSplit(totalTraffic.value.up + totalTraffic.value.down, appStore.byteDecimals))

const formattedSpeedUp = computed(() => formatBytesPerSecondSplit(totalSpeed.value.up, appStore.byteDecimals))
const formattedSpeedDown = computed(() => formatBytesPerSecondSplit(totalSpeed.value.down, appStore.byteDecimals))

// ==================== 内存 / 硬盘 / 交换内存 汇总 ====================
// 离线节点的 ram / disk / swap 为 0，不影响 used 求和；total 是静态库存信息，按全量统计
const totalMemory = computed(() => {
  let used = 0
  let total = 0
  for (const node of summaryNodes.value) {
    used += node.ram || 0
    total += node.mem_total || 0
  }
  return { used, total }
})

const totalDisk = computed(() => {
  let used = 0
  let total = 0
  for (const node of summaryNodes.value) {
    used += node.disk || 0
    total += node.disk_total || 0
  }
  return { used, total }
})

const totalSwap = computed(() => {
  let used = 0
  let total = 0
  for (const node of summaryNodes.value) {
    used += node.swap || 0
    total += node.swap_total || 0
  }
  return { used, total }
})

const formattedMemoryUsed = computed(() => formatBytesSplit(totalMemory.value.used, appStore.byteDecimals))
const formattedMemoryTotal = computed(() => formatBytesSplit(totalMemory.value.total, appStore.byteDecimals))
const formattedDiskUsed = computed(() => formatBytesSplit(totalDisk.value.used, appStore.byteDecimals))
const formattedDiskTotal = computed(() => formatBytesSplit(totalDisk.value.total, appStore.byteDecimals))
const formattedSwapUsed = computed(() => formatBytesSplit(totalSwap.value.used, appStore.byteDecimals))
const formattedSwapTotal = computed(() => formatBytesSplit(totalSwap.value.total, appStore.byteDecimals))

const onlineNodeCount = computed(() => onlineStats.value.count)
const totalNodeCount = computed(() => summaryNodes.value.length)
const avgCpu = computed(() => onlineStats.value.avgCpu)
const avgGpu = computed(() => onlineStats.value.gpuNodeCount > 0
  ? onlineStats.value.totalGpu / onlineStats.value.gpuNodeCount
  : null)
const gpuNodes = computed(() => summaryNodes.value.filter(node => Boolean(node.gpu_name?.trim()) || (node.gpu || 0) > 0))
const onlineGpuNodes = computed(() => gpuNodes.value.filter(node => node.online))
const gpuPeakNode = computed(() => onlineStats.value.gpuPeakNode)
const avgLoad = computed(() => onlineStats.value.avgLoad)
const avgLoad5 = computed(() => onlineStats.value.avgLoad5)
const avgLoad15 = computed(() => onlineStats.value.avgLoad15)
const totalProcesses = computed(() => onlineStats.value.totalProcesses)
const totalConnectionsTcp = computed(() => onlineStats.value.totalConnectionsTcp)
const totalConnectionsUdp = computed(() => onlineStats.value.totalConnectionsUdp)
const totalCpuCores = computed(() => summaryNodes.value.reduce((sum, node) => sum + (node.cpu_cores || 0), 0))
const trafficQuota = computed(() => {
  let used = 0
  let limit = 0

  for (const node of summaryNodes.value) {
    if ((node.traffic_limit || 0) <= 0)
      continue
    used += getTrafficUsed(node)
    limit += node.traffic_limit || 0
  }

  return { used, limit }
})
const trafficQuotaPercentage = computed(() => {
  if (trafficQuota.value.limit <= 0)
    return 0
  return trafficQuota.value.used / trafficQuota.value.limit * 100
})

const trafficPeak = computed(() => onlineStats.value.trafficPeak)
const uploadPeakNode = computed(() => onlineStats.value.uploadPeakNode)
const downloadPeakNode = computed(() => onlineStats.value.downloadPeakNode)
const connectionPeakNode = computed(() => onlineStats.value.connectionPeakNode)
const offlineNodes = computed(() => summaryNodes.value.filter(node => !node.online))
const highLoadNodes = computed(() => onlineStats.value.highLoadNodes)
const expiringNodes = computed(() => summaryNodes.value.filter(node => isExpiringNode(node, appStore.homeExpiringDays)))
const trafficWarningNodes = computed(() => summaryNodes.value.filter(node => isTrafficWarningNode(node, appStore.homeTrafficWarningThreshold)))
const regionDistribution = computed(() => getKnownDistribution(summaryNodes.value, node => getRegionDisplayName(node.region)))
const systemDistribution = computed(() => getDistribution(summaryNodes.value, node => node.os))
const virtualizationDistribution = computed(() => getDistribution(summaryNodes.value, node => node.virtualization))
const monthlyCostCNY = computed(() => summaryNodes.value.reduce((sum, node) => sum + getNodePeriodCostCNY(node, 30), 0))
const yearlyCostCNY = computed(() => summaryNodes.value.reduce((sum, node) => sum + getNodePeriodCostCNY(node, 365), 0))

const remainingValueCNY = computed(() => {
  return financeHelper.calculateTotalRemainingValueCNY(summaryNodes.value, exchangeRates.value, excludeFreeNodes.value)
})
const remainingValue = computed(() => {
  const targetRate = exchangeRates.value[financeCurrency.value] || 1
  return remainingValueCNY.value * targetRate
})
const formattedRemainingValue = computed(() => {
  return financeHelper.formatFinanceAmount(remainingValue.value, financeCurrency.value)
})
const totalValueCNY = computed(() => {
  return financeHelper.calculateTotalValueCNY(summaryNodes.value, exchangeRates.value, excludeFreeNodes.value)
})
const totalValue = computed(() => {
  const targetRate = exchangeRates.value[financeCurrency.value] || 1
  return totalValueCNY.value * targetRate
})
const formattedTotalValue = computed(() => {
  return financeHelper.formatFinanceAmount(totalValue.value, financeCurrency.value)
})
const totalValueTooltip = computed(() => {
  if (!showPrice.value)
    return '总价值\n***'
  return `总价值\n${formattedTotalValue.value.symbol}${formattedTotalValue.value.value}`
})

const trafficPeakCard = computed(() => formatTopNodeSpeed(trafficPeak.value))
const uploadPeakCard = computed(() => formatTopNodeSpeed(uploadPeakNode.value))
const downloadPeakCard = computed(() => formatTopNodeSpeed(downloadPeakNode.value))
const gpuPeakCard = computed(() => formatTopNodePercentage(gpuPeakNode.value))
const currentTimeText = computed(() => currentTime.value.toLocaleTimeString('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
}))
const currentDateText = computed(() => currentTime.value.toLocaleDateString('zh-CN', {
  year: 'numeric',
  month: 'long',
  day: 'numeric',
  weekday: 'short',
}))
const connectionPeakTooltip = computed(() => {
  const metric = connectionPeakNode.value
  if (!metric)
    return '暂无数据'
  return `${metric.node.name}\nTCP ${formatCount(metric.node.connections || 0)}\nUDP ${formatCount(metric.node.connections_udp || 0)}`
})
const monthlyCostCard = computed(() => formatCostCard(monthlyCostCNY.value))
const yearlyCostCard = computed(() => formatCostCard(yearlyCostCNY.value))

function getCardDefinition(key: GeneralCardKey): GeneralMetricCard {
  switch (key) {
    case 'currentTime':
      return {
        key: 'currentTime',
        label: '当前时间',
        icon: 'tabler:clock',
        value: currentTimeText.value,
        tooltip: currentDateText.value,
      }
    case 'memory':
      return {
        key: 'memory',
        label: '内存用量',
        icon: 'icon-park-outline:memory',
        value: formattedMemoryUsed.value.value,
        unit: `${formattedMemoryUsed.value.unit} / ${formattedMemoryTotal.value.value} ${formattedMemoryTotal.value.unit}`,
      }
    case 'disk':
      return {
        key: 'disk',
        label: '硬盘用量',
        icon: 'tabler:server-2',
        value: formattedDiskUsed.value.value,
        unit: `${formattedDiskUsed.value.unit} / ${formattedDiskTotal.value.value} ${formattedDiskTotal.value.unit}`,
      }
    case 'remainingValue':
      return {
        key: 'remainingValue',
        label: '剩余价值',
        icon: 'tabler:cash',
        value: showPrice.value ? `${formattedRemainingValue.value.symbol}${formattedRemainingValue.value.value}` : '***',
        tooltip: totalValueTooltip.value,
        action: showPrice.value ? 'financeDetails' : undefined,
      }
    case 'totalTraffic':
      return {
        key: 'totalTraffic',
        label: hasCycleTraffic.value ? '本周期流量' : '累计流量',
        icon: 'tabler:download',
        value: totalTrafficTooltip.value.value,
        unit: totalTrafficTooltip.value.unit,
        tooltip: `↑ ${formattedTrafficUp.value.value} ${formattedTrafficUp.value.unit}\n↓ ${formattedTrafficDown.value.value} ${formattedTrafficDown.value.unit}`,
      }
    case 'uploadSpeed':
      return {
        key: 'uploadSpeed',
        label: '实时上行',
        icon: 'tabler:chevrons-up',
        value: formattedSpeedUp.value.value,
        unit: formattedSpeedUp.value.unit,
      }
    case 'downloadSpeed':
      return {
        key: 'downloadSpeed',
        label: '实时下行',
        icon: 'tabler:chevrons-down',
        value: formattedSpeedDown.value.value,
        unit: formattedSpeedDown.value.unit,
      }
    case 'onlineNodes':
      return {
        key: 'onlineNodes',
        label: '在线节点',
        icon: 'tabler:activity-heartbeat',
        value: formatCount(onlineNodeCount.value),
        unit: `/ ${formatCount(totalNodeCount.value)}`,
      }
    case 'avgCpu':
      return {
        key: 'avgCpu',
        label: '平均 CPU',
        icon: 'tabler:cpu',
        value: formatDecimal(avgCpu.value),
        unit: '%',
      }
    case 'avgGpu':
      return {
        key: 'avgGpu',
        label: '平均 GPU',
        icon: 'tabler:device-desktop-analytics',
        value: avgGpu.value === null ? '-' : formatDecimal(avgGpu.value),
        unit: avgGpu.value === null ? undefined : '%',
        tooltip: formatNodeNames(onlineGpuNodes.value, node => `${node.name}: ${formatDecimal(node.gpu || 0)}%`),
      }
    case 'avgLoad':
      return {
        key: 'avgLoad',
        label: '平均负载',
        icon: 'tabler:chart-line',
        value: formatDecimal(avgLoad.value, 2),
        tooltip: `1m ${formatDecimal(avgLoad.value, 2)}\n5m ${formatDecimal(avgLoad5.value, 2)}\n15m ${formatDecimal(avgLoad15.value, 2)}`,
      }
    case 'swap':
      return {
        key: 'swap',
        label: '交换内存',
        icon: 'icon-park-outline:switch',
        value: formattedSwapUsed.value.value,
        unit: `${formattedSwapUsed.value.unit} / ${formattedSwapTotal.value.value} ${formattedSwapTotal.value.unit}`,
      }
    case 'processes':
      return {
        key: 'processes',
        label: '进程总数',
        icon: 'tabler:list-numbers',
        value: formatCount(totalProcesses.value),
      }
    case 'connections':
      return {
        key: 'connections',
        label: '连接数',
        icon: 'tabler:plug-connected',
        value: formatCount(totalConnectionsTcp.value + totalConnectionsUdp.value),
        tooltip: `TCP ${formatCount(totalConnectionsTcp.value)}\nUDP ${formatCount(totalConnectionsUdp.value)}`,
      }
    case 'cpuCores':
      return {
        key: 'cpuCores',
        label: 'CPU 核心',
        icon: 'tabler:chip',
        value: formatCount(totalCpuCores.value),
        unit: 'Core',
      }
    case 'gpuNodes':
      return {
        key: 'gpuNodes',
        label: 'GPU 节点',
        icon: 'tabler:device-imac',
        value: formatCount(gpuNodes.value.length),
        unit: `/ ${formatCount(totalNodeCount.value)}`,
        tooltip: formatNodeNames(gpuNodes.value, node => `${node.name}: ${node.gpu_name?.trim() || 'GPU'}`),
      }
    case 'gpuPeakNode':
      return {
        key: 'gpuPeakNode',
        label: 'GPU 峰值',
        icon: 'tabler:chart-histogram',
        value: gpuPeakCard.value.value,
        unit: gpuPeakCard.value.unit,
        tooltip: gpuPeakCard.value.tooltip,
      }
    case 'trafficQuota':
      return {
        key: 'trafficQuota',
        label: '流量配额',
        icon: 'tabler:gauge',
        value: trafficQuota.value.limit > 0 ? formatDecimal(trafficQuotaPercentage.value) : '-',
        unit: trafficQuota.value.limit > 0 ? '%' : undefined,
        tooltip: trafficQuota.value.limit > 0
          ? `${formatBytesText(trafficQuota.value.used)} / ${formatBytesText(trafficQuota.value.limit)}`
          : '无限流量',
      }
    case 'trafficPeak':
      return {
        key: 'trafficPeak',
        label: '实时峰值',
        icon: 'tabler:activity',
        value: trafficPeakCard.value.value,
        unit: trafficPeakCard.value.unit,
        tooltip: trafficPeakCard.value.tooltip,
      }
    case 'uploadPeakNode':
      return {
        key: 'uploadPeakNode',
        label: '上行最高',
        icon: 'tabler:arrow-big-up-lines',
        value: uploadPeakCard.value.value,
        unit: uploadPeakCard.value.unit,
        tooltip: uploadPeakCard.value.tooltip,
      }
    case 'downloadPeakNode':
      return {
        key: 'downloadPeakNode',
        label: '下行最高',
        icon: 'tabler:arrow-big-down-lines',
        value: downloadPeakCard.value.value,
        unit: downloadPeakCard.value.unit,
        tooltip: downloadPeakCard.value.tooltip,
      }
    case 'offlineNodes':
      return {
        key: 'offlineNodes',
        label: '离线节点',
        icon: 'tabler:plug-connected-x',
        value: formatCount(offlineNodes.value.length),
        unit: `/ ${formatCount(totalNodeCount.value)}`,
        tooltip: formatNodeNames(offlineNodes.value),
      }
    case 'highLoadNodes':
      return {
        key: 'highLoadNodes',
        label: '高负载节点',
        icon: 'tabler:alert-triangle',
        value: formatCount(highLoadNodes.value.length),
        unit: `/ ${formatCount(onlineNodeCount.value)}`,
        tooltip: formatNodeNames(highLoadNodes.value, (node) => {
          const metrics = getHighLoadMetrics(node, appStore.homeHighLoadThreshold)
          return `${node.name}: ${metrics.map(metric => `${metric.label} ${formatDecimal(metric.percentage)}%`).join(' / ')}`
        }),
      }
    case 'expiringNodes':
      return {
        key: 'expiringNodes',
        label: '即将到期',
        icon: 'tabler:calendar-exclamation',
        value: formatCount(expiringNodes.value.length),
        unit: '台',
        tooltip: formatNodeNames(expiringNodes.value, formatExpiryNode),
      }
    case 'trafficWarnings':
      return {
        key: 'trafficWarnings',
        label: '流量预警',
        icon: 'tabler:traffic-cone',
        value: formatCount(trafficWarningNodes.value.length),
        unit: '台',
        tooltip: formatNodeNames(trafficWarningNodes.value, node => `${node.name}: ${formatDecimal(getTrafficUsedPercentage(node))}%`),
      }
    case 'connectionPeakNode':
      return {
        key: 'connectionPeakNode',
        label: '连接峰值',
        icon: 'tabler:plug-connected',
        value: connectionPeakNode.value ? formatCount(connectionPeakNode.value.value) : '-',
        tooltip: connectionPeakTooltip.value,
      }
    case 'regionDistribution':
      return {
        key: 'regionDistribution',
        label: '地区分布',
        icon: 'tabler:map-pin',
        value: formatCount(regionDistribution.value.length),
        unit: '个',
        tooltip: formatDistributionTooltip(regionDistribution.value),
      }
    case 'systemDistribution':
      return {
        key: 'systemDistribution',
        label: '系统分布',
        icon: 'tabler:device-desktop',
        value: systemDistribution.value[0]?.[0] ?? '-',
        unit: systemDistribution.value[0] ? `${systemDistribution.value[0][1]} 台` : undefined,
        tooltip: formatDistributionTooltip(systemDistribution.value),
      }
    case 'virtualizationDistribution':
      return {
        key: 'virtualizationDistribution',
        label: '虚拟化',
        icon: 'tabler:box-multiple',
        value: virtualizationDistribution.value[0]?.[0] ?? '-',
        unit: virtualizationDistribution.value[0] ? `${virtualizationDistribution.value[0][1]} 台` : undefined,
        tooltip: formatDistributionTooltip(virtualizationDistribution.value),
      }
    case 'monthlyCost':
      return {
        key: 'monthlyCost',
        label: '月费用估算',
        icon: 'tabler:calendar-dollar',
        value: monthlyCostCard.value.value,
        unit: monthlyCostCard.value.unit,
      }
    case 'yearlyCost':
      return {
        key: 'yearlyCost',
        label: '年费用估算',
        icon: 'tabler:receipt-2',
        value: yearlyCostCard.value.value,
        unit: yearlyCostCard.value.unit,
      }
    default:
      return getCardDefinition('memory')
  }
}

const visibleCards = computed(() => appStore.generalCardOrder.map(getCardDefinition))
const showEarth = computed(() => !appStore.hideEarth)
const isTiledEarth = computed(() => showEarth.value && appStore.earthRenderer === 'tiled')
const shouldRenderHeader = computed(() => showEarth.value || visibleCards.value.length > 0)
const hasExtraCards = computed(() => visibleCards.value.length > 6)
const wrapperClass = computed(() => {
  if (!showEarth.value)
    return 'p-4 grid grid-cols-1 gap-2 h-auto'

  if (isTiledEarth.value)
    return 'p-3 sm:p-4 grid grid-cols-12 gap-2 sm:gap-3 h-auto min-h-[40rem] sm:min-h-[30rem] md:min-h-[36rem] lg:min-h-[40rem]'

  return hasExtraCards.value
    ? 'p-4 grid grid-cols-12 gap-2 h-auto md:min-h-58'
    : 'p-4 grid grid-cols-12 grid-rows-1 gap-2 h-auto md:h-58'
})
const earthClass = computed(() => {
  if (isTiledEarth.value)
    return 'col-span-12 row-start-2 min-h-[18rem] h-[18rem] sm:h-[20rem] md:h-[24rem] lg:h-[28rem]'

  return 'col-span-12 col-start-1 md:col-span-6 md:col-start-7 md:row-start-1'
})
const cardGridClass = computed(() => {
  if (!showEarth.value)
    return 'col-span-1 grid grid-cols-3 md:grid-cols-6 gap-2'

  if (isTiledEarth.value)
    return 'col-span-12 row-start-1 z-9 grid grid-cols-12 auto-rows-[4.75rem] sm:auto-rows-[5rem] md:auto-rows-[5.8rem] gap-2 sm:gap-3'

  return hasExtraCards.value
    ? 'h-auto -mt-42 md:mt-0 col-span-12 row-start-3 z-9 md:h-auto md:col-span-6 md:row-start-1 grid grid-cols-12 auto-rows-[5rem] md:auto-rows-[7rem] gap-2'
    : 'h-42 -mt-42 md:mt-0 col-span-12 row-start-3 z-9 md:h-auto md:col-span-6 md:row-start-1 grid grid-cols-12 grid-rows-2 gap-2'
})
const cardClass = 'group relative z-10 h-full bg-background/50 border-none hover:bg-background backdrop-blur-sm md:backdrop-blur-none transition-all'
const cardPositionClasses = [
  'col-span-4 row-span-1 col-start-1 row-start-1',
  'col-span-4 row-span-1 col-start-1 row-start-2',
  'col-span-4 row-span-1 col-start-5 row-start-1',
  'col-span-4 row-span-1 col-start-5 row-start-2',
  'col-span-4 row-span-1 col-start-9 row-start-1',
  'col-span-4 row-span-1 col-start-9 row-start-2',
]
const tiledCardPositionClasses = [
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-1 row-start-1',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-4 row-start-1',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-7 row-start-2 sm:row-start-1',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-10 row-start-2 sm:row-start-1',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-1 row-start-3 sm:row-start-2',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-4 row-start-3 sm:row-start-2',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-7 row-start-4 sm:row-start-2',
  'col-span-6 sm:col-span-3 row-span-1 sm:col-start-10 row-start-4 sm:row-start-2',
]
const unitClass = 'text-[11px] md:text-xs font-medium text-muted-foreground truncate'

function getCardPositionClass(index: number): string {
  if (!showEarth.value)
    return 'col-span-1 min-h-18 md:min-h-28'

  if (isTiledEarth.value)
    return tiledCardPositionClasses[index] ?? 'col-span-6 sm:col-span-3 row-span-1'

  return cardPositionClasses[index] ?? 'col-span-4 row-span-1'
}

function activateCard(card: GeneralMetricCard) {
  if (card.action === 'financeDetails')
    financeDetailsOpen.value = true
}

function handleCardKeydown(event: KeyboardEvent, card: GeneralMetricCard) {
  if (!card.action || (event.key !== 'Enter' && event.key !== ' '))
    return
  event.preventDefault()
  activateCard(card)
}

function updateFinanceCurrency(currency: CurrencyCode) {
  financeCurrency.value = currency
  financeHelper.setStoredFinanceCurrency(currency)
}

function updateExcludeFreeNodes(exclude: boolean) {
  excludeFreeNodes.value = exclude
  financeHelper.setExcludeFreeNodes(exclude)
}

function updateExchangeRate(currency: CurrencyCode, value: number) {
  financeHelper.setExchangeRateOverride(currency, value)
  exchangeRates.value = { ...exchangeRates.value, [currency]: value, CNY: 1 }
}

function resetExchangeRates() {
  financeHelper.clearExchangeRateOverrides()
  exchangeRates.value = { ...dailyExchangeRates.value }
}

onMounted(async () => {
  financeCurrency.value = financeHelper.getStoredFinanceCurrency()
  excludeFreeNodes.value = financeHelper.shouldExcludeFreeNodes()

  const { rates, source, updatedAt } = await financeHelper.getDailyExchangeRates()
  dailyExchangeRates.value = rates
  exchangeRates.value = financeHelper.applyExchangeRateOverrides(rates)
  exchangeRateSource.value = source
  exchangeRateUpdatedAt.value = updatedAt
})
</script>

<template>
  <div v-if="shouldRenderHeader" :class="wrapperClass">
    <NodeEarthGlobe
      v-if="showEarth"
      :nodes="globeNodes"
      :class="earthClass"
    />

    <div v-if="visibleCards.length > 0" :class="cardGridClass">
      <CardX
        v-for="(card, index) in visibleCards"
        :key="card.key"
        :data-general-card-key="card.key"
        hoverable
        :class="[cardClass, getCardPositionClass(index), card.action && 'cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring']"
        content-class="h-full !p-3"
        :role="card.action ? 'button' : undefined"
        :tabindex="card.action ? 0 : undefined"
        :aria-label="card.action ? `查看${card.label}明细` : undefined"
        @click="activateCard(card)"
        @keydown="handleCardKeydown($event, card)"
      >
        <div class="flex h-full flex-col justify-between gap-1">
          <div class="flex items-start justify-between gap-2">
            <span class="text-xs font-medium tracking-wider text-muted-foreground truncate">{{ card.label }}</span>
            <Icon
              :icon="card.icon" :width="20" :height="20"
              class="shrink-0 text-slate-500/20 group-hover:text-slate-500 transition-colors"
            />
          </div>
          <DataTooltip
            as="span"
            placement="top"
            :content="card.tooltip"
            class="min-w-0"
            content-class="whitespace-pre px-2 py-1 left-0 -translate-x-0 leading-normal"
          >
            <Transition v-bind="metricSwitchTransitionProps">
              <div
                :key="`${card.key}-${summaryTransitionKey}`"
                class="flex items-baseline gap-1 min-w-0"
                :style="getMetricSwitchStyle(index)"
              >
                <span class="text-md md:text-2xl font-bold leading-none tracking-tight truncate">
                  {{ card.value }}
                </span>
                <span v-if="card.unit" :class="unitClass">
                  {{ card.unit }}
                </span>
              </div>
            </Transition>
          </DataTooltip>
        </div>
      </CardX>
    </div>
  </div>

  <FinanceDetailsDialog
    v-if="financeDetailsOpen"
    v-model:open="financeDetailsOpen"
    :nodes="summaryNodes"
    :rates="exchangeRates"
    :source="exchangeRateSource"
    :rates-updated-at="exchangeRateUpdatedAt"
    :currency="financeCurrency"
    :exclude-free="excludeFreeNodes"
    :now="currentTime"
    @update:currency="updateFinanceCurrency"
    @update:exclude-free="updateExcludeFreeNodes"
    @update:rate="updateExchangeRate"
    @reset-rates="resetExchangeRates"
  />
</template>

<style scoped>
.metric-switch-enter-active,
.metric-switch-leave-active {
  transition:
    opacity 160ms ease,
    transform 180ms cubic-bezier(0.22, 1, 0.36, 1),
    filter 180ms ease;
}

.metric-switch-enter-active {
  transition-delay: var(--metric-switch-delay, 0ms);
}

.metric-switch-enter-from {
  opacity: 0;
  transform: translateY(6px);
  filter: blur(3px);
}

.metric-switch-leave-to {
  opacity: 0;
  transform: translateY(-4px);
  filter: blur(2px);
}

@media (prefers-reduced-motion: reduce) {
  .metric-switch-enter-active,
  .metric-switch-leave-active {
    transition: none;
    transition-delay: 0ms;
  }

  .metric-switch-enter-from,
  .metric-switch-leave-to {
    opacity: 1;
    transform: none;
    filter: none;
  }
}
</style>
