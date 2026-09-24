import { onScopeDispose, watch } from 'vue'
import { loadTrafficCycleUsage, resolveTrafficCycleWindow } from '@/services/trafficCycle.service'
import { useNodesStore } from '@/stores/nodes'

const REFRESH_INTERVAL_MS = 5 * 60 * 1000

export function useTrafficCycle(): void {
  const nodesStore = useNodesStore()
  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  let requestVersion = 0

  const clearTimer = () => {
    if (refreshTimer !== null) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
  }

  async function refresh() {
    const version = ++requestVersion
    const enabledNodes = nodesStore.nodes.filter(node => node.traffic_reset_day >= 1 && node.traffic_reset_day <= 31)
    if (!nodesStore.nodes.length) {
      clearTimer()
      refreshTimer = setTimeout(() => void refresh(), 5000)
      return
    }
    if (!enabledNodes.length) {
      nodesStore.clearTrafficCycleUsage()
      clearTimer()
      return
    }

    const now = new Date()
    const groups = new Map<string, { entityIds: string[], window: ReturnType<typeof resolveTrafficCycleWindow> }>()
    for (const node of enabledNodes) {
      const window = resolveTrafficCycleWindow(now, node.traffic_reset_day, node.traffic_reset_time, node.traffic_reset_timezone)
      const key = `${window.start}|${window.end}|${window.nextReset}`
      const group = groups.get(key) ?? { entityIds: [], window }
      group.entityIds.push(node.uuid)
      groups.set(key, group)
    }
    try {
      const cycleUsage = new Map<string, { up: number, down: number, start: string, nextReset: string }>()
      await Promise.all(Array.from(groups.values(), async ({ entityIds, window }) => {
        const usage = await loadTrafficCycleUsage(entityIds, window)
        for (const [uuid, value] of usage)
          cycleUsage.set(uuid, { ...value, start: window.start, nextReset: window.nextReset })
      }))
      if (version !== requestVersion)
        return
      nodesStore.applyTrafficCycleUsage(cycleUsage)
    }
    catch (error) {
      console.warn('[TrafficCycle] Failed to refresh cycle traffic; cumulative counters remain available.', error)
      if (version === requestVersion)
        nodesStore.clearTrafficCycleUsage()
    }
    finally {
      if (version === requestVersion) {
        const nextReset = Array.from(groups.values(), group => group.window.nextReset).sort()[0]
        scheduleRefresh(nextReset)
      }
    }
  }

  function scheduleRefresh(nextReset?: string) {
    clearTimer()
    const untilReset = nextReset ? Math.max(1000, Date.parse(nextReset) - Date.now() + 1000) : REFRESH_INTERVAL_MS
    refreshTimer = setTimeout(() => void refresh(), Math.min(REFRESH_INTERVAL_MS, untilReset))
  }

  watch(
    () => [
      nodesStore.nodes.map(node => `${node.uuid}:${node.traffic_reset_day}:${node.traffic_reset_time}:${node.traffic_reset_timezone}`).join(','),
    ],
    () => void refresh(),
    { immediate: true },
  )

  onScopeDispose(() => {
    requestVersion++
    clearTimer()
  })
}
