<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { cn } from '@/lib/utils'

interface Props {
  percentage?: number
  status?: 'default' | 'success' | 'warning' | 'error' | 'info'
  height?: number | string
  class?: HTMLAttributes['class']
}

const props = withDefaults(defineProps<Props>(), {
  percentage: 0,
  status: 'default',
  height: 4,
})

const clamped = computed(() => Math.max(0, Math.min(100, Number(props.percentage) || 0)))

const heightStyle = computed(() => ({
  height: typeof props.height === 'number' ? `${props.height}px` : props.height,
}))

const statusClass = computed(() => {
  switch (props.status) {
    case 'success': return 'bg-success'
    case 'warning': return 'bg-warning'
    case 'error': return 'bg-destructive'
    case 'info': return 'bg-info'
    default: return 'bg-primary'
  }
})
</script>

<template>
  <div
    :class="cn('relative w-full overflow-hidden rounded-full bg-muted', props.class)"
    :style="heightStyle"
  >
    <div
      class="h-full rounded-full transition-[width] duration-300 ease-out"
      :class="statusClass"
      :style="{ width: `${clamped}%` }"
    />
  </div>
</template>
