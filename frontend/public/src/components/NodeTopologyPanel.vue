<script setup lang="ts">
import type { NodeProviderMetadata } from '@/composables/useNodeProviderMetadata'
import type { NodeData } from '@/stores/nodes'
import { Icon } from '@iconify/vue'
import { computed, ref } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardX } from '@/components/ui/card-x'
import { useNodeProviderMetadata } from '@/composables/useNodeProviderMetadata'
import { useAppStore } from '@/stores/app'
import { getRegionDisplayName } from '@/utils/regionHelper'
import { parseTags } from '@/utils/tagHelper'

interface TopologyNodeView {
  node: NodeData
  asn: string
  org: string
  provider: string
  upstream?: NodeData
}

interface EdgeView {
  from: NodeData
  to: NodeData
  source: 'tag' | 'asn'
}

interface AsnGroup {
  key: string
  asn: string
  org: string
  provider: string
  nodes: TopologyNodeView[]
  online: number
  offline: number
}

interface RootCauseGroup {
  key: string
  title: string
  description: string
  icon: string
  severity: 'critical' | 'warning' | 'info'
  affectedNodes: NodeData[]
  upstream?: NodeData
}

const props = defineProps<{
  nodes: NodeData[]
}>()
const NORMALIZE_SPACE_REGEX = /\s+/g
const ASN_CODE_REGEX = /AS\d+/i
const UPSTREAM_PREFIXES = new Set(['upstream', 'parent', '上游', '父节点'])
const UPSTREAM_SEPARATORS = [':', '=', '：'] as const

const appStore = useAppStore()
const activeMode = ref<'asn' | 'tags' | 'rootcause'>('asn')

const { metadataByUuid } = useNodeProviderMetadata({
  nodes: () => props.nodes,
  customAliases: () => appStore.providerAliases,
  enabled: () => appStore.privateFeaturesAllowed,
  allowGeoLookup: () => appStore.privateFeaturesAllowed,
  geoPermission: 'nodeTopology',
})

function normalizeRef(value: string): string {
  return value.trim().toLowerCase().replace(NORMALIZE_SPACE_REGEX, ' ')
}

function stripAsn(value: string | undefined): string {
  const match = value?.match(ASN_CODE_REGEX)
  return match ? match[0].toUpperCase() : ''
}

function metadataOf(node: NodeData): NodeProviderMetadata | null {
  return metadataByUuid.value[node.uuid] ?? null
}

function getNodeAsn(node: NodeData): string {
  return stripAsn(metadataOf(node)?.geo?.asn)
}

function getNodeOrg(node: NodeData): string {
  return metadataOf(node)?.geo?.org?.trim() || metadataOf(node)?.provider?.network?.name || '未知网络'
}

function getNodeProvider(node: NodeData): string {
  return metadataOf(node)?.provider?.displayName || '未知厂商'
}

function parseUpstreamRef(text: string): string {
  const trimmed = text.trim()
  const lower = trimmed.toLowerCase()

  for (const separator of UPSTREAM_SEPARATORS) {
    const separatorIndex = lower.indexOf(separator)
    if (separatorIndex <= 0)
      continue

    const key = lower.slice(0, separatorIndex).trim()
    if (!UPSTREAM_PREFIXES.has(key))
      continue

    return trimmed.slice(separatorIndex + separator.length).trim()
  }

  return ''
}

function getUpstreamRefs(node: NodeData): string[] {
  return parseTags(node.tags)
    .map(tag => parseUpstreamRef(tag.text))
    .filter(Boolean)
}

const nodeLookup = computed(() => {
  const map = new Map<string, NodeData>()
  for (const node of props.nodes) {
    map.set(normalizeRef(node.uuid), node)
    map.set(normalizeRef(node.name), node)
    if (node.remark)
      map.set(normalizeRef(node.remark), node)
    if (node.public_remark)
      map.set(normalizeRef(node.public_remark), node)
  }
  return map
})

function resolveUpstream(refText: string): NodeData | undefined {
  return nodeLookup.value.get(normalizeRef(refText))
}

const topologyNodes = computed<TopologyNodeView[]>(() => props.nodes.map((node) => {
  const upstream = getUpstreamRefs(node)
    .map(resolveUpstream)
    .find((candidate): candidate is NodeData => Boolean(candidate && candidate.uuid !== node.uuid))

  return {
    node,
    asn: getNodeAsn(node),
    org: getNodeOrg(node),
    provider: getNodeProvider(node),
    upstream,
  }
}))

