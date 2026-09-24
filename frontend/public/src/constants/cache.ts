import { TIME_MS } from './time'

export const CACHE_CONFIG = {
  providerMetadata: {
    maxSize: 1000,
    ttl: TIME_MS.day,
  },
  request: {
    ttl: 5 * TIME_MS.minute,
  },
  promise: {
    ttl: 30 * TIME_MS.second,
  },
  cleanup: {
    interval: 5 * TIME_MS.minute,
  },
} as const
