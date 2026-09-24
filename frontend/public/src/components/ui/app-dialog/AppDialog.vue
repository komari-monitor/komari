<script setup lang="ts">
import { Icon } from '@iconify/vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'

defineProps<{
  open: boolean
  title: string
  description?: string
  contentClass?: string
}>()

const emit = defineEmits<{
  'update:open': [open: boolean]
}>()
</script>

<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-100 bg-black/45 backdrop-blur-[2px] data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
      <DialogContent
        class="fixed left-1/2 top-1/2 z-101 flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-5xl -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-lg border border-border/70 bg-card/95 text-card-foreground shadow-2xl backdrop-blur-xl focus:outline-none"
        :class="contentClass"
      >
        <div class="flex items-start gap-3 border-b border-border/60 px-4 py-3 sm:px-5">
          <div class="min-w-0 flex-1">
            <DialogTitle class="truncate text-base font-semibold">
              {{ title }}
            </DialogTitle>
            <DialogDescription class="mt-0.5 text-xs text-muted-foreground" :class="!description && 'sr-only'">
              {{ description || title }}
            </DialogDescription>
          </div>
          <DialogClose as-child>
            <button
              type="button"
              class="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              aria-label="关闭"
            >
              <Icon icon="tabler:x" width="17" height="17" />
            </button>
          </DialogClose>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto p-4 sm:p-5">
          <slot />
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
