import { TIME_MS } from './time'

export const SECURITY_CONFIG = {
  auth: {
    verifyTtl: TIME_MS.minute,
  },
  csv: {
    formulaPrefixes: ['=', '+', '-', '@'],
  },
  export: {
    secondaryPasswordSessionKey: 'komari-theme-export-secondary-password-verified',
  },
} as const

export const PRIVATE_HOME_TOOL_KEYS = ['topology', 'providerValue', 'healthSummary', 'snapshotExport'] as const

export type PrivateHomeToolKey = typeof PRIVATE_HOME_TOOL_KEYS[number]
