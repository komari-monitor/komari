<script setup lang="ts">
import type { MetricSeries, PingMetricTaskStats, PingRecord, PingTaskInfo } from '@/utils/rpc'
import { Icon } from '@iconify/vue'
import dayjs from 'dayjs'
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch, watchEffect } from 'vue'
import VChart from 'vue-echarts'
import { Button } from '@/components/ui/button'
import { Empty } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { PING_RECORD_MAX_COUNT } from '@/constants/load'
import { loadPingRecordsWithTasks } from '@/services/history.service'
import { loadPingMetricStats, loadPublicPingTasks, queryMetrics } from '@/services/metrics.service'
import { useAppStore } from '@/stores/app'
import { ACCESSIBLE_LINE_TYPES, getChartSeriesPalette } from '@/utils/chartPalette'
import { isPingMetric, normalizeMetricSeriesList, orderPingTasksByBackend, PING_LATENCY_METRIC, pingTaskId, pingTaskName } from '@/utils/metricSeries'
import { cutPeakValues, interpolateNullsLinear } from '@/utils/recordHelper'
import '@/utils/echarts' // 共享 ECharts 配置

const props = defineProps<{
  uuid: string
}>()

const appStore = useAppStore()
const isDark = computed(() => appStore.isDark)

interface CustomRange {
  start: dayjs.Dayjs
  end: dayjs.Dayjs
  hours: number
}

// 图表主题相关颜色
const chartThemeColors = computed(() => ({
  text: isDark.value ? 'rgba(255, 255, 255, 0.85)' : 'rgba(0, 0, 0, 0.85)',
  textSecondary: isDark.value ? 'rgba(255, 255, 255, 0.55)' : 'rgba(0, 0, 0, 0.55)',
  textTertiary: isDark.value ? 'rgba(255, 255, 255, 0.35)' : 'rgba(0, 0, 0, 0.35)',
  borderColor: isDark.value ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.06)',
  splitLineColor: isDark.value ? 'rgba(255, 255, 255, 0.06)' : 'rgba(0, 0, 0, 0.06)',
  tooltipBg: isDark.value ? 'rgba(40, 40, 40, 0.95)' : 'rgba(255, 255, 255, 0.8)',
  tooltipShadow: isDark.value ? 'rgba(0, 0, 0, 0.4)' : 'rgba(0, 0, 0, 0.06)',
  crosshairColor: isDark.value ? 'rgba(255, 255, 255, 0.15)' : 'rgba(0, 0, 0, 0.1)',
}))

const chartColors = reactive(getChartSeriesPalette(appStore.colorVisionFriendly))

watchEffect(() => {
  chartColors.splice(0, chartColors.length, ...getChartSeriesPalette(appStore.colorVisionFriendly))
})

// 从 publicSettings 获取记录保留时间
const maxPingRecordPreserveTime = computed(() => appStore.publicSettings?.ping_record_preserve_time || 168)

// 视图选项
const presetViews = [
  { label: '1 小时', hours: 1 },
  { label: '6 小时', hours: 6 },
  { label: '12 小时', hours: 12 },
  { label: '1 天', hours: 24 },
]
const CUSTOM_VIEW_LABEL = '自定义'
const DEFAULT_CUSTOM_RANGE_HOURS = 24

// 可用视图列表
const availableViews = computed(() => {
  const views: { label: string, hours?: number }[] = []
  const maxHours = maxPingRecordPreserveTime.value

  for (const v of presetViews) {
    if (maxHours >= v.hours) {
      views.push(v)
    }
  }

  const maxPreset = presetViews.at(-1)
  if (maxPreset && maxHours > maxPreset.hours) {
    const label = maxHours % 24 === 0
      ? `${Math.floor(maxHours / 24)} 天`
      : `${maxHours} 小时`
    views.push({ label, hours: maxHours })
  }
  else if (maxHours > 1 && !presetViews.some(v => v.hours === maxHours)) {
    const label = maxHours % 24 === 0
      ? `${Math.floor(maxHours / 24)} 天`
      : `${maxHours} 小时`
    views.push({ label, hours: maxHours })
  }

  views.push({ label: CUSTOM_VIEW_LABEL })
  return views
})

