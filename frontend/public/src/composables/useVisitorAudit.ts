import type { VisitorAuditEventParams } from '@/utils/rpc'
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { submitVisitorAuditEvent } from '@/services/visitor-audit.service'
import { useAppStore } from '@/stores/app'
import { getVisitorSecurityProfile } from '@/utils/visitorFingerprint'

export function useVisitorAudit() {
  const appStore = useAppStore()
  const supported = computed(() => appStore.visitorAuditSupported)
  const enabled = computed(() => appStore.visitorAuditEnabled)

  async function record(event: VisitorAuditEventParams, includeSecurityProfile = false): Promise<void> {
    if (!enabled.value)
      return

    const securityProfile = includeSecurityProfile
      ? await getVisitorSecurityProfile().catch(() => null)
      : null
    await submitVisitorAuditEvent({
      ...event,
      detail: {
        ...event.detail,
        ...(securityProfile ?? {}),
        theme_version: __BUILD_VERSION__,
        color_vision_mode: appStore.colorVisionMode,
      },
    }, true)
  }

  return {
    supported,
    enabled,
    record,
  }
}

export function useVisitorPageAudit(): void {
  const route = useRoute()
  const { enabled, record } = useVisitorAudit()
  let lastRecordedRoute = ''

  watch(
    [enabled, () => route.fullPath],
    ([isEnabled]) => {
      if (!isEnabled)
        return

      const routeKey = `${String(route.name ?? '')}:${route.fullPath}`
      if (routeKey === lastRecordedRoute)
        return
      lastRecordedRoute = routeKey

      const routeName = String(route.name ?? '')
      const nodeId = routeName === 'instance-detail' ? String(route.params.id ?? '') : ''
      void record({
        event: nodeId ? 'node_open' : 'page_view',
        path: route.path,
        route: routeName,
        target: nodeId || undefined,
        detail: {
          query_keys: Object.keys(route.query).slice(0, 20),
        },
      }, true)
    },
    { immediate: true, flush: 'post' },
  )
}
