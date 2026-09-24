import type { NodeData } from '@/stores/nodes'
import type { IpGeo } from '@/utils/ipGeoHelper'
import { computed, ref, watch } from 'vue'
import { useNodesStore } from '@/stores/nodes'
import { formatCityNameZh } from '@/utils/cityNameHelper'
import { getCoordByCode, getCountryCodeFromRegion } from '@/utils/geoHelper'
import { lookupIpGeo } from '@/utils/ipGeoHelper'
import { getRegionDisplayName } from '@/utils/regionHelper'

interface UseNodeGeoClustersOptions {
  nodes?: () => NodeData[] | undefined
}

export interface RegionCluster {
  id: string
  code: string
  coord: [number, number]
  label: string
  asn?: string
  org?: string
  servers: number
  onlineServers: number
}

interface ClusterSummary {
  clusters: RegionCluster[]
  totalServers: number
  onlineServers: number
}

const CITY_SLUG_INVALID_REGEX = /[^a-z0-9]+/g
const CITY_SLUG_EDGE_REGEX = /^-+|-+$/g
const IP_GEO_LOOKUP_BATCH_SIZE = 8
const IP_GEO_RETRY_INTERVAL_MS = 10 * 60 * 1000

export function useNodeGeoClusters(options: UseNodeGeoClustersOptions = {}) {
  const nodesStore = useNodesStore()

  const displayNodes = computed(() => options.nodes?.() ?? nodesStore.visibleNodes)
  const ipGeoMap = ref(new Map<string, IpGeo>())
  const failedIpAttempts = new Map<string, number>()

  async function resolveNodeCities(nodes: NodeData[]): Promise<void> {
    const ips: string[] = []
    const seenIps = new Set<string>()
    const now = Date.now()

    for (const node of nodes) {
      const ip = node.ipv4 || node.ipv6
      if (!ip || seenIps.has(ip) || ipGeoMap.value.has(ip))
        continue

      const failedAt = failedIpAttempts.get(ip)
      if (failedAt && now - failedAt < IP_GEO_RETRY_INTERVAL_MS)
        continue

      seenIps.add(ip)
      ips.push(ip)
    }

    for (let i = 0; i < ips.length; i += IP_GEO_LOOKUP_BATCH_SIZE) {
      const batch = ips.slice(i, i + IP_GEO_LOOKUP_BATCH_SIZE)
      const results = await Promise.all(batch.map(async (ip) => {
        const geo = await lookupIpGeo(ip)
        return { ip, geo }
      }))
      const resolved = results.filter((result): result is { ip: string, geo: IpGeo } => result.geo !== null)

      for (const { ip, geo } of results) {
        if (geo)
          failedIpAttempts.delete(ip)
        else
          failedIpAttempts.set(ip, Date.now())
      }

      if (!resolved.length)
        continue

      const next = new Map(ipGeoMap.value)
      for (const { ip, geo } of resolved) {
        next.set(ip, geo)
      }
      ipGeoMap.value = next
    }
  }

  function nodeClusterInfo(node: NodeData): { id: string, code: string, coord: [number, number], label: string, asn?: string, org?: string } | null {
    const countryCode = getCountryCodeFromRegion(node.region)
    const ip = node.ipv4 || node.ipv6
    const geo = ip ? ipGeoMap.value.get(ip) : undefined

    if (geo && Number.isFinite(geo.lat) && Number.isFinite(geo.lng)) {
      const code = (geo.countryCode || countryCode || '').toUpperCase()
      const citySlug = (geo.city || `${geo.lat.toFixed(2)},${geo.lng.toFixed(2)}`)
        .toLowerCase()
        .replace(CITY_SLUG_INVALID_REGEX, '-')
        .replace(CITY_SLUG_EDGE_REGEX, '')
      const label = formatCityNameZh(geo.city) || getRegionDisplayName(node.region) || getRegionDisplayName(code) || ''
      return {
        id: `${(code || 'xx').toLowerCase()}-${citySlug || 'city'}`,
        code: code || (countryCode ?? ''),
        coord: [geo.lat, geo.lng],
        label,
        asn: geo.asn,
        org: geo.org,
      }
    }

    if (countryCode) {
      const coord = getCoordByCode(countryCode)
      if (coord)
        return { id: countryCode.toLowerCase(), code: countryCode, coord, label: getRegionDisplayName(node.region) || getRegionDisplayName(countryCode) || '' }
    }

    return null
  }

  const clusterSummary = computed<ClusterSummary>(() => {
    const clustersById = new Map<string, RegionCluster>()
    let onlineServers = 0

    for (const node of displayNodes.value) {
      if (node.online)
        onlineServers += 1

      const info = nodeClusterInfo(node)
      if (!info)
        continue

      let cluster = clustersById.get(info.id)
      if (!cluster) {
        cluster = { id: info.id, code: info.code, coord: info.coord, label: info.label, asn: info.asn, org: info.org, servers: 0, onlineServers: 0 }
        clustersById.set(info.id, cluster)
      }
      if (!cluster.asn && info.asn)
        cluster.asn = info.asn
      if (!cluster.org && info.org)
        cluster.org = info.org
      cluster.servers += 1

      if (node.online)
        cluster.onlineServers += 1
    }

    return {
      clusters: Array.from(clustersById.values()).sort((a, b) => b.servers - a.servers),
      totalServers: displayNodes.value.length,
      onlineServers,
    }
  })

  const regionClusters = computed<RegionCluster[]>(() => clusterSummary.value.clusters)
  const totalServers = computed(() => clusterSummary.value.totalServers)
  const onlineServers = computed(() => clusterSummary.value.onlineServers)
  const offlineServers = computed(() => totalServers.value - onlineServers.value)

  function clusterKey(cluster: RegionCluster) {
    return `${cluster.id}:${cluster.coord[0]},${cluster.coord[1]}:${cluster.label}:${cluster.asn ?? ''}:${cluster.org ?? ''}:${cluster.servers}:${cluster.onlineServers}`
  }

  const nodeIpSignature = computed(() => displayNodes.value
    .map(node => node.ipv4 || node.ipv6 || '')
    .filter(Boolean)
    .join('|'))

  watch(nodeIpSignature, () => {
    void resolveNodeCities(displayNodes.value)
  }, { immediate: true })

  return {
    displayNodes,
    regionClusters,
    totalServers,
    onlineServers,
    offlineServers,
    clusterKey,
  }
}
