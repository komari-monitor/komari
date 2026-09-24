<script setup lang="ts">
import type { DetailMetricCardKey } from '@/stores/app'
import type { CurrencyCode } from '@/utils/financeHelper'
import { Icon } from '@iconify/vue'
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardX } from '@/components/ui/card-x'
import { DataTooltip } from '@/components/ui/data-tooltip'
import { Empty } from '@/components/ui/empty'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useNodeProviderMetadata } from '@/composables/useNodeProviderMetadata'
import { LOAD_RECORD_MAX_COUNT } from '@/constants/load'
import { loadNodeLoadRecords } from '@/services/history.service'
import { useAppStore } from '@/stores/app'
import { useNodesStore } from '@/stores/nodes'
import { formatCityNameZh } from '@/utils/cityNameHelper'
import { getCpuBenchmarkRating, getPassMarkCpuLookupUrl } from '@/utils/cpuBenchmark'
import * as financeHelper from '@/utils/financeHelper'
import { gpuUsageFromStatus } from '@/utils/gpuHelper'
import { formatBytesPerSecondWithConfig, formatBytesWithConfig, formatDateTime, formatUptimeWithFormat } from '@/utils/helper'
import { getTrafficCounters, getTrafficUsed } from '@/utils/nodeMetricsHelper'
import { getOSImage, getOSName } from '@/utils/osImageHelper'
import { getDisplayFlagCode, getRegionCode, getRegionDisplayName } from '@/utils/regionHelper'

import { formatPrice, formatPriceWithCycle, getExpireStatus, getExpireText, isFreePrice, parseTags } from '@/utils/tagHelper'

const LoadChart = defineAsyncComponent(() => import('@/components/LoadChart.vue'))
const PingChart = defineAsyncComponent(() => import('@/components/PingChart.vue'))

const route = useRoute()
const router = useRouter()

const appStore = useAppStore()
const nodesStore = useNodesStore()
const exchangeRates = ref(financeHelper.DEFAULT_EXCHANGE_RATES)
const financeCurrency = ref<CurrencyCode>('CNY')

// 近一天网速峰值（B/s）
const peakNetOut = ref(0)
const peakNetIn = ref(0)
const activeDetailSection = ref<'overview' | 'load' | 'ping'>('overview')
const data = computed(() => nodesStore.visibleNodesByUuid.get(String(route.params.id)))
const detailNodes = computed(() => nodesStore.visibleNodes)
const detailNodeIndex = computed(() => detailNodes.value.findIndex(node => node.uuid === data.value?.uuid))
const trafficCounters = computed(() => data.value ? getTrafficCounters(data.value) : { up: 0, down: 0, cycle: false })
const isFavoriteNode = computed(() => data.value ? appStore.isFavoriteNode(data.value.uuid) : false)

let trafficPeakSeq = 0

const { getNodeProviderMetadata } = useNodeProviderMetadata({
  nodes: () => data.value ? [data.value] : [],
  customAliases: () => appStore.providerAliases,
  enabled: () => Boolean(data.value),
  allowGeoLookup: () => appStore.privateFeaturesAllowed,
  geoPermission: 'providerGeoLookup',
})

async function loadTrafficPeakRecords(uuid: string): Promise<Array<{ net_in?: number, net_out?: number }>> {
  if (!appStore.privateFeaturesAllowed)
    return []

  try {
    return await loadNodeLoadRecords(uuid, 24, LOAD_RECORD_MAX_COUNT)
  }
  catch {
    return []
  }
}

// 拉取近一天负载记录，统计网速峰值（上/下行各自取最大瞬时值）
async function fetchTrafficPeak(uuid: string): Promise<void> {
  const seq = ++trafficPeakSeq
  peakNetOut.value = 0
  peakNetIn.value = 0

  const records = await loadTrafficPeakRecords(uuid)
  if (seq !== trafficPeakSeq || data.value?.uuid !== uuid)
    return

  let up = 0
  let down = 0
  for (const r of records) {
    if (typeof r.net_out === 'number' && r.net_out > up)
      up = r.net_out
    if (typeof r.net_in === 'number' && r.net_in > down)
      down = r.net_in
  }
  peakNetOut.value = up
  peakNetIn.value = down
}

onMounted(async () => {
  window.scrollTo({ top: 0, behavior: 'instant' })
  financeCurrency.value = financeHelper.getStoredFinanceCurrency()
  const { rates } = await financeHelper.getDailyExchangeRates()
  exchangeRates.value = rates
})

// 当节点数据加载后尝试获取厂商信息
// 注：节点 IP 通常不直接暴露，这里用节点 uuid 作为 fallback 标识
// 如果 data.value 有 ip 字段则直接用，否则跳过
watch(data, (node) => {
  activeDetailSection.value = 'overview'
  if (node)
    void fetchTrafficPeak(node.uuid)
}, { immediate: true })

