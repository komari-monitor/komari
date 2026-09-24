import type { GlassCustomColors } from '@/stores/app'

export type GlassColorPreset = 'emerald' | 'soft' | 'contrast' | 'midnight' | 'custom'

interface GlassThemeTokens {
  lightCard: string
  lightCardHover: string
  lightControl: string
  lightHeader: string
  lightText: string
  lightMutedText: string
  lightBorder: string
  lightShadow: string
  darkCard: string
  darkCardHover: string
  darkControl: string
  darkHeader: string
  darkText: string
  darkMutedText: string
  darkBorder: string
  darkShadow: string
}

const PRESET_TOKENS: Record<Exclude<GlassColorPreset, 'custom'>, GlassThemeTokens> = {
  emerald: {
    lightCard: '#f1f5f9bd',
    lightCardHover: '#f8fafccc',
    lightControl: '#e2e8f0c2',
    lightHeader: '#e2e8f0bd',
    lightText: '#10151c',
    lightMutedText: '#374151',
    lightBorder: '#cbd5e199',
    lightShadow: '0 8px 28px rgb(15 23 42 / 0.18)',
    darkCard: '#0d111ad9',
    darkCardHover: '#111827e8',
    darkControl: '#101624d9',
    darkHeader: '#0b1020d9',
    darkText: '#f8fafc',
    darkMutedText: '#d6dae4',
    darkBorder: '#ffffff2e',
    darkShadow: '0 8px 30px rgb(0 0 0 / 0.48)',
  },
  soft: {
    lightCard: '#f8fafcdb',
    lightCardHover: '#f8fafceb',
    lightControl: '#f1f5f9e0',
    lightHeader: '#f1f5f9e0',
    lightText: '#14151a',
    lightMutedText: '#4b5563',
    lightBorder: '#cbd5e1a6',
    lightShadow: '0 8px 24px rgb(15 23 42 / 0.12)',
    darkCard: '#111827e6',
    darkCardHover: '#111827f2',
    darkControl: '#111827e0',
    darkHeader: '#0f172ae6',
    darkText: '#f8fafc',
    darkMutedText: '#cbd5e1',
    darkBorder: '#ffffff24',
    darkShadow: '0 8px 26px rgb(0 0 0 / 0.42)',
  },
  contrast: {
    lightCard: '#f8fafcf2',
    lightCardHover: '#f8fafcff',
    lightControl: '#f1f5f9f2',
    lightHeader: '#f1f5f9f2',
    lightText: '#080b12',
    lightMutedText: '#1f2937',
    lightBorder: '#cbd5e1cc',
    lightShadow: '0 10px 30px rgb(2 6 23 / 0.2)',
    darkCard: '#020617f2',
    darkCardHover: '#020617ff',
    darkControl: '#020617f0',
    darkHeader: '#020617f2',
    darkText: '#ffffff',
    darkMutedText: '#e5e7eb',
    darkBorder: '#ffffff3d',
    darkShadow: '0 10px 34px rgb(0 0 0 / 0.58)',
  },
  midnight: {
    lightCard: '#eaf4ffcc',
    lightCardHover: '#f3f8ffe6',
    lightControl: '#eaf4ffe0',
    lightHeader: '#eaf4ffd9',
    lightText: '#0f172a',
    lightMutedText: '#334155',
    lightBorder: '#c7ddff99',
    lightShadow: '0 8px 30px rgb(30 64 175 / 0.18)',
    darkCard: '#07111fcc',
    darkCardHover: '#0b1628e6',
    darkControl: '#07111fd9',
    darkHeader: '#07111fd9',
    darkText: '#eaf2ff',
    darkMutedText: '#c7d2fe',
    darkBorder: '#60a5fa40',
    darkShadow: '0 8px 34px rgb(0 0 0 / 0.52)',
  },
}

function withHoverAlpha(color: string): string {
  return color.length === 9 ? `${color.slice(0, 7)}e6` : color
}

export function buildGlassThemeTokens(preset: GlassColorPreset, customColors: GlassCustomColors): GlassThemeTokens {
  if (preset !== 'custom')
    return PRESET_TOKENS[preset]

  return {
    lightCard: customColors.lightCard,
    lightCardHover: withHoverAlpha(customColors.lightCard),
    lightControl: customColors.lightControl,
    lightHeader: customColors.lightControl,
    lightText: customColors.lightText,
    lightMutedText: customColors.lightMutedText,
    lightBorder: customColors.lightBorder,
    lightShadow: '0 8px 28px rgb(15 23 42 / 0.16)',
    darkCard: customColors.darkCard,
    darkCardHover: withHoverAlpha(customColors.darkCard),
    darkControl: customColors.darkControl,
    darkHeader: customColors.darkControl,
    darkText: customColors.darkText,
    darkMutedText: customColors.darkMutedText,
    darkBorder: customColors.darkBorder,
    darkShadow: '0 8px 30px rgb(0 0 0 / 0.48)',
  }
}
