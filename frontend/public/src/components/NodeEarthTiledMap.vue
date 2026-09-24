<script setup lang="ts">
import type { NodeData } from '@/stores/nodes'
import { computed } from 'vue'
import { useNodeGeoClusters } from '@/composables/useNodeGeoClusters'

const props = defineProps<{
  nodes?: NodeData[]
}>()

const MAP_WIDTH = 1440
const MAP_HEIGHT = 720
const MAP_PADDING = 18
const VISIBLE_NORTH_LAT = 74
const VISIBLE_SOUTH_LAT = -58
const TEXTURE_FULL_HEIGHT = MAP_HEIGHT * 180 / (VISIBLE_NORTH_LAT - VISIBLE_SOUTH_LAT)
const TEXTURE_SOURCE_Y = TEXTURE_FULL_HEIGHT * (90 - VISIBLE_NORTH_LAT) / 180
const EARTH_DAY_TEXTURE = '/images/earth/earth-blue-marble.jpg'
const EARTH_BUMP_MAP = '/images/earth/earth-topology.png'
const EARTH_SPECULAR_MAP = '/images/earth/earth-water.png'

interface MapPoint {
  x: number
  y: number
}

interface ClusterMarker {
  id: string
  index: number
  code: string
  label: string
  meta: string
  x: number
  y: number
  statusClass: string
}

const {
  regionClusters,
  totalServers,
  onlineServers,
  offlineServers,
} = useNodeGeoClusters({ nodes: () => props.nodes })

const legendDensityClass = computed(() => {
  const count = regionClusters.value.length
  if (count > 36)
    return 'legend-ultra-dense'
  if (count > 28)
    return 'legend-very-dense'
  if (count > 20)
    return 'legend-dense'
  return ''
})
const onlineRate = computed(() => {
  if (totalServers.value === 0)
    return 0
  return Math.round((onlineServers.value / totalServers.value) * 100)
})

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max)
}

function projectCoord(coord: [number, number]): MapPoint {
  const [lat, lng] = coord
  const visibleLat = clamp(lat, VISIBLE_SOUTH_LAT, VISIBLE_NORTH_LAT)
  return {
    x: clamp(((lng + 180) / 360) * MAP_WIDTH, MAP_PADDING, MAP_WIDTH - MAP_PADDING),
    y: clamp(((VISIBLE_NORTH_LAT - visibleLat) / (VISIBLE_NORTH_LAT - VISIBLE_SOUTH_LAT)) * MAP_HEIGHT, MAP_PADDING, MAP_HEIGHT - MAP_PADDING),
  }
}

function formatClusterMeta(cluster: { code: string, asn?: string, org?: string }): string {
  const location = cluster.code || 'NODE'
  const provider = cluster.asn || cluster.org
  return provider ? `${provider} · ${location}` : location
}

const clusterMarkers = computed<ClusterMarker[]>(() => regionClusters.value.map((cluster, index) => {
  const point = projectCoord(cluster.coord)
  return {
    id: cluster.id,
    index: index + 1,
    code: cluster.code,
    label: cluster.label,
    meta: formatClusterMeta(cluster),
    x: point.x,
    y: point.y,
    statusClass: cluster.onlineServers > 0 ? 'is-online' : 'is-offline',
  }
}))
</script>

