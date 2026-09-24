import type { MetricSeries } from '@/utils/rpc'
import dayjs from 'dayjs'
import timezone from 'dayjs/plugin/timezone'
import utc from 'dayjs/plugin/utc'
import { queryMetrics } from '@/services/metrics.service'

dayjs.extend(utc)
dayjs.extend(timezone)

const TRAFFIC_UP_METRIC = 'traffic.up'
const TRAFFIC_DOWN_METRIC = 'traffic.down'
const RESET_TIME_PATTERN = /^(\d{1,2}):(\d{2})$/

export interface TrafficCycleWindow {
  start: string
  end: string
  nextReset: string
}

export interface TrafficCycleUsage {
  up: number
  down: number
}

function parseResetTime(value: string): { hour: number, minute: number } {
  const match = RESET_TIME_PATTERN.exec(value.trim())
  if (!match)
    return { hour: 0, minute: 0 }

  const hour = Number(match[1])
  const minute = Number(match[2])
  if (!Number.isInteger(hour) || hour < 0 || hour > 23 || !Number.isInteger(minute) || minute < 0 || minute > 59)
    return { hour: 0, minute: 0 }
  return { hour, minute }
}

function validTimezone(value: string): string {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: value }).format()
    return value
  }
  catch {
    return 'UTC'
  }
}

function monthlyBoundary(reference: dayjs.Dayjs, monthOffset: number, resetDay: number, resetTime: string, timezoneName: string): dayjs.Dayjs {
  const localReference = reference.tz(timezoneName).add(monthOffset, 'month')
  const { hour, minute } = parseResetTime(resetTime)
  const day = Math.min(Math.max(Math.trunc(resetDay), 1), localReference.daysInMonth())
  const value = `${localReference.year()}-${String(localReference.month() + 1).padStart(2, '0')}-${String(day).padStart(2, '0')} ${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
  return dayjs.tz(value, timezoneName)
}

export function resolveTrafficCycleWindow(now: Date, resetDay: number, resetTime: string, timezoneName: string): TrafficCycleWindow {
  const zone = validTimezone(timezoneName)
  const current = dayjs(now)
  let start = monthlyBoundary(current, 0, resetDay, resetTime, zone)
  if (start.isAfter(current))
    start = monthlyBoundary(current, -1, resetDay, resetTime, zone)

  const nextReset = monthlyBoundary(start.add(1, 'month'), 0, resetDay, resetTime, zone)
  return {
    start: start.toISOString(),
    end: current.toISOString(),
    nextReset: nextReset.toISOString(),
  }
}

function sumSeries(series: MetricSeries): number {
  return (series.points ?? []).reduce((sum, point) => {
    const value = point?.value
    return typeof value === 'number' && Number.isFinite(value) && value > 0 ? sum + value : sum
  }, 0)
}

export async function loadTrafficCycleUsage(entityIds: string[], window: TrafficCycleWindow): Promise<Map<string, TrafficCycleUsage>> {
  const uniqueIds = [...new Set(entityIds.filter(Boolean))]
  const result = new Map<string, TrafficCycleUsage>(uniqueIds.map(id => [id, { up: 0, down: 0 }]))
  if (!uniqueIds.length)
    return result

  const response = await queryMetrics({
    metric_keys: [TRAFFIC_UP_METRIC, TRAFFIC_DOWN_METRIC],
    entity_ids: uniqueIds,
    start: window.start,
    end: window.end,
    aggregation: 'sum',
    max_points: 1,
    fill_empty: false,
  })

  for (const series of response.series ?? []) {
    const usage = result.get(series.entity_id)
    if (!usage)
      continue
    const value = Math.max(0, Math.round(sumSeries(series)))
    if (series.metric_key === TRAFFIC_UP_METRIC)
      usage.up += value
    else if (series.metric_key === TRAFFIC_DOWN_METRIC)
      usage.down += value
  }
  return result
}