const cpuBenchmarkUrl = computed(() => getPassMarkCpuLookupUrl(data.value?.cpu_name ?? ''))
const cpuBenchmarkRating = computed(() => getCpuBenchmarkRating(data.value?.cpu_name ?? ''))
const cpuBenchmarkTierPercent = computed(() => ({ 'S': 92, 'A': 76, 'B': 58, 'C': 38, 'D': 20, '?': 0 }[cpuBenchmarkRating.value.tier]))
const cpuBenchmarkTierTextClass = computed(() => ({
  'S': 'text-yellow-500',
  'A': 'text-blue-500',
  'B': 'text-green-500',
  'C': 'text-orange-500',
  'D': 'text-red-400',
  '?': 'text-muted-foreground',
}[cpuBenchmarkRating.value.tier]))
const cpuBenchmarkTierBarClass = computed(() => ({
  'S': 'bg-yellow-500',
  'A': 'bg-blue-500',
  'B': 'bg-green-500',
  'C': 'bg-orange-500',
  'D': 'bg-red-400',
  '?': 'bg-slate-400',
}[cpuBenchmarkRating.value.tier]))

function navigateDetailNode(offset: number): void {
  const nodes = detailNodes.value
  const index = detailNodeIndex.value
  if (nodes.length < 2 || index < 0)
    return
  const target = nodes[(index + offset + nodes.length) % nodes.length]
  if (target)
    void router.push({ name: 'instance-detail', params: { id: target.uuid } })
}

function selectDetailNode(event: Event): void {
  const uuid = (event.target as HTMLSelectElement).value
  if (uuid && uuid !== data.value?.uuid)
    void router.push({ name: 'instance-detail', params: { id: uuid } })
}

function toggleCurrentFavorite(): void {
  if (data.value)
    appStore.toggleFavoriteNode(data.value.uuid)
}

// 机房/厂商展示：城市 · 厂商 · ASN（缺项自动省略）
const providerMetadata = computed(() => data.value ? getNodeProviderMetadata(data.value) : null)
const vpsProvider = computed(() => providerMetadata.value?.provider ?? null)
const providerDisplay = computed(() => {
  const parts: string[] = []
  const cityName = formatCityNameZh(providerMetadata.value?.geo?.city)
  if (cityName)
    parts.push(cityName)
  if (providerMetadata.value?.provider?.displayName)
    parts.push(providerMetadata.value.provider.displayName)
  if (providerMetadata.value?.geo?.asn)
    parts.push(providerMetadata.value.geo.asn)
  return parts.length ? parts.join(' · ') : '-'
})

// 节点自定义标签
const customTags = computed(() => parseTags(data.value?.tags).map(t => t.text))

// 该节点支持的 IP 协议（仅显示"支持"，不暴露具体 IP）
const ipSupport = computed(() => {
  const node = data.value
  const arr: string[] = []
  if (node?.ipv4)
    arr.push('IPv4')
  if (node?.ipv6)
    arr.push('IPv6')
  return arr
})

const hasPeak = computed(() => peakNetOut.value > 0 || peakNetIn.value > 0)

// 未登录且开启「未登录隐藏价格」时，屏蔽金额类指标（剩余时间为天数，仍显示）
const showPrice = computed(() => appStore.privateFeaturesAllowed || !appStore.hidePriceWhenLoggedOut)

const formatBytes = (bytes: number) => formatBytesWithConfig(bytes, appStore.byteDecimals)
const formatBytesPerSecond = (bytes: number) => formatBytesPerSecondWithConfig(bytes, appStore.byteDecimals)
const formatUptime = (seconds: number) => formatUptimeWithFormat(seconds, 'minute')
const getRegionAltText = (region: string) => getRegionDisplayName(region) || getRegionCode(region)

interface InfoItem {
  label: string
  value: string | undefined
  icon?: string
}

interface MetricCard {
  key: DetailMetricCardKey
  label: string
  value: string
  unit?: string
  icon: string
  valueClass?: string
  tooltip?: string
}

const MONTH_DAYS = 30
const EXPIRES_IN_SUFFIX_REGEX = /^(\d+)\s*(天|days?)$/i
const CURRENCY_SUFFIX_REGEX = /^(\S.*\S)\s+([A-Z]{3})$/

function formatNodeAmount(amount: number, currency: string): string {
  if (!Number.isFinite(amount) || amount <= 0)
    return formatPrice(0, currency, appStore.lang)
  const fractionDigits = Math.abs(amount) >= 100 ? 0 : 2
  const normalizedAmount = Number(amount.toFixed(fractionDigits))
  return formatPrice(normalizedAmount, currency, appStore.lang)
}

