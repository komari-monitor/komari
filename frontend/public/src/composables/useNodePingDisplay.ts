import type { MaybeRefOrGetter } from 'vue'
import { computed, toValue } from 'vue'
import { useNodePingStats } from '@/composables/useNodePingStats'
import { useRouteResults } from '@/composables/useRouteResults'
import { PING_SUMMARY_MAX_COUNT } from '@/constants/load'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/helper'

export type NodePingMetric = 'latency' | 'loss'

export interface NodePingBar {
  key: string
  className: string
  tooltip: string
}

export interface NodePingTaskPanel {
  id: number
  name: string
  latencyDisplay: string
  lossDisplay: string
  latencyBars: NodePingBar[]
  lossBars: NodePingBar[]
  routeBadges: { family: string, label: string, tooltip: string, warning: boolean }[]
}

interface UseNodePingDisplayOptions {
  enabled?: MaybeRefOrGetter<boolean>
  loadingDisplayText?: string
  emptyDisplayText?: string
  loadingPanelTooltipText?: Partial<Record<NodePingMetric, string>>
  emptyPanelTooltipText?: Partial<Record<NodePingMetric, string>>
}

const EMPTY_PING_BAR_COUNT = 20

const ROUTE_NAMES: Record<string, string> = {
  CTGGIA: '中国电信 CTGNet / CN2 GIA（依据可见跳点推断）',
  CN2GIA: '中国电信 CN2 GIA（依据可见跳点推断，不能据此确认商业服务等级）',
  CN2GT: '中国电信 CN2 GT（依据可见跳点推断）',
  CN2: '中国电信 CN2 骨干网（无法单独确认 GIA 服务等级）',
  CTGNet: '中国电信 CTGNet（无法单独确认 GIA 服务等级）',
  '163': '中国电信 ChinaNet 163 骨干网',
  '9929': '中国联通 AS9929 精品网',
  '10099': '中国联通 AS10099 国际网',
  '4837': '中国联通 AS4837 骨干网',
  '4808': '中国联通 AS4808 骨干网',
  CMIN2: '中国移动 CMIN2 精品网',
  CMI: '中国移动 CMI 国际网',
  CMNET: '中国移动 CMNET 骨干网',
  CERNET: '中国教育和科研计算机网 CERNET',
  CSTNET: '中国科技网 CSTNET',
  NO_IPV6: '测速点没有 IPv6（AAAA）地址，无法检测',
}

function getLatencyToneClass(latency: number): string {
  if (latency <= 60)
    return 'bg-signal-1'
  if (latency <= 100)
    return 'bg-signal-2'
  if (latency <= 160)
    return 'bg-signal-3 ping-signal-pattern-2'
  if (latency <= 200)
    return 'bg-signal-4 ping-signal-pattern-3'
  return 'bg-signal-5 ping-signal-pattern-4'
}

function getLossToneClass(loss: number): string {
  if (loss <= 1)
    return 'bg-signal-1'
  if (loss <= 3)
    return 'bg-signal-2'
  if (loss <= 6)
    return 'bg-signal-3 ping-signal-pattern-2'
  if (loss <= 9)
    return 'bg-signal-4 ping-signal-pattern-3'
  return 'bg-signal-5 ping-signal-pattern-4'
}

