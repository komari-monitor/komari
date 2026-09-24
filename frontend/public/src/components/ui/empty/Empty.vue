<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { cn } from '@/lib/utils'

interface Props {
  description?: string
  class?: HTMLAttributes['class']
}

const props = defineProps<Props>()
</script>

<template>
  <div
    :class="cn(
      'flex flex-col items-center justify-center gap-3 p-8 text-muted-foreground',
      props.class,
    )"
  >
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="56"
      height="56"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      class="opacity-60"
    >
      <rect width="18" height="14" x="3" y="3" rx="2" />
      <path d="M3 14h18M8 18h.01M12 18h.01M16 18h.01" />
    </svg>
    <slot>
      <span class="text-sm">{{ description ?? '暂无数据' }}</span>
    </slot>
    <slot name="extra" />
  </div>
</template>
