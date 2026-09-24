<script setup lang="ts">
import { Icon } from '@iconify/vue'
import { computed } from 'vue'

type MetricChartTone = 'rose' | 'amber' | 'emerald' | 'cyan' | 'sky' | 'violet' | 'orange' | 'slate'

const props = withDefaults(defineProps<{
  title: string
  icon: string
  tone?: MetricChartTone
  subtitle?: string
}>(), {
  tone: 'slate',
  subtitle: '',
})

const toneClass = computed(() => ({
  rose: 'bg-rose-500/10 text-rose-500 ring-rose-500/20',
  amber: 'bg-amber-500/10 text-amber-500 ring-amber-500/20',
  emerald: 'bg-emerald-500/10 text-emerald-500 ring-emerald-500/20',
  cyan: 'bg-cyan-500/10 text-cyan-500 ring-cyan-500/20',
  sky: 'bg-sky-500/10 text-sky-500 ring-sky-500/20',
  violet: 'bg-violet-500/10 text-violet-500 ring-violet-500/20',
  orange: 'bg-orange-500/10 text-orange-500 ring-orange-500/20',
  slate: 'bg-slate-500/10 text-slate-500 ring-slate-500/20',
})[props.tone])
</script>

<template>
  <div class="flex min-w-0 items-center justify-between gap-3">
    <div class="flex min-w-0 items-center gap-2.5">
      <div class="flex size-8 shrink-0 items-center justify-center rounded-md ring-1 ring-inset" :class="toneClass">
        <Icon :icon="icon" :width="17" :height="17" />
      </div>
      <div class="min-w-0">
        <div class="truncate text-sm font-semibold sm:text-base">
          {{ title }}
        </div>
        <div v-if="subtitle" class="truncate text-[10px] leading-4 text-muted-foreground" :title="subtitle">
          {{ subtitle }}
        </div>
      </div>
    </div>
    <div class="min-w-0 shrink-0 text-right text-xs text-muted-foreground">
      <slot />
    </div>
  </div>
</template>
