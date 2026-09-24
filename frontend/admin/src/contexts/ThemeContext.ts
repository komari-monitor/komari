import { createContext } from 'react';

export const allowedColors = [
  "gray", "gold", "bronze", "brown", "yellow", "amber",
  "orange", "tomato", "red", "ruby", "crimson", "pink",
  "plum", "purple", "violet", "iris", "indigo", "blue",
  "cyan", "teal", "jade", "green", "grass", "lime",
  "mint", "sky",
] as const;

export type Colors = typeof allowedColors[number];

export const allowedAppearances = ["light", "dark", "system"] as const;
export type Appearance = typeof allowedAppearances[number];

export const THEME_DEFAULTS = {
  appearance: "system" as Appearance,
  color: "iris" as Colors,
} as const;

export interface ThemeContextType {
  appearance: Appearance;
  setAppearance: (appearance: Appearance) => void;
  color: Colors;
  setColor: (color: Colors) => void;
}

export const ThemeContext = createContext<ThemeContextType>({
  appearance: THEME_DEFAULTS.appearance,
  setAppearance: () => {},
  color: THEME_DEFAULTS.color,
  setColor: () => {},
});
