import type { RouteResult } from '@/utils/rpc'
import { onScopeDispose, shallowRef } from 'vue'
import { getSharedRpc } from '@/utils/rpc'

const routeResults = shallowRef<RouteResult[]>([])
let subscribers = 0
let refreshTimer: ReturnType<typeof setInterval> | null = null
let pending: Promise<void> | null = null

function refreshRouteResults(): Promise<void> {
  if (pending)
    return pending
  pending = getSharedRpc().getRouteResults()
    .then(results => { routeResults.value = results })
    .catch(() => {})
    .finally(() => { pending = null })
  return pending
}

export function useRouteResults() {
  subscribers += 1
  if (subscribers === 1) {
    void refreshRouteResults()
    refreshTimer = setInterval(() => { void refreshRouteResults() }, 60_000)
  }
  onScopeDispose(() => {
    subscribers -= 1
    if (subscribers === 0 && refreshTimer) {
      clearInterval(refreshTimer)
      refreshTimer = null
    }
  })
  return routeResults
}
