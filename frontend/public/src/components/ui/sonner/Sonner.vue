<script lang="ts" setup>
import type { ToasterProps } from 'vue-sonner'

import { Icon } from '@iconify/vue'
import { Toaster as Sonner } from 'vue-sonner'
import { cn } from '@/lib/utils'
import 'vue-sonner/style.css'

const props = defineProps<ToasterProps>()
</script>

<template>
  <Sonner
    :class="cn('toaster group', props.class)" :style="{
      '--normal-bg': 'var(--popover)',
      '--normal-text': 'var(--popover-foreground)',
      '--normal-border': 'var(--border)',
      '--border-radius': 'var(--radius)',
    }" v-bind="props"
  >
    <template #success-icon>
      <Icon icon="lucide:circle-check" class="size-4" />
    </template>
    <template #info-icon>
      <Icon icon="lucide:info" class="size-4" />
    </template>
    <template #warning-icon>
      <Icon icon="lucide:triangle-alert" class="size-4" />
    </template>
    <template #error-icon>
      <Icon icon="lucide:octagon-x" class="size-4" />
    </template>
    <template #loading-icon>
      <div>
        <Icon icon="lucide:loader-2" class="size-4 animate-spin" />
      </div>
    </template>
    <template #close-icon>
      <Icon icon="lucide:x" class="size-4" />
    </template>
  </Sonner>
</template>