const topologyNodeByUuid = computed(() => new Map(topologyNodes.value.map(item => [item.node.uuid, item])))
const nearestOfflineUpstreamByUuid = computed(() => {
  const resolved = new Map<string, NodeData | undefined>()
  const visiting = new Set<string>()

  function resolveNearestOfflineUpstream(uuid: string): NodeData | undefined {
    if (resolved.has(uuid))
      return resolved.get(uuid)
    if (visiting.has(uuid)) {
      resolved.set(uuid, undefined)
      return undefined
    }

    visiting.add(uuid)
    const upstream = topologyNodeByUuid.value.get(uuid)?.upstream
    const result = upstream
      ? upstream.online
        ? resolveNearestOfflineUpstream(upstream.uuid)
        : upstream
      : undefined
    visiting.delete(uuid)
    resolved.set(uuid, result)
    return result
  }

  for (const item of topologyNodes.value)
    resolveNearestOfflineUpstream(item.node.uuid)

  return resolved
})

const tagEdges = computed<EdgeView[]>(() => topologyNodes.value
  .filter(item => item.upstream)
  .map(item => ({ from: item.upstream!, to: item.node, source: 'tag' as const })))

const rootTagNodes = computed(() => topologyNodes.value.filter(item => !item.upstream))
const downstreamByUuid = computed(() => {
  const map = new Map<string, TopologyNodeView[]>()
  for (const item of topologyNodes.value) {
    if (!item.upstream)
      continue
    const downstream = map.get(item.upstream.uuid) ?? []
    downstream.push(item)
    map.set(item.upstream.uuid, downstream)
  }
  return map
})

const asnGroups = computed<AsnGroup[]>(() => {
  const map = new Map<string, AsnGroup>()
  for (const item of topologyNodes.value) {
    const key = item.asn || `provider:${item.provider}`
    const group = map.get(key) ?? {
      key,
      asn: item.asn || 'Unknown ASN',
      org: item.org,
      provider: item.provider,
      nodes: [],
      online: 0,
      offline: 0,
    }
    group.nodes.push(item)
    if (item.node.online)
      group.online += 1
    else
      group.offline += 1
    map.set(key, group)
  }

  return Array.from(map.values()).sort((a, b) => b.nodes.length - a.nodes.length)
})

const totalOnline = computed(() => props.nodes.filter(node => node.online).length)
const totalOffline = computed(() => props.nodes.length - totalOnline.value)
const asnEdges = computed(() => asnGroups.value.reduce((count, group) => count + group.nodes.length, 0))

function getOfflineUpstream(node: NodeData): NodeData | undefined {
  return nearestOfflineUpstreamByUuid.value.get(node.uuid)
}

const rootCauseGroups = computed<RootCauseGroup[]>(() => {
  const groups: RootCauseGroup[] = []
  const groupedNodeUuids = new Set<string>()
  const byUpstream = new Map<string, { upstream: NodeData, nodes: NodeData[] }>()

  for (const node of props.nodes.filter(item => !item.online)) {
    const upstream = getOfflineUpstream(node)
    if (!upstream)
      continue

    const group = byUpstream.get(upstream.uuid) ?? { upstream, nodes: [] }
    group.nodes.push(node)
    byUpstream.set(upstream.uuid, group)
    groupedNodeUuids.add(node.uuid)
  }

  for (const group of byUpstream.values()) {
    groups.push({
      key: `upstream:${group.upstream.uuid}`,
      title: `${group.upstream.name} 上游异常`,
      description: `${group.nodes.length} 台下游节点可能受该上游影响，优先检查反代/入口节点。`,
      icon: 'tabler:git-branch-deleted',
      severity: 'critical',
      affectedNodes: group.nodes,
      upstream: group.upstream,
    })
  }

  for (const asnGroup of asnGroups.value) {
    const offlineNodes = asnGroup.nodes
      .map(item => item.node)
      .filter(node => !node.online && !groupedNodeUuids.has(node.uuid))
    if (offlineNodes.length < 2)
      continue

    for (const node of offlineNodes)
      groupedNodeUuids.add(node.uuid)

    groups.push({
      key: `asn:${asnGroup.key}`,
      title: `${asnGroup.asn} 批量异常`,
      description: `${offlineNodes.length} 台同 ASN / 同网络节点离线，可能是机房或线路层面问题。`,
      icon: 'tabler:network-off',
      severity: 'warning',
      affectedNodes: offlineNodes,
    })
  }

  const standalone = props.nodes.filter(node => !node.online && !groupedNodeUuids.has(node.uuid))
  if (standalone.length > 0) {
    groups.push({
      key: 'standalone',
      title: '独立离线节点',
      description: '未发现共同上游或 ASN 聚集特征，更像单机异常。',
      icon: 'tabler:server-off',
      severity: 'info',
      affectedNodes: standalone,
    })
  }

  return groups
})