<template>
  <div class="earth-map-scroll relative z-0 h-full w-full overflow-x-auto overflow-y-visible pointer-events-auto">
    <div class="earth-map-shell relative mx-auto h-full w-full overflow-hidden rounded-[1.5rem] border border-white/35 bg-background/35 shadow-[0_24px_80px_rgb(15_23_42/0.18)] backdrop-blur-2xl dark:border-cyan-200/10 dark:bg-slate-950/35">
      <div class="earth-map relative h-full min-w-0 overflow-hidden">
        <svg class="map-svg absolute inset-0 size-full" :viewBox="`0 0 ${MAP_WIDTH} ${MAP_HEIGHT}`" preserveAspectRatio="xMidYMid meet" role="img" aria-label="真实地球贴图节点世界地图">
          <defs>
            <filter id="earth-relief" x="-4%" y="-4%" width="108%" height="108%">
              <feDropShadow dx="0" dy="3" stdDeviation="3" flood-color="#ffffff" flood-opacity="0.14" />
              <feDropShadow dx="0" dy="-5" stdDeviation="9" flood-color="#082f49" flood-opacity="0.16" />
            </filter>
          </defs>

          <image :href="EARTH_DAY_TEXTURE" x="0" :y="-TEXTURE_SOURCE_Y" :width="MAP_WIDTH" :height="TEXTURE_FULL_HEIGHT" preserveAspectRatio="none" class="earth-image earth-image-base" />
          <image :href="EARTH_BUMP_MAP" x="0" :y="-TEXTURE_SOURCE_Y" :width="MAP_WIDTH" :height="TEXTURE_FULL_HEIGHT" preserveAspectRatio="none" class="earth-image earth-image-bump" filter="url(#earth-relief)" />
          <image :href="EARTH_SPECULAR_MAP" x="0" :y="-TEXTURE_SOURCE_Y" :width="MAP_WIDTH" :height="TEXTURE_FULL_HEIGHT" preserveAspectRatio="none" class="earth-image earth-image-water" />
          <rect :width="MAP_WIDTH" :height="MAP_HEIGHT" class="earth-overlay" />

          <g class="city-points">
            <template v-for="marker in clusterMarkers" :key="`${marker.id}-point`">
              <circle :cx="marker.x" :cy="marker.y" r="11" class="city-region" :class="marker.statusClass" />
              <circle :cx="marker.x" :cy="marker.y" r="3.8" class="city-dot" :class="marker.statusClass" />
              <image
                v-if="marker.code"
                :href="`/images/flags/${marker.code}.svg`"
                :x="marker.x - 13"
                :y="marker.y - 34"
                width="26"
                height="26"
                preserveAspectRatio="xMidYMid slice"
                class="map-flag"
              />
            </template>
          </g>
        </svg>

        <div class="absolute left-3 top-3 z-10 flex items-center gap-2 rounded-full border border-sky-100/35 bg-background/55 px-2.5 py-1 text-[10px] font-medium text-muted-foreground shadow-lg shadow-sky-950/10 backdrop-blur-xl md:left-4 md:top-4">
          <span class="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-300">
            <span class="size-1.5 rounded-full bg-emerald-400 shadow-[0_0_10px_rgb(52_211_153/0.85)]" />
            {{ onlineServers }} ONLINE
          </span>
          <span v-if="offlineServers > 0" class="inline-flex items-center gap-1 text-rose-500 dark:text-rose-300">
            <span class="size-1.5 rounded-full bg-rose-400 shadow-[0_0_10px_rgb(251_113_133/0.7)]" />
            {{ offlineServers }} OFF
          </span>
        </div>
      </div>

      <div class="legend-panel" :class="legendDensityClass">
        <div class="legend-title">
          EARTH MAP
          <span v-if="totalServers > 0">{{ onlineRate }}%</span>
        </div>
        <div v-for="marker in clusterMarkers" :key="marker.id" class="legend-item" :class="marker.statusClass">
          <span class="legend-index">{{ marker.index }}</span>
          <img v-if="marker.code" :src="`/images/flags/${marker.code}.svg`" :alt="marker.code" class="legend-flag">
          <span class="legend-copy">
            <span v-if="marker.label" class="legend-name">{{ marker.label }}</span>
            <span class="legend-meta">{{ marker.meta }}</span>
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.earth-map-shell {
  display: grid;
  isolation: isolate;
  min-height: 18rem;
  grid-template-columns: minmax(0, 1fr) minmax(14rem, 26%);
  background: color-mix(in oklab, var(--background) 68%, rgb(56 189 248 / 0.24));
}

.earth-map {
  min-height: 18rem;
  border-right: 1px solid rgb(255 255 255 / 0.24);
}

.earth-map::after {
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background:
    radial-gradient(circle at 18% 18%, rgb(255 255 255 / 0.18), transparent 32%),
    linear-gradient(135deg, rgb(255 255 255 / 0.1), transparent 38%, rgb(15 23 42 / 0.08));
  content: '';
  pointer-events: none;
}

.map-svg {
  padding-inline: 0;
}

.earth-image-base {
  opacity: 0.92;
  filter: saturate(1.08) contrast(1) brightness(1.12);
}

.earth-image-bump {
  opacity: 0.2;
  mix-blend-mode: overlay;
  filter: contrast(1.35) brightness(1.12);
}

.earth-image-water {
  opacity: 0.16;
  mix-blend-mode: screen;
  filter: saturate(0.42) contrast(1.22) brightness(1.04);
}