// 当前选中的视图
const selectedView = ref<string>('')
const customStartInput = ref('')
const customEndInput = ref('')
const appliedCustomRange = shallowRef<CustomRange | null>(null)
const isCustomRange = computed(() => selectedView.value === CUSTOM_VIEW_LABEL)
const customRange = computed<CustomRange | null>(() => {
  if (!customStartInput.value || !customEndInput.value)
    return null

  const start = dayjs(customStartInput.value)
  const end = dayjs(customEndInput.value)
  if (!start.isValid() || !end.isValid() || !end.isAfter(start))
    return null

  return {
    start,
    end,
    hours: Math.max(1, Math.ceil(end.diff(start, 'hour', true))),
  }
})
const customRangeError = computed(() => {
  if (!isCustomRange.value || (!customStartInput.value && !customEndInput.value))
    return ''
  if (!customStartInput.value || !customEndInput.value)
    return '请选择开始和结束时间'
  return customRange.value ? '' : '结束时间必须晚于开始时间'
})
const selectedHours = computed(() => {
  if (isCustomRange.value)
    return appliedCustomRange.value?.hours ?? customRange.value?.hours ?? DEFAULT_CUSTOM_RANGE_HOURS

  const view = availableViews.value.find(v => v.label === selectedView.value)
  return view?.hours || 1
})

function ensureDefaultCustomRange() {
  if (customStartInput.value && customEndInput.value)
    return

  const end = dayjs()
  const hours = Math.max(1, Math.min(DEFAULT_CUSTOM_RANGE_HOURS, maxPingRecordPreserveTime.value))
  customStartInput.value = end.subtract(hours, 'hour').format('YYYY-MM-DDTHH:mm')
  customEndInput.value = end.format('YYYY-MM-DDTHH:mm')
}

// 初始化默认视图
watch(availableViews, (views) => {
  const firstView = views[0]
  if (firstView && !selectedView.value) {
    selectedView.value = firstView.label
  }
}, { immediate: true })

