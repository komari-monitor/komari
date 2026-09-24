import { TIME_MS } from './time'

export const LOAD_CONFIG = {
  records: {
    maxCount: 6_000,
    refreshInterval: 5 * TIME_MS.minute,
  },
  pingRecords: {
    maxCount: 6_000,
    summaryMaxCount: 150,
  },
  diskPrediction: {
    minSampleDays: 2,
  },
} as const

export const LOAD_RECORD_MAX_COUNT = LOAD_CONFIG.records.maxCount
export const PING_RECORD_MAX_COUNT = LOAD_CONFIG.pingRecords.maxCount
export const PING_SUMMARY_MAX_COUNT = LOAD_CONFIG.pingRecords.summaryMaxCount
