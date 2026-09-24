<script setup lang="ts">
import type { NodeData } from '@/stores/nodes'
import { Icon } from '@iconify/vue'
import { useStorageAsync } from '@vueuse/core'
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAppStore } from '@/stores/app'
import { formatBytesPerSecondWithConfig, formatBytesWithConfig, formatUptimeWithFormat } from '@/utils/helper'
import { getDiskPercentage, getMemoryPercentage, getTrafficCounters, getTrafficUsed, getTrafficUsedPercentage, hasTrafficLimit } from '@/utils/nodeMetricsHelper'
import { isNodeMatchSearch } from '@/utils/nodeSearch'
import { formatPriceWithCycle, isFreePrice } from '@/utils/tagHelper'

interface CompareMetric {
  key: string
  label: string
  value: (node: NodeData) => string
  percentage?: (node: NodeData) => number | null
}

const props = defineProps<{
  nodes: NodeData[]
}>()

const MAX_COMPARE_NODES = 4
const appStore = useAppStore()
const searchText = ref('')
const selectedIds = useStorageAsync<string[]>('theme:node-compare:v1', [], localStorage)
const normalizedSelectedIds = computed(() => [...new Set(
  (Array.isArray(selectedIds.value) ? selectedIds.value : [])
    .filter((id): id is string => typeof id === 'string' && Boolean(id.trim())),
)].slice(0, MAX_COMPARE_NODES))

const filteredNodes = computed(() => {
  const query = searchText.value.trim()
  return query ? props.nodes.filter(node => isNodeMatchSearch(node, query)) : props.nodes
})

const selectedNodes = computed(() => normalizedSelectedIds.value
  .map(uuid => props.nodes.find(node => node.uuid === uuid))
  .filter((node): node is NodeData => Boolean(node)))

const comparisonGridStyle = computed(() => ({
  gridTemplateColumns: `7.5rem repeat(${Math.max(1, selectedNodes.value.length)}, minmax(11rem, 1fr))`,
}))

function formatBytes(bytes: number): string {
  return formatBytesWithConfig(bytes, appStore.byteDecimals)
}

function formatSpeed(bytes: number): string {
  return formatBytesPerSecondWithConfig(bytes, appStore.byteDecimals)
}

function clampPercentage(value: number): number {
  return Math.min(100, Math.max(0, Number.isFinite(value) ? value : 0))
}

const compareMetrics = computed<CompareMetric[]>(() => {
  const metrics: CompareMetric[] = [
    { key: 'status', label: '状态', value: node => node.online ? '在线' : '离线' },
    { key: 'cpuModel', label: 'CPU 型号', value: node => node.cpu_name || '-' },
    { key: 'cpuCores', label: 'vCPU', value: node => `${node.cpu_cores || 0} 核` },
    { key: 'cpu', label: 'CPU 使用率', value: node => `${(node.cpu || 0).toFixed(1)}%`, percentage: node => node.cpu || 0 },
    { key: 'load', label: '系统负载', value: node => `${(node.load || 0).toFixed(2)} / ${(node.load5 || 0).toFixed(2)} / ${(node.load15 || 0).toFixed(2)}` },
    { key: 'memory', label: '内存', value: node => `${formatBytes(node.ram || 0)} / ${formatBytes(node.mem_total || 0)}`, percentage: getMemoryPercentage },
    { key: 'disk', label: '磁盘', value: node => `${formatBytes(node.disk || 0)} / ${formatBytes(node.disk_total || 0)}`, percentage: getDiskPercentage },
    { key: 'network', label: '实时网络', value: node => `↑ ${formatSpeed(node.net_out || 0)}  ↓ ${formatSpeed(node.net_in || 0)}` },
    { key: 'traffic', label: '周期流量', value: (node) => {
      const traffic = getTrafficCounters(node)
      return `${formatBytes(traffic.up)} ↑ / ${formatBytes(traffic.down)} ↓`
    } },
    {
      key: 'trafficQuota',
      label: '流量配额',
      value: node => hasTrafficLimit(node) ? `${formatBytes(getTrafficUsed(node))} / ${formatBytes(node.traffic_limit)}` : '无限',
      percentage: node => hasTrafficLimit(node) ? getTrafficUsedPercentage(node) : null,
    },
    { key: 'uptime', label: '运行时间', value: node => formatUptimeWithFormat(node.uptime || 0, 'day') },
  ]

  if (appStore.privateFeaturesAllowed || !appStore.hidePriceWhenLoggedOut) {
    metrics.push({
      key: 'price',
      label: '价格',
      value: node => node.price > 0 || isFreePrice(node.price)
        ? formatPriceWithCycle(node.price, node.billing_cycle, node.currency, appStore.lang)
        : '-',
    })
  }
  return metrics
})

