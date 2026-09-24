export interface LoadChartPalette {
  primary: string
  primaryAreaStrong: string
  primaryAreaFaint: string
  secondary: string
  tertiary: string
  tertiaryAreaStrong: string
  tertiaryAreaFaint: string
  quaternary: string
  quinary: string
  senary: string
}

const DEFAULT_LOAD_CHART_PALETTE: LoadChartPalette = {
  primary: '#FF6B6B',
  primaryAreaStrong: 'rgba(255, 107, 107, 0.25)',
  primaryAreaFaint: 'rgba(255, 107, 107, 0.02)',
  secondary: '#FFB347',
  tertiary: '#4ECDC4',
  tertiaryAreaStrong: 'rgba(78, 205, 196, 0.25)',
  tertiaryAreaFaint: 'rgba(78, 205, 196, 0.02)',
  quaternary: '#A78BFA',
  quinary: '#60A5FA',
  senary: '#34D399',
}

const ACCESSIBLE_LOAD_CHART_PALETTE: LoadChartPalette = {
  primary: '#D55E00',
  primaryAreaStrong: 'rgba(213, 94, 0, 0.25)',
  primaryAreaFaint: 'rgba(213, 94, 0, 0.02)',
  secondary: '#E69F00',
  tertiary: '#009E73',
  tertiaryAreaStrong: 'rgba(0, 158, 115, 0.25)',
  tertiaryAreaFaint: 'rgba(0, 158, 115, 0.02)',
  quaternary: '#CC79A7',
  quinary: '#0072B2',
  senary: '#56B4E9',
}

const DEFAULT_SERIES_PALETTE = [
  '#FF6B6B',
  '#4ECDC4',
  '#A78BFA',
  '#60A5FA',
  '#FFB347',
  '#F472B6',
  '#34D399',
  '#FB923C',
]

const ACCESSIBLE_SERIES_PALETTE = [
  '#0072B2',
  '#E69F00',
  '#009E73',
  '#CC79A7',
  '#D55E00',
  '#56B4E9',
  '#F0C94A',
  '#6B7280',
]

export const ACCESSIBLE_LINE_TYPES = ['solid', 'dashed', 'dotted'] as const

export function getLoadChartPalette(accessible: boolean): LoadChartPalette {
  return accessible ? ACCESSIBLE_LOAD_CHART_PALETTE : DEFAULT_LOAD_CHART_PALETTE
}

export function getChartSeriesPalette(accessible: boolean): string[] {
  return [...(accessible ? ACCESSIBLE_SERIES_PALETTE : DEFAULT_SERIES_PALETTE)]
}