function calculateMonthlyAverageCost(price: number, billingCycle: number): number | null {
  const normalizedPrice = Number(price)
  const normalizedCycle = Number(billingCycle)
  if (!Number.isFinite(normalizedPrice) || normalizedPrice <= 0)
    return 0
  if (!Number.isFinite(normalizedCycle) || normalizedCycle <= 0)
    return null
  return normalizedPrice / normalizedCycle * MONTH_DAYS
}

function splitMetricValue(value: string): { value: string, unit?: string } {
  const cycleIndex = value.indexOf(' / ')
  if (cycleIndex > -1)
    return { value: value.slice(0, cycleIndex), unit: value.slice(cycleIndex) }
  const expiresInMatch = value.match(EXPIRES_IN_SUFFIX_REGEX)
  if (expiresInMatch)
    return { value: expiresInMatch[1] ?? value, unit: expiresInMatch[2] ?? undefined }
  const currencyMatch = value.match(CURRENCY_SUFFIX_REGEX)
  if (currencyMatch)
    return { value: currencyMatch[1] ?? value, unit: currencyMatch[2] ?? undefined }
  return { value }
}

const nodePriceText = computed(() => {
  if (!data.value)
    return '-'
  if (Number(data.value.price) <= 0)
    return formatPrice(0, data.value.currency, appStore.lang)
  return formatPriceWithCycle(data.value.price, data.value.billing_cycle, data.value.currency, appStore.lang)
})

const monthlyAverageCostText = computed(() => {
  if (!data.value)
    return '-'
  if (isFreePrice(data.value.price))
    return formatPrice(data.value.price, data.value.currency, appStore.lang)
  const monthlyAverageCost = calculateMonthlyAverageCost(data.value.price, data.value.billing_cycle)
  if (monthlyAverageCost === null)
    return appStore.lang === 'zh-CN' ? '不适用' : 'N/A'
  return `${formatNodeAmount(monthlyAverageCost, data.value.currency)} / 月`
})

const remainingTimeText = computed(() => {
  if (!data.value?.expired_at)
    return '-'
  return getExpireText(data.value.expired_at, appStore.lang)
})

const remainingValueText = computed(() => {
  if (!data.value)
    return '-'
  if (isFreePrice(data.value.price))
    return appStore.lang === 'zh-CN' ? '无' : 'N/A'
  const remainingValueCNY = financeHelper.calculateRemainingValueCNY(data.value, exchangeRates.value)
  const targetRate = exchangeRates.value[financeCurrency.value] || 1
  const formattedValue = financeHelper.formatFinanceAmount(remainingValueCNY * targetRate, financeCurrency.value)
  return `${formattedValue.symbol}${formattedValue.value}`
})

const remainingTimeValueClass = computed(() => {
  if (!data.value?.expired_at)
    return ''
  const status = getExpireStatus(data.value.expired_at)
  if (status === 'expired' || status === 'critical')
    return 'text-destructive'
  if (status === 'warning')
    return 'text-orange-600 dark:text-orange-400'
  if (status === 'long_term')
    return 'text-muted-foreground'
  return 'text-emerald-600 dark:text-emerald-400'
})

function usagePercentage(used: number, total: number): number | null {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0)
    return null
  return Math.min(100, Math.max(0, used / total * 100))
}

function splitMeasurement(value: string): { value: string, unit?: string } {
  const separatorIndex = value.lastIndexOf(' ')
  if (separatorIndex <= 0)
    return { value }
  return {
    value: value.slice(0, separatorIndex),
    unit: value.slice(separatorIndex + 1),
  }
}