.earth-overlay {
  fill: rgb(255 255 255 / 0.05);
  mix-blend-mode: soft-light;
}

:global(.dark .earth-image-base) {
  filter: saturate(1.1) contrast(1.04) brightness(0.92);
}

:global(.dark .earth-image-bump) {
  opacity: 0.28;
}

:global(.dark .earth-image-water) {
  opacity: 0.2;
}

.city-region {
  fill: rgb(253 224 71 / 0.24);
  stroke: rgb(253 224 71 / 0.46);
  stroke-width: 1.1;
  vector-effect: non-scaling-stroke;
}

.city-region.is-offline {
  fill: rgb(251 113 133 / 0.14);
  stroke: rgb(251 113 133 / 0.38);
}

.city-dot {
  fill: rgb(253 224 71 / 0.95);
  stroke: rgb(21 128 61 / 0.84);
  stroke-width: 1.3;
  vector-effect: non-scaling-stroke;
}

.city-dot.is-offline {
  fill: rgb(251 113 133 / 0.9);
  stroke: rgb(190 18 60 / 0.8);
}

.map-flag {
  overflow: hidden;
  clip-path: inset(0 round 1.6px);
  filter: drop-shadow(0 3px 5px rgb(15 23 42 / 0.32));
}

.legend-panel {
  --legend-grid-columns: repeat(2, minmax(0, 1fr));
  --legend-gap: 0.22rem;
  --legend-item-columns: 1.05rem 0.85rem minmax(0, 1fr);
  --legend-item-gap: 0.26rem;
  --legend-item-radius: 0.55rem;
  --legend-item-padding: 0.2rem 0.32rem;
  --legend-index-size: 1rem;
  --legend-index-font-size: 0.58rem;
  --legend-flag-size: 0.86rem;
  --legend-copy-line-height: 1.05;
  --legend-name-font-size: 0.58rem;
  --legend-meta-font-size: 0.46rem;

  position: relative;
  z-index: 14;
  display: grid;
  min-width: 0;
  max-height: 100%;
  align-content: start;
  grid-template-columns: var(--legend-grid-columns);
  gap: var(--legend-gap);
  overflow: hidden;
  padding: 0.7rem 0.75rem;
  background: rgb(255 255 255 / 0.08);
  pointer-events: none;
}

.legend-title {
  display: flex;
  grid-column: 1 / -1;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  border: 1px solid rgb(255 255 255 / 0.36);
  border-radius: 999px;
  background: rgb(255 255 255 / 0.45);
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.1);
  padding: 0.22rem 0.7rem;
  color: rgb(14 116 144 / 0.86);
  font-size: 0.62rem;
  font-weight: 800;
  letter-spacing: 0.22em;
  backdrop-filter: blur(12px) saturate(150%);
}

.legend-title span {
  color: rgb(5 150 105 / 0.95);
  letter-spacing: normal;
}

.legend-dense {
  --legend-gap: 0.16rem;
  --legend-item-columns: 0.92rem 0.74rem minmax(0, 1fr);
  --legend-item-gap: 0.18rem;
  --legend-item-radius: 0.45rem;
  --legend-item-padding: 0.14rem 0.24rem;
  --legend-index-size: 0.82rem;
  --legend-index-font-size: 0.5rem;
  --legend-flag-size: 0.74rem;
  --legend-copy-line-height: 1;
  --legend-name-font-size: 0.52rem;
  --legend-meta-font-size: 0.4rem;
}

.legend-very-dense {
  --legend-gap: 0.12rem;
  --legend-item-columns: 0.82rem 0.66rem minmax(0, 1fr);
  --legend-item-gap: 0.14rem;
  --legend-item-radius: 0.38rem;
  --legend-item-padding: 0.1rem 0.2rem;
  --legend-index-size: 0.72rem;
  --legend-index-font-size: 0.44rem;
  --legend-flag-size: 0.66rem;
  --legend-copy-line-height: 0.96;
  --legend-name-font-size: 0.48rem;
  --legend-meta-font-size: 0.36rem;
}

.legend-ultra-dense {
  --legend-gap: 0.1rem;
  --legend-item-columns: 0.74rem 0.58rem minmax(0, 1fr);
  --legend-item-gap: 0.12rem;
  --legend-item-radius: 0.34rem;
  --legend-item-padding: 0.08rem 0.16rem;
  --legend-index-size: 0.64rem;
  --legend-index-font-size: 0.38rem;
  --legend-flag-size: 0.58rem;
  --legend-copy-line-height: 0.94;
  --legend-name-font-size: 0.44rem;
  --legend-meta-font-size: 0.32rem;
}

