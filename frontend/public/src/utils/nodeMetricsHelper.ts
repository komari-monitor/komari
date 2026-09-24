import type { NodeData } from '@/stores/nodes'
import { getDaysUntilExpired } from '@/utils/tagHelper'

export interface TopNodeMetric {
  node: NodeData
  value: number
}

export interface HighLoadMetric {
  key: 'cpu' | 'memory' | 'disk'
  label: string
  percentage: number
}

export function hasTrafficLimit(node: Pick<NodeData, 'traffic_limit'>): boolean {
  return (node.traffic_limit || 0) > 0
}

export function getTrafficCounters(node: Pick<NodeData, 'net_total_up' | 'net_total_down' | 'traffic_cycle_up' | 'traffic_cycle_down' | 'traffic_cycle_ready'>): { up: number, down: number, cycle: boolean } {
  const cycle = node.traffic_cycle_ready === true
  return {
    up: cycle ? (node.traffic_cycle_up ?? 0) : (node.net_total_up ?? 0),
    down: cycle ? (node.traffic_cycle_down ?? 0) : (node.net_total_down ?? 0),
    cycle,
  }
}

export function getTrafficUsed(node: Pick<NodeData, 'net_total_up' | 'net_total_down' | 'traffic_cycle_up' | 'traffic_cycle_down' | 'traffic_cycle_ready' | 'traffic_limit_type'>): number {
  const { up, down } = getTrafficCounters(node)
  const { traffic_limit_type } = node

  switch (traffic_limit_type) {
    case 'up': return up
    case 'down': return down
    case 'min': return Math.min(up, down)
    case 'max': return Math.max(up, down)
    case 'sum':
    default: return up + down
  }
}

export function getTrafficUsedPercentage(node: Pick<NodeData, 'traffic_limit' | 'net_total_up' | 'net_total_down' | 'traffic_cycle_up' | 'traffic_cycle_down' | 'traffic_cycle_ready' | 'traffic_limit_type'>): number {
  if (!hasTrafficLimit(node))
    return 0

  return clampPercentage(getTrafficUsed(node) / node.traffic_limit * 100)
}

export function getRealtimeTotalSpeed(node: Pick<NodeData, 'net_in' | 'net_out'>): number {
  return (node.net_in || 0) + (node.net_out || 0)
}

export function getRealtimePeakSpeed(node: Pick<NodeData, 'net_in' | 'net_out'>): number {
  return Math.max(node.net_in || 0, node.net_out || 0)
}

export function getTotalTraffic(node: Pick<NodeData, 'net_total_up' | 'net_total_down' | 'traffic_cycle_up' | 'traffic_cycle_down' | 'traffic_cycle_ready'>): number {
  const { up, down } = getTrafficCounters(node)
  return up + down
}

export function getConnectionCount(node: Pick<NodeData, 'connections' | 'connections_udp'>): number {
  return (node.connections || 0) + (node.connections_udp || 0)
}

export function getMemoryPercentage(node: Pick<NodeData, 'ram' | 'mem_total'>): number {
  return clampPercentage((node.ram || 0) / (node.mem_total || 1) * 100)
}

export function getDiskPercentage(node: Pick<NodeData, 'disk' | 'disk_total'>): number {
  return clampPercentage((node.disk || 0) / (node.disk_total || 1) * 100)
}

export function getHighLoadMetrics(node: NodeData, threshold: number): HighLoadMetric[] {
  if (!node.online)
    return []

  const metrics: HighLoadMetric[] = []
  const safeThreshold = clampThreshold(threshold, 80)
  const cpu = clampPercentage(node.cpu || 0)
  const memory = getMemoryPercentage(node)
  const disk = getDiskPercentage(node)

  if (cpu >= safeThreshold)
    metrics.push({ key: 'cpu', label: 'CPU', percentage: cpu })
  if (memory >= safeThreshold)
    metrics.push({ key: 'memory', label: '内存', percentage: memory })
  if (disk >= safeThreshold)
    metrics.push({ key: 'disk', label: '硬盘', percentage: disk })

  return metrics
}

export function isHighLoadNode(node: NodeData, threshold: number): boolean {
  return getHighLoadMetrics(node, threshold).length > 0
}

export function hasValidExpiry(node: Pick<NodeData, 'expired_at'>): boolean {
  if (!node.expired_at)
    return false

  const time = Date.parse(String(node.expired_at))
  return Number.isFinite(time)
}

export function getExpiryDays(node: Pick<NodeData, 'expired_at'>): number | null {
  if (!hasValidExpiry(node))
    return null

  return getDaysUntilExpired(node.expired_at)
}

export function isExpiringNode(node: NodeData, days: number): boolean {
  if (node.price === 0)
    return false

  const expiryDays = getExpiryDays(node)
  if (expiryDays === null)
    return false

  return expiryDays <= Math.max(1, days)
}

export function isTrafficWarningNode(node: NodeData, threshold: number): boolean {
  if (!hasTrafficLimit(node))
    return false

  return getTrafficUsedPercentage(node) >= clampThreshold(threshold, 80)
}

export function getTopNodeBy(nodes: readonly NodeData[], selector: (node: NodeData) => number): TopNodeMetric | null {
  let topNode: NodeData | null = null
  let topValue = -Infinity

  for (const node of nodes) {
    const value = selector(node)
    if (!Number.isFinite(value))
      continue

    if (value > topValue) {
      topValue = value
      topNode = node
    }
  }

  if (!topNode)
    return null

  return { node: topNode, value: Math.max(0, topValue) }
}

export function clampThreshold(value: number, fallback: number): number {
  if (!Number.isFinite(value))
    return fallback

  return Math.min(Math.max(value, 1), 100)
}

function clampPercentage(value: number): number {
  if (!Number.isFinite(value))
    return 0

  return Math.min(Math.max(value, 0), 100)
}