function isSelected(uuid: string): boolean {
  return normalizedSelectedIds.value.includes(uuid)
}

function toggleNode(uuid: string): void {
  if (isSelected(uuid)) {
    selectedIds.value = normalizedSelectedIds.value.filter(id => id !== uuid)
    return
  }
  if (normalizedSelectedIds.value.length >= MAX_COMPARE_NODES) {
    window.$message?.warning(`最多同时对比 ${MAX_COMPARE_NODES} 台节点。`)
    return
  }
  selectedIds.value = [...normalizedSelectedIds.value, uuid]
}

function clearSelection(): void {
  selectedIds.value = []
}

watch(() => props.nodes.map(node => node.uuid), (uuids) => {
  const available = new Set(uuids)
  const validSelected = normalizedSelectedIds.value.filter(uuid => available.has(uuid))
  if (validSelected.join('|') !== normalizedSelectedIds.value.join('|') || !Array.isArray(selectedIds.value))
    selectedIds.value = validSelected
}, { immediate: true })
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <div class="relative min-w-52 flex-1 sm:max-w-80">
        <Input v-model="searchText" placeholder="搜索节点、IP 或 CPU" class="h-8 bg-background/55 pl-8" />
        <Icon icon="tabler:search" width="14" height="14" class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
      </div>
      <span class="text-xs tabular-nums text-muted-foreground">已选 {{ selectedNodes.length }} / {{ MAX_COMPARE_NODES }}</span>
      <Button variant="ghost" size="sm" :disabled="!selectedNodes.length" @click="clearSelection">
        <Icon icon="tabler:x" width="14" height="14" />
        清空
      </Button>
    </div>

    <div class="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto">
      <button
        v-for="node in filteredNodes" :key="node.uuid"
        type="button"
        class="inline-flex h-7 items-center gap-1.5 rounded-md bg-background/45 px-2 text-xs text-muted-foreground transition-colors hover:bg-background/70 hover:text-foreground"
        :class="isSelected(node.uuid) && 'bg-background text-selection shadow-sm'"
        :aria-pressed="isSelected(node.uuid)"
        @click="toggleNode(node.uuid)"
      >
        <span class="size-1.5 rounded-full" :class="node.online ? 'bg-success' : 'bg-destructive'" />
        <Icon v-if="appStore.isFavoriteNode(node.uuid)" icon="tabler:star-filled" width="11" height="11" class="text-amber-500" />
        <span class="max-w-38 truncate">{{ node.name }}</span>
      </button>
    </div>

    <div v-if="selectedNodes.length" class="overflow-x-auto rounded-md bg-background/35 p-1">
      <div class="grid min-w-max gap-px overflow-hidden rounded-sm" :style="comparisonGridStyle">
        <div class="bg-background/65 px-3 py-2 text-xs font-semibold text-muted-foreground">
          实时快照
        </div>
        <div v-for="node in selectedNodes" :key="`header-${node.uuid}`" class="min-w-0 bg-background/65 px-3 py-2">
          <div class="flex items-center gap-1.5">
            <span class="size-2 rounded-full" :class="node.online ? 'bg-success' : 'bg-destructive'" />
            <span class="truncate text-sm font-semibold">{{ node.name }}</span>
          </div>
        </div>

        <template v-for="metric in compareMetrics" :key="metric.key">
          <div class="bg-background/45 px-3 py-2 text-xs font-medium text-muted-foreground">
            {{ metric.label }}
          </div>
          <div v-for="node in selectedNodes" :key="`${metric.key}-${node.uuid}`" class="min-w-0 bg-background/45 px-3 py-2">
            <div class="break-words text-xs tabular-nums" :title="metric.value(node)">
              {{ metric.value(node) }}
            </div>
            <div v-if="metric.percentage?.(node) != null" class="mt-1.5 h-1 overflow-hidden rounded-full bg-slate-500/10">
              <div
                class="h-full rounded-full bg-selection/70"
                :style="{ width: `${clampPercentage(metric.percentage?.(node) ?? 0)}%` }"
              />
            </div>
          </div>
        </template>
      </div>
    </div>

    <div v-else class="space-y-3 py-7 text-center text-sm text-muted-foreground">
      <p>选择 2 至 4 台节点进行横向对比</p>
      <div class="mx-auto flex max-w-3xl flex-wrap justify-center gap-1.5">
        <span
          v-for="metric in compareMetrics" :key="`preview-${metric.key}`"
          class="rounded-sm bg-background/45 px-2 py-1 text-xs"
        >
          {{ metric.label }}
        </span>
      </div>
    </div>
  </div>
</template>