.legend-item {
  display: grid;
  grid-template-columns: var(--legend-item-columns);
  align-items: center;
  gap: var(--legend-item-gap);
  border: 1px solid rgb(255 255 255 / 0.38);
  border-radius: var(--legend-item-radius);
  background: rgb(255 255 255 / 0.44);
  box-shadow: 0 8px 18px rgb(15 23 42 / 0.12);
  padding: var(--legend-item-padding);
  backdrop-filter: blur(12px) saturate(150%);
}

.legend-index {
  display: inline-grid;
  width: var(--legend-index-size);
  height: var(--legend-index-size);
  place-items: center;
  border-radius: 999px;
  background: rgb(253 224 71 / 0.92);
  color: rgb(15 23 42 / 0.86);
  font-size: var(--legend-index-font-size);
  font-weight: 800;
}

.legend-item.is-offline .legend-index {
  background: rgb(251 113 133 / 0.85);
  color: white;
}

.legend-flag {
  width: var(--legend-flag-size);
  height: var(--legend-flag-size);
  border-radius: 0.12rem;
  object-fit: cover;
}

.legend-copy {
  display: flex;
  min-width: 0;
  flex-direction: column;
  line-height: var(--legend-copy-line-height);
}

.legend-name {
  overflow: hidden;
  color: rgb(15 23 42 / 0.84);
  font-size: var(--legend-name-font-size);
  font-weight: 800;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.legend-meta {
  overflow: hidden;
  color: rgb(71 85 105 / 0.76);
  font-size: var(--legend-meta-font-size);
  font-weight: 700;
  letter-spacing: 0.06em;
  text-overflow: ellipsis;
  white-space: nowrap;
}

:global(.dark .earth-map) {
  border-right-color: rgb(125 211 252 / 0.12);
}

:global(.dark .legend-panel) {
  background: rgb(15 23 42 / 0.18);
}

:global(.dark .legend-title),
:global(.dark .legend-item) {
  border-color: rgb(125 211 252 / 0.18);
  background: rgb(15 23 42 / 0.58);
}

:global(.dark .legend-title) {
  color: rgb(186 230 253 / 0.86);
}

:global(.dark .legend-title span) {
  color: rgb(110 231 183 / 0.92);
}

:global(.dark .legend-name) {
  color: rgb(255 255 255 / 0.9);
}

:global(.dark .legend-meta) {
  color: rgb(186 230 253 / 0.66);
}

@media (max-width: 640px) {
  .earth-map-scroll {
    touch-action: pan-x pan-y;
  }

  .earth-map-shell {
    min-width: 42rem;
    grid-template-columns: minmax(28rem, 1fr) minmax(11rem, 30%);
  }

  .earth-map {
    min-width: 0;
    min-height: 18rem;
  }

  .map-svg {
    padding-inline: 0;
  }

  .legend-panel {
    --legend-grid-columns: 1fr;
    --legend-gap: 0.16rem;
    --legend-item-columns: 0.92rem 0.74rem minmax(0, 1fr);
    --legend-item-gap: 0.18rem;
    --legend-item-radius: 0.45rem;
    --legend-item-padding: 0.14rem 0.24rem;
    --legend-index-size: 0.82rem;
    --legend-index-font-size: 0.5rem;
    --legend-flag-size: 0.74rem;
    --legend-copy-line-height: 1;
    --legend-name-font-size: 0.52rem;
    --legend-meta-font-size: 0.4rem;
    padding: 0.55rem 0.5rem;
  }

  .legend-title {
    padding: 0.18rem 0.45rem;
    font-size: 0.54rem;
    letter-spacing: 0.15em;
  }

  .legend-very-dense,
  .legend-ultra-dense {
    --legend-gap: 0.12rem;
    --legend-item-columns: 0.78rem 0.62rem minmax(0, 1fr);
    --legend-item-gap: 0.12rem;
    --legend-item-radius: 0.36rem;
    --legend-item-padding: 0.08rem 0.16rem;
    --legend-index-size: 0.66rem;
    --legend-index-font-size: 0.4rem;
    --legend-flag-size: 0.62rem;
    --legend-copy-line-height: 0.94;
    --legend-name-font-size: 0.44rem;
    --legend-meta-font-size: 0.32rem;
  }
}
</style>
