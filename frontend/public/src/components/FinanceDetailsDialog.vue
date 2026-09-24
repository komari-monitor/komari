<script setup lang="ts">
import type { NodeData } from '@/stores/nodes'
import type {
  CurrencyCode,
  ExchangeRates,
  ExchangeRateSource,
  MeteredEstimateSettings,
  MeteredTrafficMode,
} from '@/utils/financeHelper'
import { Icon } from '@iconify/vue'
import { computed, ref, watch } from 'vue'
import { AppDialog } from '@/components/ui/app-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import * as financeHelper from '@/utils/financeHelper'
import { formatPriceWithCycle, getDaysUntilExpired, getExpireStatus, isFreeNode, isFreePrice } from '@/utils/tagHelper'

type FinanceTab = 'fixed' | 'metered' | 'rates'
interface UsageSnapshot {
  up: number | null
  down: number | null
  uptime: number | null
  online: boolean
  statusUpdatedAt: string
}

const props = defineProps<{
  open: boolean
  nodes: NodeData[]
  rates: ExchangeRates
  source: ExchangeRateSource | 'loading'
  ratesUpdatedAt: number | null
  currency: CurrencyCode
  excludeFree: boolean
  now: Date
}>()

const emit = defineEmits<{
  'update:open': [open: boolean]
  'update:currency': [currency: CurrencyCode]
  'update:excludeFree': [exclude: boolean]
  'update:rate': [currency: CurrencyCode, value: number]
  'resetRates': []
}>()

const activeTab = ref<FinanceTab>('fixed')
const visibleNodes = computed(() => props.nodes.filter(node => !props.excludeFree || !isFreeNode(node)))
const snapshotTakenAt = ref(Date.now())
const usageSnapshots = ref<Map<string, UsageSnapshot>>(new Map())
const selectedNodeUuid = ref(visibleNodes.value[0]?.uuid ?? '')
const meteredSettings = ref<MeteredEstimateSettings>(financeHelper.getStoredMeteredEstimateSettings(selectedNodeUuid.value))

const displayRate = computed<number | null>(() => {
  if (props.currency === 'CNY')
    return 1
  const rate = props.rates[props.currency]
  return Number.isFinite(rate) && rate > 0 ? rate : null
})
const selectedNode = computed(() => visibleNodes.value.find(node => node.uuid === selectedNodeUuid.value) ?? null)
const selectedSnapshot = computed(() => selectedNode.value ? usageSnapshots.value.get(selectedNode.value.uuid) ?? null : null)
const snapshotUptimeAvailable = computed(() => typeof selectedSnapshot.value?.uptime === 'number')
const usageAvailable = computed(() => Boolean(
  selectedSnapshot.value?.online
  && selectedSnapshot.value.up !== null
  && selectedSnapshot.value.down !== null,
))

const sourceLabels: Record<ExchangeRateSource | 'loading', string> = {
  'loading': '载入中',
  'network': '今日网络汇率',
  'cache': '今日缓存汇率',
  'stale-cache': '历史缓存汇率',
  'default': '内置参考汇率',
}

const trafficModeLabels: Record<MeteredTrafficMode, string> = {
  sum: '上行 + 下行',
  max: '取较大值',
  up: '仅上行',
  down: '仅下行',
}

function toCNY(amount: number, currencyValue: string): number | null {
  const currency = financeHelper.normalizeCurrency(currencyValue)
  if (currency === 'CNY')
    return amount
  const rate = props.rates[currency]
  return Number.isFinite(rate) && rate > 0 ? amount / rate : null
}

function formatDisplayAmount(amountCNY: number): string {
  if (displayRate.value === null)
    return '汇率不可用'
  const formatted = financeHelper.formatFinanceAmount(amountCNY * displayRate.value, props.currency)
  return `${formatted.symbol}${formatted.value}`
}

function formatRemainingValue(node: NodeData, amountCNY: number): string {
  return isFreePrice(node.price) ? '无' : formatDisplayAmount(amountCNY)
}

function formatOptionalDisplayAmount(amountCNY: number | null): string {
  return amountCNY === null ? '不可用' : formatDisplayAmount(amountCNY)
}

function formatPricingAmount(amount: number): string {
  const formatted = financeHelper.formatFinanceAmount(amount, meteredSettings.value.currency)
  return `${formatted.symbol}${formatted.value}`
}

