<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { computed, useAttrs } from 'vue'
import { cn } from '@/lib/utils'

interface Props {
  title?: string
  size?: 'small' | 'medium' | 'large'
  hoverable?: boolean
  bordered?: boolean
  segmented?: boolean | { content?: boolean, footer?: 'soft' | true }
  contentClass?: HTMLAttributes['class']
  headerClass?: HTMLAttributes['class']
  class?: HTMLAttributes['class']
}

const props = withDefaults(defineProps<Props>(), {
  size: 'medium',
  hoverable: false,
  bordered: true,
})

const hasHeaderSlot = defineSlots<{
  'header'?: () => any
  'header-extra'?: () => any
  'default'?: () => any
  'footer'?: () => any
}>()

const attrs = useAttrs()

const paddingClass = computed(() => {
  const isShowHeader = hasHeaderSlot.header || hasHeaderSlot['header-extra'] || props.title
  const pt = isShowHeader ? 'pt-0' : ''
  if (props.size === 'small')
    return `p-3 ${pt}`
  if (props.size === 'large')
    return `p-6 ${pt}`
  return `p-4 ${pt}`
})

const headerPaddingClass = computed(() => {
  if (props.size === 'small')
    return 'px-3 py-2'
  if (props.size === 'large')
    return 'px-6 py-4'
  return 'px-4 py-3'
})

const segmentedContent = computed(() => {
  if (typeof props.segmented === 'boolean')
    return props.segmented
  return !!props.segmented?.content
})

const segmentedFooter = computed(() => {
  if (typeof props.segmented === 'boolean')
    return props.segmented
  return !!props.segmented?.footer
})
</script>

<template>
  <div
    v-bind="attrs"
    :class="cn(
      'bg-card text-card-foreground flex flex-col rounded-lg',
      bordered && 'border',
      hoverable && 'transition-colors hover:border-foreground/30',
      props.class,
    )"
  >
    <div
      v-if="hasHeaderSlot.header || title || hasHeaderSlot['header-extra']"
      :class="cn(
        'flex items-center gap-2',
        headerPaddingClass,
        segmentedContent && 'border-b',
        props.headerClass,
      )"
    >
      <div class="flex-1 min-w-0">
        <slot name="header">
          <span class="font-medium">{{ title }}</span>
        </slot>
      </div>
      <div v-if="hasHeaderSlot['header-extra']" class="shrink-0">
        <slot name="header-extra" />
      </div>
    </div>
    <div :class="cn(paddingClass, props.contentClass)">
      <slot />
    </div>
    <div
      v-if="hasHeaderSlot.footer"
      :class="cn(
        headerPaddingClass,
        segmentedFooter && 'border-t bg-muted/40',
      )"
    >
      <slot name="footer" />
    </div>
  </div>
</template>
