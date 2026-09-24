import { useEffect, useState } from 'react';
import type { Appearance } from '../contexts/ThemeContext';

/**
 * Custom hook to convert "system" appearance to actual "light" or "dark" for Radix UI
 * @param appearance - The appearance setting from context ("light", "dark", or "system")
 * @returns The resolved appearance for Radix UI ("light" or "dark")
 */
export const useSystemTheme = (appearance: Appearance): "light" | "dark" => {
  const [systemTheme, setSystemTheme] = useState<"light" | "dark">(() => {
    // Initial system theme detection
    if (typeof window !== 'undefined' && window.matchMedia) {
      return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    return 'light';
  });

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) {
      return;
    }

    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

    const handleChange = (e: MediaQueryListEvent) => {
      setSystemTheme(e.matches ? 'dark' : 'light');
    };

    // Add listener for system theme changes
    mediaQuery.addEventListener('change', handleChange);

    // Cleanup
    return () => mediaQuery.removeEventListener('change', handleChange);
  }, []);

  // Return the resolved theme
  if (appearance === 'system') {
    return systemTheme;
  }

  return appearance as "light" | "dark";
};
