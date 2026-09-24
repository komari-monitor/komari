<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { cn } from '@/lib/utils'

type DataTooltipPlacement = 'top' | 'bottom' | 'left' | 'right'

interface Props {
  /** 提示文本，留空且无 #content 插槽时不渲染气泡 */
  content?: string
  /** 气泡相对触发元素的方位 */
  placement?: DataTooltipPlacement
  /** 气泡宽度，number 视为 px；默认由内容撑起 */
  width?: number | string
  /** 气泡高度，number 视为 px；默认由内容撑起 */
  height?: number | string
  /** 包裹元素标签，默认 div */
  as?: string
  /** 包裹元素的附加类 */
  class?: HTMLAttributes['class']
  /** 气泡的附加类 */
  contentClass?: HTMLAttributes['class']
}

const props = withDefaults(defineProps<Props>(), {
  placement: 'top',
  as: 'div',
})

const placementClass: Record<DataTooltipPlacement, string> = {
  top: 'bottom-full left-1/2 mb-2 -translate-x-1/2',
  bottom: 'top-full left-1/2 mt-2 -translate-x-1/2',
  left: 'top-1/2 right-full mr-2 -translate-y-1/2',
  right: 'top-1/2 left-full ml-2 -translate-y-1/2',
}

const sizeStyle = computed(() => {
  const style: Record<string, string> = {}
  if (props.width != null)
    style.width = typeof props.width === 'number' ? `${props.width}px` : props.width
  if (props.height != null)
    style.height = typeof props.height === 'number' ? `${props.height}px` : props.height
  return style
})
</script>

<template>
  <component
    :is="as"
    data-slot="data-tooltip"
    :class="cn('group/data-tooltip relative inline-block', props.class)"
  >
    <slot />
    <span
      v-if="content || $slots.content"
      role="tooltip"
      :class="cn(
        'pointer-events-none absolute z-20 hidden rounded bg-foreground/80 p-1 text-[10px] leading-none text-background shadow-lg group-hover/data-tooltip:block group-focus-within/data-tooltip:block whitespace-normal break-words',
        placementClass[placement],
        props.contentClass,
      )"
      :style="sizeStyle"
    >
      <slot name="content">{{ content }}</slot>
    </span>
  </component>
</template>