// ==================== 数据状态 ====================
const remoteData = shallowRef<PingRecord[]>([])
const tasks = shallowRef<PingTaskInfo[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const legacyCustomRangeFallback = ref(false)

// 任务选择
const selectedTaskIds = ref<number[]>([])
const cutPeak = ref(false)
const isTouchTooltipMode = ref(false)
const activeTaskTooltipId = ref<number | null>(null)
const smoothInfoTooltipOpen = ref(false)

const chartMargin = { top: 30, right: 24, bottom: 52, left: 56 }
let coarsePointerMediaQuery: MediaQueryList | null = null
let fetchRecordsSequence = 0

function syncTouchTooltipMode() {
  if (typeof window === 'undefined') {
    isTouchTooltipMode.value = false
    return
  }

  const hasCoarsePointer = window.matchMedia('(pointer: coarse)').matches
  const hasTouchPoints = typeof navigator !== 'undefined' && navigator.maxTouchPoints > 0
  isTouchTooltipMode.value = hasCoarsePointer || hasTouchPoints
}

function setTaskTooltipOpen(taskId: number, open: boolean) {
  activeTaskTooltipId.value = open ? taskId : activeTaskTooltipId.value === taskId ? null : activeTaskTooltipId.value
}

function toggleTaskTooltip(taskId: number) {
  if (!isTouchTooltipMode.value)
    return

  activeTaskTooltipId.value = activeTaskTooltipId.value === taskId ? null : taskId
  smoothInfoTooltipOpen.value = false
}

function toggleSmoothInfoTooltip() {
  if (!isTouchTooltipMode.value)
    return

  smoothInfoTooltipOpen.value = !smoothInfoTooltipOpen.value
  if (smoothInfoTooltipOpen.value) {
    activeTaskTooltipId.value = null
  }
}

function normalizeMetricTaskId(taskId: string): number {
  if (!taskId.trim())
    return Number.NaN

  const numericTaskId = Number(taskId)
  if (Number.isFinite(numericTaskId))
    return numericTaskId

  let hash = 0
  for (let index = 0; index < taskId.length; index++)
    hash = (hash * 31 + taskId.charCodeAt(index)) | 0
  return Math.abs(hash)
}

function normalizeMetricTask(stat: PingMetricTaskStats): PingTaskInfo {
  return {
    id: normalizeMetricTaskId(stat.task_id),
    name: stat.name?.trim() || pingTaskName(stat) || `Task ${stat.task_id}`,
    interval: stat.interval ?? 0,
    loss: stat.loss,
    min: stat.min,
    max: stat.max,
    avg: stat.avg,
    latest: stat.latest,
    p50: stat.p50,
    p99: stat.p99,
    p99_p50_ratio: stat.p99_p50_ratio,
    stddev: stat.stddev,
    total: stat.total,
    valid: stat.valid,
    loss_approximate: stat.loss_approximate,
    type: stat.type,
  }
}

function buildMetricRecords(seriesList: MetricSeries[]): PingRecord[] {
  const records: PingRecord[] = []
  const normalizedSeriesList = normalizeMetricSeriesList(seriesList).filter(isPingMetric)

  for (const series of normalizedSeriesList) {
    const taskId = normalizeMetricTaskId(pingTaskId(series))
    if (!Number.isFinite(taskId))
      continue

    for (const point of series.points) {
      if (point.value === null)
        continue

      records.push({
        client: series.entity_id,
        task_id: taskId,
        time: point.time,
        value: point.value,
      })
    }
  }

  return records.sort((a, b) => dayjs(a.time).valueOf() - dayjs(b.time).valueOf())
}

async function loadMetricPingPayload(nodeUuid: string): Promise<{ records: PingRecord[], tasks: PingTaskInfo[] } | null> {
  const range = appliedCustomRange.value
  const metricRangeParams = isCustomRange.value && range
    ? { start: range.start.toDate().toISOString(), end: range.end.toDate().toISOString() }
    : { hours: selectedHours.value }

  const [statsResult, metricsResult, backendTasksResult] = await Promise.allSettled([
    loadPingMetricStats({ entity_id: nodeUuid, ...metricRangeParams, max_points: PING_RECORD_MAX_COUNT }),
    queryMetrics({
      metric_keys: [PING_LATENCY_METRIC],
      entity_id: nodeUuid,
      ...metricRangeParams,
      downsample: true,
      fill_empty: true,
      max_points: PING_RECORD_MAX_COUNT,
      aggregation: 'avg',
    }),
    loadPublicPingTasks(),
  ])

  const metricStats = statsResult.status === 'fulfilled'
    ? (statsResult.value.stats ?? []).filter(stat => stat.entity_id === nodeUuid)
    : []
  const metricRecords = metricsResult.status === 'fulfilled'
    ? buildMetricRecords(metricsResult.value.series)
    : []

  const metricTaskIds = new Set(metricRecords.map(record => record.task_id))
  const exactStatTaskIds = new Set(
    metricStats
      .filter(stat => stat.total > 0 && !stat.loss_approximate && Number.isFinite(stat.loss))
      .map(stat => normalizeMetricTaskId(stat.task_id)),
  )
  if (!metricRecords.length || [...metricTaskIds].some(taskId => !exactStatTaskIds.has(taskId)))
    return null

  const taskMap = new Map<number, PingTaskInfo>()
  for (const stat of metricStats) {
    const task = normalizeMetricTask(stat)
    taskMap.set(task.id, task)
  }

  for (const series of normalizeMetricSeriesList(
    metricsResult.status === 'fulfilled' ? metricsResult.value.series : [],
  ).filter(isPingMetric)) {
    const taskId = normalizeMetricTaskId(pingTaskId(series))
    if (!taskId || taskMap.has(taskId))
      continue

    taskMap.set(taskId, {
      id: taskId,
      name: pingTaskName(series) || `Task ${taskId}`,
      interval: series.interval_seconds ?? 0,
      loss: 0,
    })
  }

  return {
    records: metricRecords,
    tasks: orderPingTasksByBackend(
      [...taskMap.values()],
      backendTasksResult.status === 'fulfilled' ? backendTasksResult.value : [],
    ),
  }
}

// ==================== 数据获取 ====================

async function fetchRecords() {
  const sequence = ++fetchRecordsSequence
  const requestedUuid = props.uuid
  if (!requestedUuid)
    return

  if (isCustomRange.value && !customRange.value) {
    remoteData.value = []
    tasks.value = []
    error.value = customRangeError.value || '请选择有效的自定义时间范围'
    legacyCustomRangeFallback.value = false
    loading.value = false
    return
  }

  appliedCustomRange.value = isCustomRange.value ? customRange.value : null

  loading.value = true
  error.value = null

  try {
    const metricPayload = await loadMetricPingPayload(requestedUuid).catch(() => null)
    if (sequence !== fetchRecordsSequence || requestedUuid !== props.uuid)
      return

    legacyCustomRangeFallback.value = !metricPayload && isCustomRange.value
    const range = appliedCustomRange.value
    const legacyHours = range
      ? Math.min(
          maxPingRecordPreserveTime.value,
          Math.max(range.hours, Math.ceil(dayjs().diff(range.start, 'hour', true))),
        )
      : selectedHours.value
    const result = metricPayload ?? await loadPingRecordsWithTasks(legacyHours, PING_RECORD_MAX_COUNT, requestedUuid)
    if (sequence !== fetchRecordsSequence || requestedUuid !== props.uuid)
      return

    const records = result.records
    records.sort((a, b) => dayjs(a.time).valueOf() - dayjs(b.time).valueOf())

    remoteData.value = records
    tasks.value = result.tasks

    if (tasks.value.length > 0 && selectedTaskIds.value.length === 0) {
      selectedTaskIds.value = tasks.value.map(t => t.id)
    }
  }
  catch (err) {
    if (sequence !== fetchRecordsSequence || requestedUuid !== props.uuid)
      return

    error.value = err instanceof Error ? err.message : '获取数据失败'
    legacyCustomRangeFallback.value = false
    remoteData.value = []
    tasks.value = []
  }
  finally {
    if (sequence === fetchRecordsSequence)
      loading.value = false
  }
}

// ==================== 数据处理 ====================

const mergedData = computed(() => {
  const data = remoteData.value
  if (!data.length)
    return []

  const taskList = tasks.value

  const taskIntervals = taskList
    .map(t => t.interval)
    .filter((v): v is number => typeof v === 'number' && v > 0)

  const fallbackIntervalSec = taskIntervals.length ? Math.min(...taskIntervals) : 60
  const toleranceMs = Math.min(
    6000,
    Math.max(800, Math.floor(fallbackIntervalSec * 1000 * 0.25)),
  )

  const grouped: Map<number, Record<string, unknown>> = new Map()
  const anchors: number[] = []

  for (const rec of data) {
    const ts = dayjs(rec.time).valueOf()
    let anchor: number | null = null

    for (let index = anchors.length - 1; index >= 0; index--) {
      const a = anchors[index]
      if (a === undefined || ts - a > toleranceMs)
        break
      if (Math.abs(a - ts) <= toleranceMs) {
        anchor = a
        break
      }
    }

    const useTs = anchor ?? ts
    if (!grouped.has(useTs)) {
      grouped.set(useTs, { time: dayjs(useTs).toISOString() })
      if (anchor === null) {
        anchors.push(useTs)
      }
    }

    const group = grouped.get(useTs)!
    group[rec.task_id] = rec.value < 0 ? null : rec.value
  }

  const merged = Array.from(grouped.values()).sort(
    (a, b) => dayjs(a.time as string).valueOf() - dayjs(b.time as string).valueOf(),
  )

  const range = appliedCustomRange.value
  if (isCustomRange.value && range) {
    const fromTs = range.start.valueOf()
    const toTs = range.end.valueOf()
    return merged.filter((item) => {
      const timestamp = dayjs(item.time as string).valueOf()
      return timestamp >= fromTs && timestamp <= toTs
    })
  }

  const hours = selectedHours.value
  const lastItem = merged.at(-1)
  const lastTs = lastItem ? dayjs(lastItem.time as string).valueOf() : dayjs().valueOf()
  const fromTs = lastTs - hours * 3600_000

  let startIdx = 0
  for (let i = 0; i < merged.length; i++) {
    const item = merged[i]
    if (!item)
      continue
    const ts = dayjs(item.time as string).valueOf()
    if (ts >= fromTs) {
      startIdx = Math.max(0, i - 1)
      break
    }
  }

  return merged.slice(startIdx)
})

const chartData = computed(() => {
  let data = mergedData.value
  const selectedKeys = selectedTaskIds.value.map(String)

  if (selectedKeys.length === 0)
    return []

  if (cutPeak.value) {
    data = cutPeakValues(data, selectedKeys)
  }

  if (selectedKeys.length > 0 && data.length > 0) {
    data = interpolateNullsLinear(data, selectedKeys, {
      maxGapMultiplier: 6,
      minCapMs: 2 * 60_000,
      maxCapMs: 30 * 60_000,
    })
  }

  return data
})

// ==================== 工具函数 ====================

function formatTime(time: string, showDate: boolean): string {
  const date = dayjs(time)
  if (showDate) {
    return date.format('M/D HH:mm')
  }
  return date.format('HH:mm')
}

function formatTimeForTooltip(time: string, hours: number): string {
  const date = dayjs(time)
  if (hours < 24) {
    return date.format('HH:mm:ss')
  }
  return date.format('MM/DD HH:mm')
}

const showDateInAxis = computed(() => selectedHours.value >= 24)

// ==================== 任务选择 ====================

// 获取任务颜色（根据任务在完整列表中的索引）
function getTaskColor(taskId: number): string {
  const taskIndex = tasks.value.findIndex(t => t.id === taskId)
  const safeIndex = Math.max(0, taskIndex % chartColors.length)
  return chartColors[safeIndex]!
}

// 最新值统计（从服务端 tasks 获取，保持颜色顺序）
const latestValues = computed(() => {
  if (!tasks.value.length)
    return []

  const latestMap = new Map<number, number | null>()
  for (const task of tasks.value) {
    for (let i = remoteData.value.length - 1; i >= 0; i--) {
      const rec = remoteData.value[i]
      if (rec && rec.task_id === task.id && rec.value >= 0) {
        latestMap.set(task.id, rec.value)
        break
      }
    }
  }

  return tasks.value.map((task, idx) => {
    const safeIdx = Math.max(0, idx % chartColors.length)
    return {
      ...task,
      latestValue: latestMap.get(task.id) ?? null,
      color: chartColors[safeIdx]!,
    }
  })
})

const selectedTasks = computed(() => {
  return tasks.value.filter(t => selectedTaskIds.value.includes(t.id))
})

// 切换任务选中状态
function toggleTask(taskId: number) {
  if (selectedTaskIds.value.includes(taskId)) {
    selectedTaskIds.value = selectedTaskIds.value.filter(id => id !== taskId)
  }
  else {
    selectedTaskIds.value = [...selectedTaskIds.value, taskId]
  }
}

function showAllTasks() {
  selectedTaskIds.value = tasks.value.map(t => t.id)
}

function hideAllTasks() {
  selectedTaskIds.value = []
}

// ==================== 图表配置 ====================

// 通用 Tooltip 配置
const baseTooltipConfig = computed(() => ({
  trigger: 'axis' as const,
  confine: false,
  backgroundColor: chartThemeColors.value.tooltipBg,
  borderColor: 'transparent',
  borderWidth: 0,
  borderRadius: 6,
  textStyle: {
    color: chartThemeColors.value.text,
    fontSize: 12,
    lineHeight: 20,
  },
  extraCssText: `backdrop-filter: blur(5px);z-index:9;box-shadow:0 0 0 1px ${chartThemeColors.value.tooltipShadow}, 0 0 16px ${chartThemeColors.value.tooltipShadow}`,
  axisPointer: {
    type: 'cross' as const,
    crossStyle: {
      color: chartThemeColors.value.textTertiary,
    },
    lineStyle: {
      color: chartThemeColors.value.crosshairColor,
      width: 1,
      type: 'dashed' as const,
    },
    shadowStyle: {
      color: chartThemeColors.value.crosshairColor,
    },
  },
}))

const pingChartOption = computed(() => {
  const taskList = selectedTasks.value
  const data = chartData.value
  const hours = selectedHours.value

  // 构建 series，确保颜色与卡片一致
  const series = taskList.map((task, index) => {
    const color = getTaskColor(task.id)
    const lineType = appStore.colorVisionFriendly
      ? (ACCESSIBLE_LINE_TYPES[index % ACCESSIBLE_LINE_TYPES.length] ?? 'solid')
      : 'solid'
    return {
      name: task.name,
      type: 'line' as const,
      data: data.map(d => d[task.id] as number | null ?? null),
      smooth: cutPeak.value ? 0.6 : 0.1,
      showSymbol: false,
      connectNulls: false,
      lineStyle: { width: 1.5, color, cap: 'round' as const, type: lineType },
      itemStyle: { color }, // 确保 symbol 颜色一致
    }
  })

  // 颜色映射表（用于 Tooltip）
  const colorMap = new Map<number, string>()
  tasks.value.forEach((task, idx) => {
    const safeIdx = Math.max(0, idx % chartColors.length)
    colorMap.set(task.id, chartColors[safeIdx]!)
  })

  return {
    animation: false,
    // 全局颜色设置（用于图例等）
    color: tasks.value.map((_, idx) => {
      const safeIdx = Math.max(0, idx % chartColors.length)
      return chartColors[safeIdx]!
    }),
    tooltip: {
      ...baseTooltipConfig.value,
      formatter: (params: unknown) => {
        const p = params as Array<{ seriesName: string, value: number | null, dataIndex: number }>
        if (!p.length)
          return ''
        const firstParam = p[0]
        if (!firstParam)
          return ''
        const rowData = data[firstParam.dataIndex]
        if (!rowData)
          return ''

        const time = rowData.time as string
        const timeStr = formatTimeForTooltip(time, hours)
        let html = `<div style="font-weight:600;margin-bottom:6px;color:${chartThemeColors.value.textSecondary}">${timeStr}</div>`
        html += '<div style="display:flex;flex-direction:column;gap:4px">'

        // 按延迟值排序显示
        const sortedParams = [...p].sort((a, b) => (a.value ?? 0) - (b.value ?? 0))

        for (const item of sortedParams) {
          if (item.value !== null && item.value !== undefined) {
            // 通过任务名找到对应的任务ID，再获取颜色
            const task = tasks.value.find(t => t.name === item.seriesName)
            const color = task ? colorMap.get(task.id) || chartColors[0] : chartColors[0]
            const colorDot = `<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:${color};margin-right:8px;flex-shrink:0"></span>`
            html += `<div style="display:flex;align-items:center">${colorDot}<span style="flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${item.seriesName}</span><span style="margin-left:auto;font-weight:600;margin-left:16px;font-variant-numeric:tabular-nums">${Math.round(item.value)} ms</span></div>`
          }
        }
        html += '</div>'
        return html
      },
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      itemWidth: 12,
      itemHeight: 12,
      itemGap: 16,
      icon: 'roundRect',
      textStyle: { fontSize: 11, color: chartThemeColors.value.textSecondary },
      data: taskList.map(t => t.name),
    },
    grid: chartMargin,
    xAxis: {
      type: 'category',
      data: data.map(d => formatTime(d.time as string, showDateInAxis.value)),
      axisLabel: {
        fontSize: 11,
        color: chartThemeColors.value.textSecondary,
        margin: 12,
      },
      axisLine: {
        show: true,
        lineStyle: { color: chartThemeColors.value.borderColor, width: 1 },
      },
      axisTick: { show: false },
      boundaryGap: false,
    },
    yAxis: {
      type: 'value',
      name: '延迟 (ms)',
      nameTextStyle: { color: chartThemeColors.value.textSecondary },
      axisLabel: { fontSize: 11, color: chartThemeColors.value.textSecondary, formatter: '{value}' },
      axisLine: { show: false },
      axisTick: { show: false },
      splitLine: {
        lineStyle: {
          color: chartThemeColors.value.splitLineColor,
          type: 'dashed' as const,
        },
      },
    },
    series,
  }
})

// ==================== 生命周期 ====================

watch(selectedView, () => {
  selectedTaskIds.value = []
  if (isCustomRange.value)
    ensureDefaultCustomRange()
  fetchRecords()
})

watch(() => props.uuid, () => {
  remoteData.value = []
  tasks.value = []
  selectedTaskIds.value = []
  activeTaskTooltipId.value = null
  smoothInfoTooltipOpen.value = false
  fetchRecords()
})

onMounted(() => {
  syncTouchTooltipMode()
  coarsePointerMediaQuery = window.matchMedia('(pointer: coarse)')
  coarsePointerMediaQuery.addEventListener('change', syncTouchTooltipMode)

  const firstView = availableViews.value[0]
  if (firstView && !selectedView.value) {
    selectedView.value = firstView.label
  }
  fetchRecords()
})

onBeforeUnmount(() => {
  coarsePointerMediaQuery?.removeEventListener('change', syncTouchTooltipMode)
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- 时间选择器 -->
    <div class="flex flex-col gap-2">
      <Tabs v-model="selectedView" class="w-full items-center">
        <div class="min-w-0 flex-1 overflow-x-auto rounded-sm pointer-events-auto">
          <TabsList class="w-max h-8 bg-background/50 backdrop-blur-xl rounded-md">
            <TabsTrigger
              v-for="view in availableViews" :key="view.label" :value="view.label"
              class="h-6.5 flex-none shrink-0 text-xs border-none data-[state=active]:text-green-600 shadow-none rounded-sm"
            >
              {{ view.label }}
            </TabsTrigger>
          </TabsList>
        </div>
        <div class="md:flex-1" />
        <div class="flex gap-2 items-center">
          <Button
            variant="ghost" size="xs" class="h-7 rounded-sm bg-background/50 hover:bg-background border-none"
            :class="selectedTaskIds.length === tasks.length ? 'shadow-[0_0_0_2px] shadow-green-600/10 text-green-600' : ''"
            @click="showAllTasks"
          >
            全选
          </Button>
          <Button
            variant="ghost" size="xs" class="h-7 rounded-sm bg-background/50 hover:bg-background border-none"
            :class="!selectedTaskIds.length && 'shadow-[0_0_0_2px] shadow-green-600/10 text-green-600'"
            @click="hideAllTasks"
          >
            全不选
          </Button>
        </div>
      </Tabs>

      <div v-if="isCustomRange" class="flex w-full flex-col items-center gap-2 sm:flex-row sm:justify-center">
        <div class="grid w-full gap-2 sm:w-auto sm:grid-cols-[minmax(0,13rem)_minmax(0,13rem)_auto]">
          <Input
            v-model="customStartInput"
            type="datetime-local"
            aria-label="延迟图开始时间"
            class="h-8 bg-background/50 text-xs"
          />
          <Input
            v-model="customEndInput"
            type="datetime-local"
            aria-label="延迟图结束时间"
            class="h-8 bg-background/50 text-xs"
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            :disabled="!customRange"
            class="h-8 text-xs"
            @click="fetchRecords"
          >
            应用
          </Button>
        </div>
        <div v-if="customRangeError" class="text-[11px] text-orange-500">
          {{ customRangeError }}
        </div>
        <div v-else-if="legacyCustomRangeFallback" class="text-[11px] text-muted-foreground">
          旧接口按可用保留时长回溯，再裁剪到所选区间
        </div>
      </div>
    </div>

    <!-- 内容区域 -->
    <Spinner :show="loading" content-class="flex flex-col gap-4">
      <div v-if="error" class="text-red-500 py-8 text-center">
        {{ error }}
      </div>
      <div v-else-if="tasks.length === 0 && !loading" class="py-8">
        <Empty description="暂无延迟数据" />
      </div>

      <template v-else>
        <!-- 最新值统计卡片（可点击切换选中状态） -->
        <div
          v-if="latestValues.length > 0" class="gap-3 grid"
          style="grid-template-columns: repeat(auto-fit, minmax(180px, 1fr))"
        >
          <div
            v-for="task in latestValues" :key="task.id"
            :data-ping-task-id="task.id"
            class="p-2 rounded-md bg-background/50 hover:bg-background hover:shadow-[0_0_0_2px] hover:shadow-primary/10 flex gap-3 cursor-pointer select-none transition-all items-center"
            :class="[!selectedTaskIds.includes(task.id) && 'opacity-30']"
            :onmouseover="(e: MouseEvent) => ((e.currentTarget as HTMLElement).style.borderColor = task.color)"
            :onmouseout="(e: MouseEvent) => ((e.currentTarget as HTMLElement).style.borderColor = '')"
            @click="toggleTask(task.id)"
          >
            <div class="flex-1 min-w-0">
              <TooltipProvider>
                <div class="flex gap-2 items-center">
                  <div class="rounded h-4 w-1" :style="{ backgroundColor: task.color }" />
                  <span class="text-sm font-semibold truncate">{{ task.name }}</span>
                  <div class="flex-1" />
                  <Tooltip
                    :open="isTouchTooltipMode ? activeTaskTooltipId === task.id : undefined"
                    @update:open="(open) => setTaskTooltipOpen(task.id, open)"
                  >
                    <TooltipTrigger as-child>
                      <Button variant="ghost" size="icon-xs" class="text-slate-500" @click.stop="toggleTaskTooltip(task.id)">
                        <Icon icon="carbon:information" :width="14" :height="14" />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent class="!rounded p-3">
                      <div class="text-xs gap-x-4 gap-y-1.5 grid grid-cols-4">
                        <template v-if="task.min !== undefined">
                          <span class="text-muted-foreground">最小</span>
                          <span class="font-medium">{{ Math.round(task.min) }} ms</span>
                        </template>
                        <template v-if="task.max !== undefined">
                          <span class="text-muted-foreground">最大</span>
                          <span class="font-medium">{{ Math.round(task.max) }} ms</span>
                        </template>
                        <template v-if="task.avg !== undefined">
                          <span class="text-muted-foreground">平均</span>
                          <span class="font-medium">{{ Math.round(task.avg) }} ms</span>
                        </template>
                        <template v-if="task.latest !== undefined">
                          <span class="text-muted-foreground">最新</span>
                          <span class="font-medium">{{ Math.round(task.latest) }} ms</span>
                        </template>
                        <template v-if="task.p50 !== undefined">
                          <span class="text-muted-foreground">P50</span>
                          <span class="font-medium">{{ Math.round(task.p50) }} ms</span>
                        </template>
                        <template v-if="task.p99 !== undefined">
                          <span class="text-muted-foreground">P99</span>
                          <span class="font-medium">{{ Math.round(task.p99) }} ms</span>
                        </template>
                        <template v-if="task.p99_p50_ratio !== undefined">
                          <span class="text-muted-foreground">波动率</span>
                          <span class="font-medium">{{ task.p99_p50_ratio.toFixed(2) }}</span>
                        </template>
                        <template v-if="task.interval !== undefined">
                          <span class="text-muted-foreground">间隔</span>
                          <span class="font-medium">{{ task.interval }}s</span>
                        </template>
                        <template v-if="task.type">
                          <span class="text-muted-foreground">类型</span>
                          <span class="font-medium">{{ task.type.toUpperCase() }}</span>
                        </template>
                        <template v-if="task.stddev !== undefined">
                          <span class="text-muted-foreground">标准差</span>
                          <span class="font-medium">{{ task.stddev.toFixed(1) }}</span>
                        </template>
                        <template v-if="task.total !== undefined">
                          <span class="text-muted-foreground">总数</span>
                          <span class="font-medium">{{ task.total }}</span>
                        </template>
                        <template v-if="task.valid !== undefined">
                          <span class="text-muted-foreground">有效</span>
                          <span class="font-medium">{{ task.valid }}</span>
                        </template>
                      </div>
                    </TooltipContent>
                  </Tooltip>
                </div>
              </TooltipProvider>
              <div class="text-xs mt-1 flex gap-1.5 items-center text-muted-foreground">
                <span class="font-medium" title="平均延迟">
                  {{ task.avg !== undefined ? `${Math.round(task.avg)}ms` : '-' }}
                </span>
                <span class="opacity-60">·</span>
                <span title="丢包率">{{ task.loss.toFixed(2) }}%{{ task.loss_approximate ? '≈' : '' }}</span>
                <template v-if="task.p99_p50_ratio !== undefined">
                  <span class="opacity-60">·</span>
                  <span title="波动率">{{ task.p99_p50_ratio.toFixed(2) }}</span>
                </template>
              </div>
            </div>
          </div>
        </div>

        <!-- 平滑峰值开关 -->
        <div class="flex flex-wrap gap-4 items-center py-2 justify-between">
          <TooltipProvider>
            <div class="flex gap-2 items-center">
              <Button
                variant="ghost" size="xs" class="h-7 rounded-sm bg-background/50 hover:bg-background border-none"
                :class="cutPeak && 'shadow-[0_0_0_2px] shadow-green-600/10 text-green-600'" @click="cutPeak = !cutPeak"
              >
                平滑峰值
              </Button>
              <Tooltip
                :open="isTouchTooltipMode ? smoothInfoTooltipOpen : undefined"
                @update:open="(open) => smoothInfoTooltipOpen = open"
              >
                <TooltipTrigger as-child>
                  <Button variant="ghost" size="icon-xs" class="text-slate-500" @click.stop="toggleSmoothInfoTooltip">
                    <Icon icon="carbon:information" :width="14" :height="14" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  <span>使用 EWMA 算法平滑数据并过滤突变值</span>
                </TooltipContent>
              </Tooltip>
            </div>
          </TooltipProvider>
        </div>

        <!-- 图表 -->
        <div class="h-80 bg-background/50 p-4 rounded-md">
          <VChart :option="pingChartOption" autoresize />
        </div>
      </template>
    </Spinner>
  </div>
</template>