const fixedRows = computed(() => visibleNodes.value.map((node) => {
  const remainingCNY = financeHelper.calculateRemainingValueCNY(node, props.rates, props.now)
  const monthlyCNY = financeHelper.calculateMonthlyCostCNY(node, props.rates)
  const expireStatus = getExpireStatus(node.expired_at)
  const days = getDaysUntilExpired(node.expired_at)
  const expiryLabel = expireStatus === 'unknown'
    ? '未设置'
    : expireStatus === 'expired'
      ? '已过期'
      : expireStatus === 'long_term'
        ? '长期'
        : days === 0
          ? '今天'
          : `${days} 天`

  return { node, remainingCNY, monthlyCNY, expiryLabel }
}))

const selectedEstimate = computed(() => {
  const node = selectedNode.value
  const snapshot = selectedSnapshot.value
  if (!node || !snapshot || !usageAvailable.value)
    return null

  return financeHelper.calculateMeteredEstimate({
    ...node,
    net_total_up: snapshot.up ?? 0,
    net_total_down: snapshot.down ?? 0,
  }, meteredSettings.value)
})

const totalRemainingCNY = computed(() => fixedRows.value.reduce((sum, row) => sum + row.remainingCNY, 0))
const totalMonthlyCNY = computed(() => fixedRows.value.reduce((sum, row) => sum + row.monthlyCNY, 0))
const totalMeteredCNY = computed(() => selectedEstimate.value
  ? toCNY(selectedEstimate.value.totalCost, meteredSettings.value.currency)
  : null)

watch(visibleNodes, (nodes) => {
  if (nodes.some(node => node.uuid === selectedNodeUuid.value))
    return
  selectedNodeUuid.value = nodes[0]?.uuid ?? ''
  meteredSettings.value = financeHelper.getStoredMeteredEstimateSettings(selectedNodeUuid.value)
})

watch(() => props.open, (open) => {
  if (!open)
    return

  snapshotTakenAt.value = Date.now()
  usageSnapshots.value = new Map(props.nodes.map(node => [node.uuid, {
    up: typeof node.net_total_up === 'number' && Number.isFinite(node.net_total_up) && node.net_total_up >= 0 ? node.net_total_up : null,
    down: typeof node.net_total_down === 'number' && Number.isFinite(node.net_total_down) && node.net_total_down >= 0 ? node.net_total_down : null,
    uptime: typeof node.uptime === 'number' && Number.isFinite(node.uptime) && node.uptime >= 0 ? node.uptime : null,
    online: node.online,
    statusUpdatedAt: node.status_updated_at || node.time || '',
  }]))
}, { immediate: true })

function handleCurrencyChange(event: Event) {
  emit('update:currency', (event.target as HTMLSelectElement).value as CurrencyCode)
}

function handleRateChange(currency: CurrencyCode, rawValue: string | number) {
  const value = Number(rawValue)
  if (currency !== 'CNY' && Number.isFinite(value) && value > 0)
    emit('update:rate', currency, value)
}

function handleMeteredCurrencyChange(event: Event) {
  meteredSettings.value.currency = (event.target as HTMLSelectElement).value as CurrencyCode
  persistMeteredSettings()
}

function handleTrafficModeChange(event: Event) {
  meteredSettings.value.trafficMode = (event.target as HTMLSelectElement).value as MeteredTrafficMode
  persistMeteredSettings()
}

function handleMeteredNumberUpdate(key: 'trafficRate' | 'timeRate' | 'manualHours' | 'oneTimeFee', rawValue: string | number) {
  const value = String(rawValue).trim() === '' ? 0 : Number(rawValue)
  if (!Number.isFinite(value) || value < 0 || value > financeHelper.MAX_ESTIMATE_INPUT) {
    window.$message?.warning(`请输入 0 到 ${financeHelper.MAX_ESTIMATE_INPUT.toLocaleString()} 之间的数字。`)
    return
  }
  meteredSettings.value[key] = value
  persistMeteredSettings()
}

function handleSelectedNodeChange(event: Event) {
  selectedNodeUuid.value = (event.target as HTMLSelectElement).value
  meteredSettings.value = financeHelper.getStoredMeteredEstimateSettings(selectedNodeUuid.value)
}

function useSnapshotUptime() {
  const uptime = selectedSnapshot.value?.uptime
  if (typeof uptime !== 'number' || !Number.isFinite(uptime) || uptime < 0)
    return
  meteredSettings.value.manualHours = uptime / 3600
  persistMeteredSettings()
}

