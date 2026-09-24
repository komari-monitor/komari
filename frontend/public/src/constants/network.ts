import { TIME_MS } from './time'

export const NETWORK_CONFIG = {
  timeout: {
    request: 30 * TIME_MS.second,
  },
  concurrency: {
    maxRequests: 8,
  },
  retry: {
    attempts: 1,
  },
} as const

export const MAX_CONCURRENT_REQUESTS = NETWORK_CONFIG.concurrency.maxRequests