export function useNodePingDisplay(
  uuid: MaybeRefOrGetter<string>,
  options: UseNodePingDisplayOptions = {},
) {
  const appStore = useAppStore()
  const routeResults = useRouteResults()

  const pingStatsEnabled = computed(() => {
    if (toValue(options.enabled) === false)
      return false
    if (appStore.publicSettings?.record_enabled === false)
      return false
    return appStore.publicSettings?.ping_record_preserve_time !== 0
  })

  const pingStatsHours = computed(() => {
    const preserveTime = appStore.publicSettings?.ping_record_preserve_time
    if (typeof preserveTime === 'number' && preserveTime > 0)
      return Math.min(preserveTime, 1)
    return 1
  })

  const pingStats = useNodePingStats(uuid, {
    hours: pingStatsHours,
    enabled: pingStatsEnabled,
    maxCount: PING_SUMMARY_MAX_COUNT,
  })

  function buildPingBars(metric: NodePingMetric): NodePingBar[] {
    const points = pingStats.history.value
    if (!points.length)
      return []

    return points.map((point, index) => {
      const value = point[metric]

      return {
        key: `${point.time}-${index}`,
        className: value === null
          ? 'bg-muted-foreground/15'
          : metric === 'latency'
            ? getLatencyToneClass(value)
            : getLossToneClass(value),
        tooltip: value === null
          ? `${formatDateTime(point.time, 'HH:mm:ss')}\n无采样数据`
          : metric === 'latency'
            ? `${formatDateTime(point.time, 'HH:mm:ss')}\n${Math.round(value)} ms`
            : `${formatDateTime(point.time, 'HH:mm:ss')}\n${value.toFixed(1)}%`,
      }
    })
  }

  function buildEmptyPingBars(metric: NodePingMetric): NodePingBar[] {
    const tooltip = pingStats.loading.value
      ? '加载中'
      : pingStats.error.value
        ? '加载失败'
        : !pingStatsEnabled.value
            ? '未启用记录'
            : metric === 'latency'
              ? '无采样数据'
              : '无采样数据'

    return Array.from({ length: EMPTY_PING_BAR_COUNT }, (_, index) => ({
      key: `${metric}-empty-${index}`,
      className: 'bg-muted-foreground/10',
      tooltip,
    }))
  }

  const latencyBars = computed(() => buildPingBars('latency'))
  const lossBars = computed(() => buildPingBars('loss'))
  const latencyRenderBars = computed(() => latencyBars.value.length ? latencyBars.value : buildEmptyPingBars('latency'))
  const lossRenderBars = computed(() => lossBars.value.length ? lossBars.value : buildEmptyPingBars('loss'))

  const latencyDisplay = computed(() => {
    if (pingStats.hasData.value)
      return `${Math.round(pingStats.avgLatency.value)} ms`
    if (pingStats.loading.value)
      return options.loadingDisplayText ?? '加载中'
    return options.emptyDisplayText ?? '-'
  })

  const lossDisplay = computed(() => {
    if (pingStats.hasData.value)
      return `${pingStats.avgLoss.value.toFixed(1)}%`
    if (pingStats.loading.value)
      return options.loadingDisplayText ?? '加载中'
    return options.emptyDisplayText ?? '-'
  })

  const latencyPanelTooltip = computed(() => {
    if (!pingStats.hasData.value) {
      if (pingStats.loading.value)
        return options.loadingPanelTooltipText?.latency ?? ''
      return options.emptyPanelTooltipText?.latency ?? ''
    }
    return `平均延迟 ${Math.round(pingStats.avgLatency.value)} ms`
  })

  const taskPanels = computed<NodePingTaskPanel[]>(() => pingStats.taskStats.value.map(task => {
    const taskRoutes = routeResults.value
      .filter(result => result.uuid === toValue(uuid) && result.task_id === task.id)
      .sort((left, right) => (left.family || 'ipv4').localeCompare(right.family || 'ipv4'))
    const routeBadges = task.type !== 'tcp' ? [] : taskRoutes.length === 0
      ? [{ family: '', label: '待检测', tooltip: '等待 Agent 探测回国路由', warning: false }]
      : taskRoutes.map((result) => {
          const label = result.label === 'NO_IPV6' ? '无IPv6' : result.label || '未知'
          return {
            family: result.family || 'ipv4',
            label,
            tooltip: `${ROUTE_NAMES[result.label] || result.label || '线路无法判断'}\n回国路由检测时间：${formatDateTime(result.checked_at)}`,
            warning: label === '未知' || label === '无IPv6',
          }
        })
    const buildBars = (metric: NodePingMetric): NodePingBar[] => {
      const points = task.history
      if (!points.length)
        return buildEmptyPingBars(metric)
      return points.map((point, index) => {
        const value = point[metric]
        return {
          key: `${task.id}-${metric}-${point.time}-${index}`,
          className: value === null
            ? 'bg-muted-foreground/15'
            : metric === 'latency' ? getLatencyToneClass(value) : getLossToneClass(value),
          tooltip: value === null
            ? `${formatDateTime(point.time, 'HH:mm:ss')}\n无采样数据`
            : metric === 'latency'
              ? `${formatDateTime(point.time, 'HH:mm:ss')}\n${Math.round(value)} ms`
              : `${formatDateTime(point.time, 'HH:mm:ss')}\n${value.toFixed(1)}%`,
        }
      })
    }

    return {
      id: task.id,
      name: task.name,
      latencyDisplay: task.avgLatency === null ? '-' : `${Math.round(task.avgLatency)}ms`,
      lossDisplay: task.loss === null ? '-' : `${task.loss.toFixed(1)}%`,
      latencyBars: buildBars('latency'),
      lossBars: buildBars('loss'),
      routeBadges,
    }
  }))

  const lossPanelTooltip = computed(() => {
    if (!pingStats.hasData.value) {
      if (pingStats.loading.value)
        return options.loadingPanelTooltipText?.loss ?? ''
      return options.emptyPanelTooltipText?.loss ?? ''
    }

    const volatility = pingStats.avgVolatility.value > 0
      ? `，平均波动 ${pingStats.avgVolatility.value.toFixed(2)}`
      : ''
    return `平均丢包 ${pingStats.avgLoss.value.toFixed(1)}%${volatility}`
  })

  return {
    pingStats,
    pingStatsEnabled,
    pingStatsHours,
    latencyRenderBars,
    lossRenderBars,
    latencyDisplay,
    lossDisplay,
    latencyPanelTooltip,
    lossPanelTooltip,
    taskPanels,
  }
}