function clearMeteredSettings() {
  financeHelper.clearStoredMeteredEstimateSettings(selectedNodeUuid.value)
  meteredSettings.value = financeHelper.getStoredMeteredEstimateSettings(selectedNodeUuid.value)
  window.$message?.success('已清除当前节点的本地估算参数。')
}

function persistMeteredSettings() {
  financeHelper.setStoredMeteredEstimateSettings(selectedNodeUuid.value, meteredSettings.value)
}

function formatSnapshotTime(): string {
  return new Date(snapshotTakenAt.value).toLocaleString('zh-CN', { hour12: false })
}

function formatRateUpdatedAt(): string {
  if (!props.ratesUpdatedAt)
    return '无更新时间'
  return new Date(props.ratesUpdatedAt).toLocaleString('zh-CN', { hour12: false })
}

function formatHours(hours: number): string {
  if (hours < 24)
    return `${hours.toFixed(1)} h`
  return `${(hours / 24).toFixed(1)} d`
}

function formatTraffic(tib: number): string {
  if (tib < 0.001)
    return `${(tib * 1024).toFixed(2)} GiB`
  return `${tib.toFixed(3)} TiB`
}
</script>

<template>
  <AppDialog
    :open="open"
    title="价值与费用明细"
    description="固定账单、纯前端按量估算与汇率设置"
    @update:open="emit('update:open', $event)"
  >
    <div class="space-y-4">
      <div class="grid grid-cols-3 divide-x divide-border/60 border-y border-border/60 py-3 text-center">
        <div class="min-w-0 px-2">
          <div class="text-xs text-muted-foreground">
            剩余价值
          </div>
          <div class="mt-1 truncate text-lg font-semibold tabular-nums">
            {{ formatDisplayAmount(totalRemainingCNY) }}
          </div>
        </div>
        <div class="min-w-0 px-2">
          <div class="text-xs text-muted-foreground">
            固定月均支出
          </div>
          <div class="mt-1 truncate text-lg font-semibold tabular-nums">
            {{ formatDisplayAmount(totalMonthlyCNY) }}
          </div>
        </div>
        <div class="min-w-0 px-2">
          <div class="text-xs text-muted-foreground">
            当前节点估算
          </div>
          <div class="mt-1 truncate text-lg font-semibold tabular-nums">
            {{ formatOptionalDisplayAmount(totalMeteredCNY) }}
          </div>
        </div>
      </div>

      <Tabs v-model="activeTab" class="gap-4">
        <div class="flex flex-wrap items-center gap-2">
          <TabsList class="h-9 min-w-0 flex-1 sm:flex-none">
            <TabsTrigger value="fixed">
              <Icon icon="tabler:calendar-dollar" />固定账单
            </TabsTrigger>
            <TabsTrigger value="metered">
              <Icon icon="tabler:calculator" />按量估算
            </TabsTrigger>
            <TabsTrigger value="rates">
              <Icon icon="tabler:currency-yuan" />汇率设置
            </TabsTrigger>
          </TabsList>
          <label class="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              class="size-4 accent-primary"
              :checked="excludeFree"
              @change="emit('update:excludeFree', ($event.target as HTMLInputElement).checked)"
            >
            排除免费节点
          </label>
        </div>

        <TabsContent value="fixed" class="space-y-2">
          <div class="flex items-end justify-between gap-3">
            <div>
              <h3 class="text-sm font-semibold">
                固定账单明细
              </h3>
              <p class="text-xs text-muted-foreground">
                沿用 Komari 原生价格、周期和到期时间。
              </p>
            </div>
            <select
              :value="currency"
              aria-label="显示币种"
              class="h-8 rounded-md border border-input bg-background px-2 text-xs outline-none focus:ring-2 focus:ring-ring"
              @change="handleCurrencyChange"
            >
              <option v-for="item in financeHelper.SUPPORTED_CURRENCIES" :key="item" :value="item">
                {{ item }}
              </option>
            </select>
          </div>

          <div class="overflow-x-auto rounded-md border border-border/60">
            <table class="w-full min-w-[620px] text-left text-xs">
              <thead class="bg-muted/45 text-muted-foreground">
                <tr>
                  <th class="px-3 py-2 font-medium">
                    节点
                  </th>
                  <th class="px-3 py-2 font-medium">
                    固定费用
                  </th>
                  <th class="px-3 py-2 font-medium">
                    到期
                  </th>
                  <th class="px-3 py-2 font-medium">
                    剩余价值
                  </th>
                  <th class="px-3 py-2 text-right font-medium">
                    月均支出
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border/50">
                <tr v-for="row in fixedRows" :key="row.node.uuid" class="hover:bg-muted/25">
                  <td class="max-w-48 px-3 py-2.5">
                    <div class="truncate font-medium">
                      {{ row.node.name }}
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-3 py-2.5">
                    {{ formatPriceWithCycle(row.node.price, row.node.billing_cycle, row.node.currency) }}
                  </td>
                  <td class="whitespace-nowrap px-3 py-2.5">
                    {{ row.expiryLabel }}
                  </td>
                  <td class="whitespace-nowrap px-3 py-2.5">
                    {{ formatRemainingValue(row.node, row.remainingCNY) }}
                  </td>
                  <td class="whitespace-nowrap px-3 py-2.5 text-right font-semibold tabular-nums">
                    {{ formatDisplayAmount(row.monthlyCNY) }}
                  </td>
                </tr>
                <tr v-if="fixedRows.length === 0">
                  <td colspan="5" class="px-3 py-8 text-center text-muted-foreground">
                    暂无账单节点
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </TabsContent>

        <TabsContent value="metered" class="space-y-3">
          <div class="rounded-md border border-sky-500/20 bg-sky-500/7 px-3 py-2 text-xs text-muted-foreground">
            按量费用估算器仅在当前浏览器计算，不写入 Komari。当前探针累计流量可能因机器重启、Agent 重启或网卡变化而重置，也可能包含安装 Komari 前的流量。1 TiB = 1024⁴ bytes，本结果不是正式账单。
          </div>

          <div class="flex flex-wrap items-end gap-3">
            <label class="min-w-44 flex-1 space-y-1 text-xs">
              <span class="font-medium">估算节点</span>
              <select
                :value="selectedNodeUuid"
                aria-label="估算节点"
                class="h-9 w-full rounded-md border border-input bg-background px-2 outline-none focus:ring-2 focus:ring-ring"
                @change="handleSelectedNodeChange"
              >
                <option v-for="node in visibleNodes" :key="node.uuid" :value="node.uuid">{{ node.name }}</option>
              </select>
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">流量口径</span>
              <select
                :value="meteredSettings.trafficMode"
                class="h-9 w-full rounded-md border border-input bg-background px-2 outline-none focus:ring-2 focus:ring-ring"
                @change="handleTrafficModeChange"
              >
                <option v-for="(label, key) in trafficModeLabels" :key="key" :value="key">{{ label }}</option>
              </select>
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">计价币种</span>
              <select
                :value="meteredSettings.currency"
                class="h-9 w-full rounded-md border border-input bg-background px-2 outline-none focus:ring-2 focus:ring-ring"
                @change="handleMeteredCurrencyChange"
              >
                <option v-for="item in financeHelper.SUPPORTED_CURRENCIES" :key="item" :value="item">{{ item }}</option>
              </select>
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">显示币种</span>
              <select
                :value="currency"
                aria-label="估算显示币种"
                class="h-9 w-full rounded-md border border-input bg-background px-2 outline-none focus:ring-2 focus:ring-ring"
                @change="handleCurrencyChange"
              >
                <option v-for="item in financeHelper.SUPPORTED_CURRENCIES" :key="item" :value="item">{{ item }}</option>
              </select>
            </label>
          </div>

          <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
            <label class="space-y-1 text-xs">
              <span class="font-medium">流量单价 / TiB</span>
              <Input type="number" min="0" :max="financeHelper.MAX_ESTIMATE_INPUT" step="any" :model-value="meteredSettings.trafficRate" class="h-9" @update:model-value="handleMeteredNumberUpdate('trafficRate', $event)" />
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">小时单价 / h</span>
              <Input type="number" min="0" :max="financeHelper.MAX_ESTIMATE_INPUT" step="any" :model-value="meteredSettings.timeRate" class="h-9" @update:model-value="handleMeteredNumberUpdate('timeRate', $event)" />
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">手工计费小时</span>
              <Input type="number" min="0" :max="financeHelper.MAX_ESTIMATE_INPUT" step="any" :model-value="meteredSettings.manualHours" class="h-9" @update:model-value="handleMeteredNumberUpdate('manualHours', $event)" />
            </label>
            <label class="space-y-1 text-xs">
              <span class="font-medium">本次附加费用</span>
              <Input type="number" min="0" :max="financeHelper.MAX_ESTIMATE_INPUT" step="any" :model-value="meteredSettings.oneTimeFee" class="h-9" @update:model-value="handleMeteredNumberUpdate('oneTimeFee', $event)" />
            </label>
          </div>

          <div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span>快照：{{ formatSnapshotTime() }}</span>
            <span v-if="selectedSnapshot?.statusUpdatedAt">· 探针上报：{{ selectedSnapshot.statusUpdatedAt }}</span>
            <Button type="button" size="sm" variant="outline" class="ml-auto h-8 gap-1.5" :disabled="!snapshotUptimeAvailable" @click="useSnapshotUptime">
              <Icon icon="tabler:clock-down" width="14" height="14" />带入当前 uptime
            </Button>
            <Button type="button" size="sm" variant="ghost" class="h-8 gap-1.5" @click="clearMeteredSettings">
              <Icon icon="tabler:trash" width="14" height="14" />清除本地配置
            </Button>
          </div>

          <div v-if="!usageAvailable" class="rounded-md border border-amber-500/25 bg-amber-500/8 px-3 py-5 text-center text-sm text-amber-700 dark:text-amber-300">
            当前节点离线或没有可用的实时累计流量，无法进行本次估算。
          </div>
          <div v-else-if="selectedEstimate" class="grid grid-cols-2 divide-x divide-y divide-border/60 overflow-hidden rounded-md border border-border/60 md:grid-cols-4 md:divide-y-0">
            <div class="p-3">
              <div class="text-xs text-muted-foreground">
                计费流量
              </div>
              <div class="mt-1 font-semibold tabular-nums">
                {{ formatTraffic(selectedEstimate.trafficTiB) }}
              </div>
              <div class="mt-1 text-[11px] text-muted-foreground">
                {{ trafficModeLabels[meteredSettings.trafficMode] }}
              </div>
            </div>
            <div class="p-3">
              <div class="text-xs text-muted-foreground">
                流量费用
              </div>
              <div class="mt-1 font-semibold tabular-nums">
                {{ formatPricingAmount(selectedEstimate.trafficCost) }}
              </div>
            </div>
            <div class="p-3">
              <div class="text-xs text-muted-foreground">
                时间 / 附加
              </div>
              <div class="mt-1 font-semibold tabular-nums">
                {{ formatPricingAmount(selectedEstimate.timeCost) }} / {{ formatPricingAmount(selectedEstimate.oneTimeCost) }}
              </div>
              <div class="mt-1 text-[11px] text-muted-foreground">
                手工 {{ formatHours(selectedEstimate.runtimeHours) }}
              </div>
            </div>
            <div class="p-3">
              <div class="text-xs text-muted-foreground">
                显示金额
              </div>
              <div class="mt-1 text-lg font-semibold tabular-nums">
                {{ formatOptionalDisplayAmount(totalMeteredCNY) }}
              </div>
              <div class="mt-1 text-[11px] text-muted-foreground">
                源币种小计 {{ formatPricingAmount(selectedEstimate.totalCost) }}
              </div>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="rates" class="space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <div class="mr-auto">
              <h3 class="text-sm font-semibold">
                汇率
              </h3>
              <p class="text-xs text-muted-foreground">
                {{ sourceLabels[source] }} · {{ formatRateUpdatedAt() }} · 1 CNY 对应数值
              </p>
            </div>
            <select
              :value="currency"
              aria-label="显示币种"
              class="h-8 rounded-md border border-input bg-background px-2 text-xs outline-none focus:ring-2 focus:ring-ring"
              @change="handleCurrencyChange"
            >
              <option v-for="item in financeHelper.SUPPORTED_CURRENCIES" :key="item" :value="item">
                {{ item }}
              </option>
            </select>
            <Button type="button" size="sm" variant="outline" class="h-8 gap-1.5" @click="emit('resetRates')">
              <Icon icon="tabler:refresh" width="14" height="14" />恢复今日汇率
            </Button>
          </div>

          <div class="grid grid-cols-2 gap-x-3 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
            <label v-for="item in financeHelper.SUPPORTED_CURRENCIES" :key="item" class="grid grid-cols-[2.5rem_1fr] items-center gap-2 text-xs">
              <span class="font-medium">{{ item }}</span>
              <Input
                type="number"
                min="0"
                step="any"
                :disabled="item === 'CNY'"
                :model-value="rates[item]"
                class="h-8 px-2 text-xs tabular-nums"
                @update:model-value="handleRateChange(item, $event)"
              />
            </label>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  </AppDialog>
</template>
