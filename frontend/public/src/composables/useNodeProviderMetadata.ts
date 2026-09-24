import type { MaybeRefOrGetter } from 'vue'
import type { PermissionKey } from '@/services/auth.service'
import type { NodeProviderMetadata } from '@/services/provider.service'
import type { NodeData } from '@/stores/nodes'
import { markRaw, ref, toValue, watch } from 'vue'
import { CACHE_CONFIG } from '@/constants/cache'
import { SharedCache } from '@/services/cache.service'
import { buildNodeProviderMetadata, getNodeIps, getNodeProviderFingerprint, lookupNodeGeo } from '@/services/provider.service'
import { useAppStore } from '@/stores/app'

export type { NodeProviderMetadata } from '@/services/provider.service'

interface UseNodeProviderMetadataOptions {
  nodes: MaybeRefOrGetter<NodeData[]>
  customAliases: MaybeRefOrGetter<string>
  enabled?: MaybeRefOrGetter<boolean>
  allowGeoLookup?: MaybeRefOrGetter<boolean>
  geoPermission?: PermissionKey
}

interface SharedCacheEntry {
  metadata: NodeProviderMetadata
  subscribers: Set<(metadata: NodeProviderMetadata) => void>
  promise: Promise<void> | null
}

const sharedMetadataCache = new SharedCache<SharedCacheEntry>({
  maxSize: CACHE_CONFIG.providerMetadata.maxSize,
  ttl: CACHE_CONFIG.providerMetadata.ttl,
  cleanupInterval: CACHE_CONFIG.cleanup.interval,
  canEvict: entry => entry.subscribers.size === 0 && entry.promise === null,
})

function buildRawNodeProviderMetadata(...args: Parameters<typeof buildNodeProviderMetadata>): NodeProviderMetadata {
  return markRaw(buildNodeProviderMetadata(...args))
}

function notifySharedEntry(entry: SharedCacheEntry): void {
  for (const subscriber of entry.subscribers)
    subscriber(entry.metadata)
}

function releaseSharedMetadataEntry(entry: SharedCacheEntry, subscriber: (metadata: NodeProviderMetadata) => void): void {
  entry.subscribers.delete(subscriber)
  sharedMetadataCache.sweep()
}

function getSharedMetadataEntry(node: NodeData, customAliases: string, allowGeoLookup: boolean): { fingerprint: string, entry: SharedCacheEntry } {
  const fingerprint = getNodeProviderFingerprint(node, customAliases, allowGeoLookup)
  const cached = sharedMetadataCache.get(fingerprint)
  if (cached)
    return { fingerprint, entry: cached }

  const hasIps = allowGeoLookup && getNodeIps(node).length > 0
  const entry: SharedCacheEntry = {
    metadata: buildRawNodeProviderMetadata(node, customAliases, null, hasIps),
    subscribers: new Set(),
    promise: null,
  }
  sharedMetadataCache.set(fingerprint, entry)

  if (hasIps) {
    entry.promise = lookupNodeGeo(node)
      .then((geo) => {
        entry.metadata = buildRawNodeProviderMetadata(node, customAliases, geo, false)
        notifySharedEntry(entry)
      })
      .catch(() => {
        entry.metadata = buildRawNodeProviderMetadata(node, customAliases, null, false)
        notifySharedEntry(entry)
      })
      .finally(() => {
        entry.promise = null
        sharedMetadataCache.sweep()
      })
  }

  return { fingerprint, entry }
}

export function useNodeProviderMetadata(options: UseNodeProviderMetadataOptions) {
  const appStore = useAppStore()
  const metadataByUuid = ref<Record<string, NodeProviderMetadata>>({})
  const activeFingerprints = new Map<string, string>()
  let refreshSeq = 0
  let geoPermissionVerified = false

  function getNodeProviderMetadata(node: NodeData): NodeProviderMetadata | null {
    return metadataByUuid.value[node.uuid] ?? null
  }

  watch(
    () => {
      const nodes = toValue(options.nodes)
      const customAliases = toValue(options.customAliases)
      const enabled = options.enabled === undefined ? true : toValue(options.enabled)
      const allowGeoLookup = options.allowGeoLookup === undefined ? true : toValue(options.allowGeoLookup)
      return {
        allowGeoLookup,
        authStatus: appStore.authStatus,
        customAliases,
        enabled,
        fingerprint: nodes.map(node => getNodeProviderFingerprint(node, customAliases, allowGeoLookup)).join(''),
      }
    },
    async ({ allowGeoLookup, authStatus, customAliases, enabled }, _previous, onCleanup) => {
      const seq = ++refreshSeq
      const nodes = toValue(options.nodes)
      const nextMetadata: Record<string, NodeProviderMetadata> = {}
      const unsubscribers: Array<() => void> = []
      let cancelled = false
      activeFingerprints.clear()

      onCleanup(() => {
        cancelled = true
        for (const unsubscribe of unsubscribers)
          unsubscribe()
      })

      if (!enabled) {
        metadataByUuid.value = nextMetadata
        return
      }

      let effectiveAllowGeoLookup = allowGeoLookup
      if (allowGeoLookup && options.geoPermission) {
        if (authStatus !== 'authenticated')
          geoPermissionVerified = false

        if (!geoPermissionVerified) {
          const granted = await appStore.requireLoginPermission(options.geoPermission, { force: false })
          if (cancelled || seq !== refreshSeq)
            return
          geoPermissionVerified = granted
          effectiveAllowGeoLookup = granted
        }
      }

      for (const node of nodes) {
        const { fingerprint, entry } = getSharedMetadataEntry(node, customAliases, effectiveAllowGeoLookup)
        activeFingerprints.set(node.uuid, fingerprint)
        nextMetadata[node.uuid] = entry.metadata

        const subscriber = (metadata: NodeProviderMetadata) => {
          if (seq !== refreshSeq || activeFingerprints.get(node.uuid) !== fingerprint)
            return

          metadataByUuid.value = {
            ...metadataByUuid.value,
            [node.uuid]: metadata,
          }
        }
        entry.subscribers.add(subscriber)
        unsubscribers.push(() => releaseSharedMetadataEntry(entry, subscriber))
      }

      metadataByUuid.value = nextMetadata
    },
    { immediate: true },
  )

  return {
    metadataByUuid,
    getNodeProviderMetadata,
  }
}