function getDetailMetricCard(key: DetailMetricCardKey): MetricCard {
  const node = data.value
  const masked = !showPrice.value
  const nodePrice = splitMetricValue(nodePriceText.value)
  const monthlyAverageCost = splitMetricValue(monthlyAverageCostText.value)
  const remainingTime = splitMetricValue(remainingTimeText.value)
  const remainingValue = splitMetricValue(remainingValueText.value)
  const memoryUsage = usagePercentage(node?.ram ?? 0, node?.mem_total ?? 0)
  const swapUsage = usagePercentage(node?.swap ?? 0, node?.swap_total ?? 0)
  const diskUsage = usagePercentage(node?.disk ?? 0, node?.disk_total ?? 0)
  const nodeTrafficCounters = node ? getTrafficCounters(node) : { up: 0, down: 0, cycle: false }
  const nodeTrafficUsed = node ? getTrafficUsed(node) : 0
  const nodeTrafficLimit = node?.traffic_limit ?? 0
  const nodeHasTrafficLimit = nodeTrafficLimit > 0
  const nodeTrafficPercentage = nodeHasTrafficLimit
    ? Math.min(nodeTrafficUsed / nodeTrafficLimit * 100, 100)
    : 0

  switch (key) {
    case 'nodePrice':
      return { key, label: '节点价格', value: masked ? '***' : nodePrice.value, unit: masked ? undefined : nodePrice.unit, icon: 'tabler:cash' }
    case 'monthlyCost':
      return { key, label: '月均支出', value: masked ? '***' : monthlyAverageCost.value, unit: masked ? undefined : monthlyAverageCost.unit, icon: 'tabler:receipt-2' }
    case 'remainingTime':
      return { key, label: '剩余时间', value: remainingTime.value, unit: remainingTime.unit, icon: 'tabler:calendar-dollar', valueClass: remainingTimeValueClass.value }
    case 'remainingValue':
      return { key, label: '剩余价值', value: masked ? '***' : remainingValue.value, unit: masked ? undefined : remainingValue.unit, icon: 'tabler:coins' }
    case 'cpuUsage':
      return { key, label: 'CPU 使用率', value: (node?.cpu ?? 0).toFixed(1), unit: '%', icon: 'tabler:cpu' }
    case 'gpuUsage': {
      const gpu = gpuUsageFromStatus(node)
      const hasGpu = Boolean(node?.gpu_name?.trim()) || gpu > 0
      return { key, label: 'GPU 使用率', value: hasGpu ? gpu.toFixed(1) : '-', unit: hasGpu ? '%' : undefined, icon: 'tabler:device-desktop-analytics', tooltip: node?.gpu_name?.trim() || undefined }
    }
    case 'memoryUsage':
      return { key, label: '内存使用率', value: memoryUsage === null ? '-' : memoryUsage.toFixed(1), unit: memoryUsage === null ? undefined : '%', icon: 'icon-park-outline:memory', tooltip: `${formatBytes(node?.ram ?? 0)} / ${formatBytes(node?.mem_total ?? 0)}` }
    case 'swapUsage':
      return { key, label: '交换内存', value: swapUsage === null ? '-' : swapUsage.toFixed(1), unit: swapUsage === null ? undefined : '%', icon: 'icon-park-outline:switch', tooltip: `${formatBytes(node?.swap ?? 0)} / ${formatBytes(node?.swap_total ?? 0)}` }
    case 'diskUsage':
      return { key, label: '硬盘使用率', value: diskUsage === null ? '-' : diskUsage.toFixed(1), unit: diskUsage === null ? undefined : '%', icon: 'tabler:server-2', tooltip: `${formatBytes(node?.disk ?? 0)} / ${formatBytes(node?.disk_total ?? 0)}` }
    case 'load':
      return { key, label: '系统负载', value: (node?.load ?? 0).toFixed(2), unit: '1m', icon: 'tabler:chart-line', tooltip: `5m ${(node?.load5 ?? 0).toFixed(2)} / 15m ${(node?.load15 ?? 0).toFixed(2)}` }
    case 'temperature': {
      const temperature = node?.temp ?? 0
      return { key, label: '系统温度', value: temperature > 0 ? temperature.toFixed(1) : '-', unit: temperature > 0 ? '°C' : undefined, icon: 'tabler:temperature' }
    }
    case 'processes':
      return { key, label: '进程数', value: Math.round(node?.process ?? 0).toLocaleString('zh-CN'), icon: 'tabler:list-numbers' }
    case 'connections':
      return { key, label: '连接数', value: Math.round((node?.connections ?? 0) + (node?.connections_udp ?? 0)).toLocaleString('zh-CN'), icon: 'tabler:plug-connected', tooltip: `TCP ${node?.connections ?? 0} / UDP ${node?.connections_udp ?? 0}` }
    case 'uptime':
      return { key, label: '运行时间', value: formatUptime(node?.uptime ?? 0), icon: 'tabler:clock-up' }
    case 'uploadSpeed': {
      const speed = splitMeasurement(formatBytesPerSecond(node?.net_out ?? 0))
      return { key, label: '实时上行', value: speed.value, unit: speed.unit, icon: 'tabler:chevrons-up' }
    }
    case 'downloadSpeed': {
      const speed = splitMeasurement(formatBytesPerSecond(node?.net_in ?? 0))
      return { key, label: '实时下行', value: speed.value, unit: speed.unit, icon: 'tabler:chevrons-down' }
    }
    case 'totalTraffic': {
      const traffic = splitMeasurement(formatBytes(nodeTrafficCounters.up + nodeTrafficCounters.down))
      return { key, label: nodeTrafficCounters.cycle ? '周期流量' : '累计流量', value: traffic.value, unit: traffic.unit, icon: 'tabler:arrows-transfer-up-down', tooltip: `↑ ${formatBytes(nodeTrafficCounters.up)} / ↓ ${formatBytes(nodeTrafficCounters.down)}` }
    }
    case 'trafficQuota':
      return {
        key,
        label: '流量配额',
        value: nodeHasTrafficLimit ? nodeTrafficPercentage.toFixed(1) : '∞',
        unit: nodeHasTrafficLimit ? '%' : undefined,
        icon: 'tabler:gauge',
        tooltip: nodeHasTrafficLimit ? `${formatBytes(nodeTrafficUsed)} / ${formatBytes(nodeTrafficLimit)}` : '无限流量',
      }
    default:
      return { key: 'cpuUsage', label: 'CPU 使用率', value: (node?.cpu ?? 0).toFixed(1), unit: '%', icon: 'tabler:cpu' }
  }
}