function severityClass(severity: RootCauseGroup['severity']): string {
  if (severity === 'critical')
    return 'border-red-500/30 bg-red-500/8 text-red-600 dark:text-red-400'
  if (severity === 'warning')
    return 'border-orange-500/30 bg-orange-500/8 text-orange-600 dark:text-orange-400'
  return 'border-info/25 bg-info/8 text-info'
}

function getNodeMetaLine(item: TopologyNodeView): string {
  return [getRegionDisplayName(item.node.region), item.asn, item.provider]
    .filter(Boolean)
    .join(' · ') || '未知网络'
}
</script>

<template>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
      <CardX size="small" class="border-none bg-background/50">
        <div class="text-xs text-muted-foreground">
          节点
        </div>
        <div class="mt-1 text-2xl font-bold">
          {{ props.nodes.length }}
        </div>
      </CardX>
      <CardX size="small" class="border-none bg-background/50">
        <div class="text-xs text-muted-foreground">
          在线 / 离线
        </div>
        <div class="mt-1 text-2xl font-bold">
          <span class="text-green-600">{{ totalOnline }}</span> / <span class="text-red-500">{{ totalOffline }}</span>
        </div>
      </CardX>
      <CardX size="small" class="border-none bg-background/50">
        <div class="text-xs text-muted-foreground">
          ASN / 网络
        </div>
        <div class="mt-1 text-2xl font-bold">
          {{ asnGroups.length }}
        </div>
      </CardX>
      <CardX size="small" class="border-none bg-background/50">
        <div class="text-xs text-muted-foreground">
          拓扑边
        </div>
        <div class="mt-1 text-2xl font-bold">
          {{ tagEdges.length || asnEdges }}
        </div>
      </CardX>
    </div>

    <div class="flex flex-wrap gap-2">
      <Button size="sm" variant="ghost" class="bg-background/50" :class="activeMode === 'asn' && 'text-green-600 bg-background'" @click="activeMode = 'asn'">
        <Icon icon="tabler:world-share" width="14" height="14" />
        ASN / BGP 视图
      </Button>
      <Button size="sm" variant="ghost" class="bg-background/50" :class="activeMode === 'tags' && 'text-green-600 bg-background'" @click="activeMode = 'tags'">
        <Icon icon="tabler:git-branch" width="14" height="14" />
        标签上游视图
      </Button>
      <Button size="sm" variant="ghost" class="bg-background/50" :class="activeMode === 'rootcause' && 'text-green-600 bg-background'" @click="activeMode = 'rootcause'">
        <Icon icon="tabler:alert-triangle" width="14" height="14" />
        根因分组
      </Button>
    </div>

    <CardX v-if="activeMode === 'asn'" class="border-none bg-background/50" content-class="overflow-x-auto">
      <template #header>
        <div>
          <div class="font-semibold">
            ASN / BGP 拓扑
          </div>
          <div class="text-xs text-muted-foreground">
            按 IP ASN / Org 聚合，模拟 bgp.tools 这类站点的 ASN → 节点关系。
          </div>
        </div>
      </template>
      <div class="min-w-[760px] space-y-4">
        <div class="grid grid-cols-[12rem_1fr] gap-4 items-start">
          <div class="rounded-xl border border-border/60 bg-slate-500/5 p-4 text-center">
            <Icon icon="tabler:cloud-network" width="34" height="34" class="mx-auto text-info" />
            <div class="mt-2 text-sm font-semibold">
              Internet / BGP
            </div>
            <div class="text-xs text-muted-foreground">
              公网路由入口
            </div>
          </div>
          <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            <div v-for="group in asnGroups" :key="group.key" class="rounded-xl border border-border/60 bg-background/55 p-3">
              <div class="flex items-start gap-2">
                <Icon icon="tabler:network" width="18" height="18" class="mt-0.5 text-info" />
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-semibold">
                    {{ group.asn }}
                  </div>
                  <div class="truncate text-xs text-muted-foreground" :title="group.org">
                    {{ group.org }}
                  </div>
                  <div class="truncate text-[11px] text-muted-foreground/80">
                    {{ group.provider }}
                  </div>
                </div>
                <Badge variant="outline" class="rounded-md text-[11px]">
                  {{ group.nodes.length }} 台
                </Badge>
              </div>
              <div class="mt-3 flex flex-wrap gap-1.5">
                <span
                  v-for="item in group.nodes"
                  :key="item.node.uuid"
                  class="inline-flex max-w-[9rem] items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px]"
                  :class="item.node.online ? 'bg-green-500/10 text-green-600' : 'bg-red-500/10 text-red-500'"
                  :title="getNodeMetaLine(item)"
                >
                  <span class="size-1.5 rounded-full" :class="item.node.online ? 'bg-green-600' : 'bg-red-500'" />
                  <span class="truncate">{{ item.node.name }}</span>
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </CardX>

    <CardX v-else-if="activeMode === 'tags'" class="border-none bg-background/50">
      <template #header>
        <div>
          <div class="font-semibold">
            标签上游拓扑
          </div>
          <div class="text-xs text-muted-foreground">
            在节点标签里写 <span class="font-mono">upstream:节点名</span> / <span class="font-mono">上游:节点名</span> 后自动连线。
          </div>
        </div>
      </template>
      <div v-if="tagEdges.length === 0" class="rounded-lg bg-slate-500/5 p-4 text-sm text-muted-foreground">
        还没有解析到 upstream 标签；未配置时会把所有节点视为根节点。
      </div>
      <div class="grid gap-4 lg:grid-cols-2">
        <div v-for="item in rootTagNodes" :key="item.node.uuid" class="rounded-xl border border-border/60 bg-background/55 p-3">
          <div class="flex items-center gap-2">
            <span class="size-2 rounded-full" :class="item.node.online ? 'bg-green-600' : 'bg-red-500'" />
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm font-semibold">
                {{ item.node.name }}
              </div>
              <div class="truncate text-xs text-muted-foreground">
                {{ getNodeMetaLine(item) }}
              </div>
            </div>
            <Badge variant="outline" class="rounded-md text-[11px]">
              根节点
            </Badge>
          </div>
          <div v-if="downstreamByUuid.get(item.node.uuid)?.length" class="mt-3 border-l border-border/70 pl-3">
            <div v-for="child in downstreamByUuid.get(item.node.uuid)" :key="child.node.uuid" class="mb-2 last:mb-0 rounded-lg bg-slate-500/5 p-2">
              <div class="flex items-center gap-2 text-sm">
                <Icon icon="tabler:corner-down-right" width="14" height="14" class="text-muted-foreground" />
                <span class="size-1.5 rounded-full" :class="child.node.online ? 'bg-green-600' : 'bg-red-500'" />
                <span class="truncate font-medium">{{ child.node.name }}</span>
              </div>
              <div class="ml-8 truncate text-xs text-muted-foreground">
                {{ getNodeMetaLine(child) }}
              </div>
            </div>
          </div>
        </div>
      </div>
    </CardX>

    <div v-else class="space-y-3">
      <CardX v-for="group in rootCauseGroups" :key="group.key" class="border bg-background/50" :class="severityClass(group.severity)">
        <div class="flex flex-col gap-3 md:flex-row md:items-start">
          <Icon :icon="group.icon" width="24" height="24" class="shrink-0" />
          <div class="min-w-0 flex-1">
            <div class="font-semibold">
              {{ group.title }}
            </div>
            <div class="mt-1 text-sm text-muted-foreground">
              {{ group.description }}
            </div>
            <div v-if="group.upstream" class="mt-2 text-xs text-muted-foreground">
              上游：{{ group.upstream.name }}
            </div>
            <div class="mt-3 flex flex-wrap gap-1.5">
              <Badge v-for="node in group.affectedNodes" :key="node.uuid" variant="outline" class="rounded-md bg-background/60 text-[11px]">
                {{ node.name }}
              </Badge>
            </div>
          </div>
        </div>
      </CardX>
      <CardX v-if="rootCauseGroups.length === 0" class="border-none bg-background/50">
        <div class="py-8 text-center text-sm text-muted-foreground">
          当前没有离线节点，暂未形成根因分组。
        </div>
      </CardX>
    </div>
  </div>
</template>
