import { NETWORK_CONFIG } from './network'

export const REQUEST_CONFIG = {
  timeout: {
    default: NETWORK_CONFIG.timeout.request,
  },
  retry: {
    attempts: NETWORK_CONFIG.retry.attempts,
  },
  pool: {
    maxConcurrent: NETWORK_CONFIG.concurrency.maxRequests,
  },
} as const