// 硬件信息小卡：CPU 单独全宽展示，这里是其余小格。
// - 「架构」格：登录后改显节点 IP（避免未登录访客扫到 IP），未登录或无 IP 时回退显示架构
// - GPU：仅在节点确实有 GPU 时才显示
const hardwareSmallItems = computed<InfoItem[]>(() => {
  const node = data.value
  const items: InfoItem[] = []

  const ip = node?.ipv4 || node?.ipv6
  if (appStore.privateFeaturesAllowed && ip)
    items.push({ label: 'IP', value: ip, icon: 'tabler:world' })
  else
    items.push({ label: '架构', value: node?.arch ?? '-', icon: 'icon-park-outline:application-two' })

  const physicalCores = node?.cpu_physical_cores
  if (typeof physicalCores === 'number' && physicalCores > 0)
    items.push({ label: '物理核心', value: `${physicalCores} 核`, icon: 'tabler:cpu' })

  items.push({ label: '虚拟化', value: node?.virtualization ?? '-', icon: 'icon-park-outline:server' })

  const gpu = node?.gpu_name?.trim()
  if (gpu && gpu.toLowerCase() !== 'none')
    items.push({ label: 'GPU', value: gpu, icon: 'icon-park-outline:video-one' })

  return items
})

const systemInfo = computed<InfoItem[]>(() => [
  { label: '操作系统', value: data.value?.os ?? '-', icon: 'icon-park-outline:computer' },
  { label: '内核版本', value: data.value?.kernel_version ?? '-', icon: 'icon-park-outline:code' },
  { label: '运行时间', value: formatUptime(data.value?.uptime ?? 0), icon: 'icon-park-outline:timer' },
  { label: '厂商', value: providerDisplay.value, icon: vpsProvider.value?.primary.icon ?? 'icon-park-outline:server' },
])

const storageInfo = computed<InfoItem[]>(() => [
  { label: '内存', value: formatBytes(data.value?.mem_total ?? 0), icon: 'icon-park-outline:memory' },
  { label: '内存交换', value: formatBytes(data.value?.swap_total ?? 0), icon: 'icon-park-outline:switch' },
  { label: '硬盘', value: formatBytes(data.value?.disk_total ?? 0), icon: 'icon-park-outline:hard-disk' },
])

const trafficUsed = computed(() => {
  const node = data.value
  return node ? getTrafficUsed(node) : 0
})

const hasTrafficLimit = computed(() => (data.value?.traffic_limit ?? 0) > 0)

const trafficUsedPercentage = computed(() => {
  const trafficLimit = data.value?.traffic_limit ?? 0
  if (trafficLimit <= 0)
    return 0
  return Math.min((trafficUsed.value / trafficLimit) * 100, 100)
})

const trafficUsageText = computed(() => {
  if (!hasTrafficLimit.value)
    return '无限流量'
  return `${formatBytes(trafficUsed.value)} / ${formatBytes(data.value?.traffic_limit ?? 0)}`
})

const trafficProgressStyle = computed(() => ({
  width: `${trafficUsedPercentage.value}%`,
}))

// 总流量进度条按使用率分级着色：≥80% 红、60-80% 琥珀、<60% 绿
const trafficProgressClass = computed(() => {
  const p = trafficUsedPercentage.value
  if (p >= 80)
    return 'bg-red-500/30'
  if (p >= 60)
    return 'bg-amber-500/25'
  return 'bg-emerald-500/20'
})

const metricCards = computed<MetricCard[]>(() => appStore.detailMetricCardOrder.map(getDetailMetricCard))
</script>

