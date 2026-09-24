<script setup lang="ts">
import { computed } from 'vue'
import { formatBytes } from '@/utils/helper'

export interface TrafficProgressProps {
  upload: number
  download: number
  trafficLimit: number
  trafficLimitType: 'up' | 'down' | 'min' | 'max' | 'sum'
  uploadColor?: string
  downloadColor?: string
  singleColor?: string
  height?: number | string
  showIndicator?: boolean
}

const props = withDefaults(defineProps<TrafficProgressProps>(), {
  uploadColor: undefined,
  downloadColor: undefined,
  singleColor: undefined,
  height: undefined,
  showIndicator: false,
})

const showProgress = computed(() => props.trafficLimit > 0)

const usedTraffic = computed(() => {
  const { upload, download, trafficLimitType } = props
  switch (trafficLimitType) {
    case 'up': return upload
    case 'down': return download
    case 'min': return Math.min(upload, download)
    case 'max': return Math.max(upload, download)
    case 'sum': return upload + download
    default: return upload + download
  }
})

const totalPercentage = computed(() => {
  if (props.trafficLimit <= 0)
    return 0
  return Math.min((usedTraffic.value / props.trafficLimit) * 100, 100)
})

const uploadPercentage = computed(() => {
  if (props.trafficLimit <= 0)
    return 0
  return Math.min((props.upload / props.trafficLimit) * 100, 100)
})

const downloadPercentage = computed(() => {
  if (props.trafficLimit <= 0)
    return 0
  return Math.min((props.download / props.trafficLimit) * 100, 100)
})

const isDualColorMode = computed(() => props.trafficLimitType === 'sum')

const progressHeight = computed(() => {
  if (props.height === undefined)
    return undefined
  return typeof props.height === 'number' ? `${props.height}px` : props.height
})
</script>

<template>
  <div class="traffic-progress">
    <div v-if="isDualColorMode" class="traffic-progress__rail bg-muted" :style="{ height: progressHeight }">
      <div class="traffic-progress__fill bg-green-600" :style="{ width: `${uploadPercentage}%` }" />
      <div
        class="traffic-progress__fill traffic-progress__fill--last bg-blue-600"
        :style="{ width: `${downloadPercentage}%` }"
      />
    </div>

    <div v-else class="traffic-progress__rail bg-muted" :style="{ height: progressHeight }">
      <div
        class="traffic-progress__fill traffic-progress__fill--last bg-green-600"
        :style="{ width: `${totalPercentage}%` }"
      />
    </div>

    <div v-if="showIndicator && showProgress" class="traffic-progress__indicator">
      <span>{{ totalPercentage.toFixed(1) }}%</span>
      <span class="traffic-progress__indicator-detail">
        {{ formatBytes(usedTraffic) }} / {{ formatBytes(trafficLimit) }}
      </span>
    </div>
  </div>
</template>

<style scoped>
.traffic-progress {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}

.traffic-progress__rail {
  position: relative;
  display: flex;
  overflow: hidden;
  height: 8px;
  border-radius: 5px;
  transition: background-color 0.3s;
}

.traffic-progress__fill {
  position: relative;
  height: 100%;
  transition:
    max-width 0.2s,
    width 0.2s,
    background-color 0.3s;
}

.traffic-progress__fill--last {
  border-top-right-radius: 5px;
  border-bottom-right-radius: 5px;
}

.traffic-progress__indicator {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: hsl(var(--foreground) / 0.8);
}

.traffic-progress__indicator-detail {
  color: hsl(var(--muted-foreground));
}
</style>