<template>
  <div class="instance-detail space-y-4">
    <div v-if="!data" class="p-4">
      <CardX>
        <Empty description="节点不存在或已被删除">
          <template #extra>
            <Button @click="router.push('/')">
              返回首页
            </Button>
          </template>
        </Empty>
      </CardX>
    </div>

    <template v-else>
      <!-- 顶部导航 -->
      <div class="px-4 flex flex-wrap gap-2 items-center sm:gap-4">
        <Button variant="ghost" size="icon-sm" class="bg-background/50 hover:bg-background" aria-label="返回首页" @click="router.push('/')">
          <Icon icon="tabler:arrow-left" :width="16" :height="16" />
        </Button>
        <div class="min-w-0 text-lg font-bold flex gap-2 items-center">
          <img :src="`/images/flags/${getDisplayFlagCode(data.region, appStore.taiwanFlagAsChina)}.svg`" :alt="getRegionAltText(data.region)" class="size-6">
          <span class="truncate">{{ data.name }}</span>
        </div>
        <Badge :variant="data.online ? 'default' : 'destructive'" class="text-xs !rounded">
          {{ data.online ? '在线' : '离线' }}
        </Badge>
        <!-- 节点自定义标签 -->
        <div v-if="customTags.length" class="flex flex-wrap gap-1">
          <Badge
            v-for="(tag, i) in customTags" :key="i" variant="outline"
            class="!text-[11px] rounded text-muted-foreground border-muted-foreground/15 px-1.5 py-0"
          >
            {{ tag }}
          </Badge>
        </div>
        <div class="ml-auto flex h-8 shrink-0 items-center gap-1 rounded-md bg-background/50 p-0.5 backdrop-blur-xs">
          <Button
            variant="ghost" size="icon-sm"
            class="size-7 rounded-sm shadow-none"
            :class="isFavoriteNode && 'text-amber-500'"
            :aria-label="isFavoriteNode ? '取消收藏当前节点' : '收藏当前节点'"
            :title="isFavoriteNode ? '取消收藏' : '收藏节点'"
            @click="toggleCurrentFavorite"
          >
            <Icon :icon="isFavoriteNode ? 'tabler:star-filled' : 'tabler:star'" :width="14" :height="14" />
          </Button>
          <Button
            variant="ghost" size="icon-sm" class="size-7 rounded-sm shadow-none"
            :disabled="detailNodes.length < 2"
            aria-label="上一个节点" title="上一个节点"
            @click="navigateDetailNode(-1)"
          >
            <Icon icon="tabler:chevron-left" :width="14" :height="14" />
          </Button>
          <select
            :value="data.uuid"
            class="h-7 max-w-34 rounded-sm border-0 bg-transparent px-1 text-xs text-foreground outline-none sm:max-w-48"
            aria-label="切换节点"
            @change="selectDetailNode"
          >
            <option v-for="node in detailNodes" :key="node.uuid" :value="node.uuid">
              {{ node.name }}
            </option>
          </select>
          <Button
            variant="ghost" size="icon-sm" class="size-7 rounded-sm shadow-none"
            :disabled="detailNodes.length < 2"
            aria-label="下一个节点" title="下一个节点"
            @click="navigateDetailNode(1)"
          >
            <Icon icon="tabler:chevron-right" :width="14" :height="14" />
          </Button>
        </div>
        <!-- 厂商标识 -->
        <DataTooltip
          v-if="vpsProvider"
          as="div"
          placement="bottom"
          :content="vpsProvider.tooltipLines.join('\n')"
          class="max-w-full"
          content-class="w-72 whitespace-pre-wrap break-words px-2 py-1.5 text-left leading-relaxed"
        >
          <div class="flex max-w-full items-center gap-1.5 rounded-full bg-background/50 px-3 py-1 text-xs text-muted-foreground">
            <Icon :icon="vpsProvider.primary.icon" :width="14" :height="14" class="shrink-0" />
            <span class="whitespace-normal break-words leading-snug">{{ vpsProvider.displayName }}</span>
          </div>
        </DataTooltip>
      </div>

      <div v-if="appStore.nodeDetailSectionTabsEnabled" class="px-4 overflow-x-auto">
        <Tabs v-model="activeDetailSection" class="w-full">
          <TabsList class="w-max h-8 bg-background/50 backdrop-blur-xl rounded-md">
            <TabsTrigger value="overview" class="h-6.5 flex-none shrink-0 gap-1 text-xs border-none data-[state=active]:text-selection shadow-none rounded-sm">
              <Icon icon="tabler:layout-dashboard" :width="12" :height="12" />
              概览
            </TabsTrigger>
            <TabsTrigger value="load" class="h-6.5 flex-none shrink-0 gap-1 text-xs border-none data-[state=active]:text-selection shadow-none rounded-sm">
              <Icon icon="tabler:activity" :width="12" :height="12" />
              负载
            </TabsTrigger>
            <TabsTrigger value="ping" class="h-6.5 flex-none shrink-0 gap-1 text-xs border-none data-[state=active]:text-selection shadow-none rounded-sm">
              <Icon icon="tabler:timeline" :width="12" :height="12" />
              延迟
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      <!-- 价格指标卡片 -->
      <div v-if="!appStore.nodeDetailSectionTabsEnabled || activeDetailSection === 'overview'" class="px-4 grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-4">
        <CardX
          v-for="item in metricCards" :key="item.key" hoverable size="small"
          class="group h-full bg-background/50 border-none hover:bg-background transition-all rounded-md"
          content-class="h-full !p-3"
        >
          <div :title="item.tooltip" class="flex h-full min-h-10 md:min-h-18 flex-col justify-between gap-3">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-medium tracking-wider text-muted-foreground">{{ item.label }}</span>
              <Icon :icon="item.icon" :width="20" :height="20" class="text-slate-500/25 transition-colors group-hover:text-slate-500" />
            </div>
            <div class="min-w-0 space-y-1">
              <div class="flex min-w-0 items-baseline gap-1 truncate font-semibold leading-none" :class="item.valueClass">
                <span class="truncate text-base sm:text-2xl">{{ item.value }}</span>
                <span v-if="item.unit" class="shrink-0 text-[11px] font-medium text-muted-foreground sm:text-xs">{{ item.unit }}</span>
              </div>
            </div>
          </div>
        </CardX>
      </div>

      <!-- 硬件信息 -->
      <div v-if="!appStore.nodeDetailSectionTabsEnabled || activeDetailSection === 'overview'" class="px-4 gap-4 grid grid-cols-1 lg:grid-cols-2">
        <CardX
          title="硬件信息" size="small" content-class="flex-1"
          class="group h-full bg-background/50 border-none hover:bg-background transition-all rounded-md"
        >
          <div class="flex flex-col gap-3 h-full">
            <!-- CPU 信息（跨全宽） -->
            <div class="min-w-0 flex flex-col gap-2 rounded-sm bg-slate-500/5 p-2">
              <div class="flex items-center justify-between gap-2 text-muted-foreground">
                <div class="flex min-w-0 items-center gap-1">
                  <Icon icon="icon-park-outline:cpu" :width="14" :height="14" />
                  <span class="text-xs sm:text-sm">CPU</span>
                </div>
                <a
                  :href="cpuBenchmarkUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="ml-auto inline-flex shrink-0 items-center gap-1.5 rounded-sm bg-slate-500/8 px-2 py-1 text-[11px] text-muted-foreground transition-colors hover:bg-slate-500/12 hover:text-foreground"
                  title="在 PassMark 查看该 CPU 的公开 CPU Mark 跑分与排行"
                >
                  <Icon icon="tabler:chart-bar" :width="13" :height="13" />
                  <span class="hidden sm:inline">CPU Mark 排行</span>
                  <span class="sm:hidden">CPU Mark</span>
                  <Icon icon="tabler:external-link" :width="11" :height="11" />
                </a>
              </div>
              <span class="text-xs sm:text-sm break-all">{{ data.cpu_name }} ({{ data.cpu_cores }} vCPU)</span>
              <div
                class="mt-1 flex items-center gap-2"
                :title="`参考公开天梯与型号代际的本地近似分级，不代表当前 ${data.cpu_cores} vCPU 的实测性能。${cpuBenchmarkRating.description}`"
              >
                <span class="shrink-0 text-xs font-bold" :class="cpuBenchmarkTierTextClass">{{ cpuBenchmarkRating.tier }}</span>
                <div class="h-1.5 flex-1 overflow-hidden rounded-full bg-slate-500/10">
                  <div
                    class="h-full rounded-full transition-all duration-700"
                    :class="cpuBenchmarkTierBarClass"
                    :style="{ width: `${cpuBenchmarkTierPercent}%` }"
                  />
                </div>
                <span class="shrink-0 text-[11px] text-muted-foreground">{{ cpuBenchmarkRating.label }}</span>
              </div>
            </div>

            <!-- IP/架构 · 虚拟化 · GPU(仅在存在时)，列数随数量自适应避免留空，整体撑满高度 -->
            <div class="grid gap-3 flex-1 auto-rows-fr" :class="hardwareSmallItems.length <= 2 ? 'grid-cols-2' : 'grid-cols-3'">
              <div
                v-for="item in hardwareSmallItems" :key="item.label"
                class="min-w-0 flex flex-col gap-1 rounded-sm bg-slate-500/5 p-2"
              >
                <div class="flex gap-1 items-center text-muted-foreground">
                  <Icon v-if="item.icon" :icon="item.icon" :width="14" :height="14" />
                  <span class="text-xs sm:text-sm">{{ item.label }}</span>
                </div>
                <span class="text-xs sm:text-sm break-words">{{ item.value }}</span>
              </div>
            </div>
          </div>
        </CardX>

        <CardX
          title="系统信息" size="small" content-class="flex-1"
          class="group h-full bg-background/50 border-none hover:bg-background transition-all rounded-md"
        >
          <div class="gap-3 grid grid-cols-1 sm:grid-cols-2 h-full sm:auto-rows-fr">
            <div
              v-for="item in systemInfo" :key="item.label"
              class="min-w-0 flex flex-col gap-1 rounded-sm bg-slate-500/5 p-2"
            >
              <div class="flex gap-1 items-center text-muted-foreground">
                <Icon v-if="item.icon" :icon="item.icon" :width="14" :height="14" />
                <span class="text-xs sm:text-sm">{{ item.label }}</span>
              </div>
              <div class="flex min-w-0 gap-2 items-center">
                <img v-if="item.label === '操作系统'" :src="getOSImage(data.os)" :alt="getOSName(data.os)" class="size-5 shrink-0">
                <span class="text-xs sm:text-sm break-words">{{ item.value }}</span>
              </div>
            </div>
          </div>
        </CardX>

        <CardX
          title="存储信息" size="small"
          class="group h-full bg-background/50 border-none hover:bg-background transition-all rounded-md"
        >
          <div class="gap-3 grid grid-cols-3">
            <div
              v-for="item in storageInfo" :key="item.label"
              class="min-w-0 flex flex-col gap-1 rounded-sm bg-slate-500/5 p-2"
            >
              <div class="flex gap-1 items-center text-muted-foreground">
                <Icon v-if="item.icon" :icon="item.icon" :width="14" :height="14" />
                <span class="text-xs sm:text-sm">{{ item.label }}</span>
              </div>
              <span class="text-xs sm:text-sm break-words">{{ item.value }}</span>
            </div>
          </div>
        </CardX>

        <CardX
          title="网络信息" size="small"
          class="group h-full bg-background/50 border-none hover:bg-background transition-all rounded-md"
          content-class="pt-0"
        >
          <div class="gap-3 grid grid-cols-2">
            <div class="relative min-w-0 overflow-hidden rounded-sm bg-slate-500/5 p-2">
              <div
                v-if="hasTrafficLimit"
                class="absolute inset-y-0 left-0 rounded-sm pointer-events-none transition-[width,background-color] duration-300 ease-out"
                :class="trafficProgressClass"
                :style="trafficProgressStyle"
              />
              <div class="relative flex flex-col gap-1.5">
                <div class="flex gap-1 items-center text-muted-foreground">
                  <Icon icon="icon-park-outline:transfer-data" :width="14" :height="14" />
                  <span class="text-xs sm:text-sm">{{ trafficCounters.cycle ? '本周期流量' : '总流量' }}</span>
                  <Badge
                    v-for="proto in ipSupport" :key="proto" variant="outline"
                    class="!text-[10px] rounded text-emerald-600 border-emerald-600/25 px-1 py-0 leading-none"
                  >
                    {{ proto }}
                  </Badge>
                  <div class="flex-1" />
                  <span class="hidden sm:block text-[11px] font-medium text-foreground/70">
                    {{ formatBytes(trafficCounters.up) }} / {{ formatBytes(trafficCounters.down) }}
                  </span>
                </div>
                <span class="text-xs sm:text-sm break-all">{{ trafficUsageText }}</span>
                <span v-if="data?.traffic_cycle_ready && data.traffic_cycle_next_reset" class="text-[10px] text-muted-foreground/80 leading-none">
                  下次重置 {{ formatDateTime(data.traffic_cycle_next_reset) }}
                </span>
                <span v-if="hasPeak" class="text-[10px] text-muted-foreground/80 flex items-center gap-2 leading-none">
                  <span>近一天峰值</span>
                  <span class="text-green-600 flex items-center gap-0.5">
                    <Icon icon="tabler:chevron-up" width="10" height="10" />{{ formatBytesPerSecond(peakNetOut) }}
                  </span>
                  <span class="text-blue-600 flex items-center gap-0.5">
                    <Icon icon="tabler:chevron-down" width="10" height="10" />{{ formatBytesPerSecond(peakNetIn) }}
                  </span>
                </span>
              </div>
            </div>
            <div class="min-w-0 flex flex-col gap-1 rounded-sm bg-slate-500/5 p-2">
              <div class="flex gap-1 items-center text-muted-foreground">
                <Icon icon="icon-park-outline:dashboard-one" :width="14" :height="14" />
                <span class="text-xs sm:text-sm">网络速率</span>
              </div>
              <span class="text-xs sm:text-sm break-all flex flex-row flex-wrap items-center gap-1">
                <Icon icon="tabler:chevron-up" width="12" height="12" />
                {{ formatBytesPerSecond(data?.net_out ?? 0) }}
                <span class="px-0.5" />
                <Icon icon="tabler:chevron-down" width="12" height="12" />
                {{ formatBytesPerSecond(data?.net_in ?? 0) }}
              </span>
            </div>
          </div>
        </CardX>
      </div>

      <LoadChart v-if="!appStore.nodeDetailSectionTabsEnabled || activeDetailSection === 'load'" :uuid="data.uuid" class="px-4" />
      <PingChart v-if="!appStore.nodeDetailSectionTabsEnabled || activeDetailSection === 'ping'" :uuid="data.uuid" class="px-4" />
    </template>
  </div>
</template>
