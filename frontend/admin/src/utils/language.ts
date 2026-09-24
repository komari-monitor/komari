export const LANGUAGE_STORAGE_KEY = "language";
export const LANGUAGE_COOKIE_KEY = "language";

const LANGUAGE_COOKIE_MAX_AGE_SECONDS = 60 * 60 * 24 * 365;

const parseStoredString = (value: string) => {
  try {
    const parsed = JSON.parse(value);
    return typeof parsed === "string" ? parsed : value;
  } catch {
    return value;
  }
};

export const normalizeLanguage = (language?: string | null) => {
  const raw = (language ?? "").trim();
  if (!raw) return "";

  const normalized = parseStoredString(raw).trim().replace(/_/g, "-");
  if (
    normalized.length < 2 ||
    normalized.length > 32 ||
    !/^[A-Za-z0-9-]+$/.test(normalized)
  ) {
    return "";
  }

  return normalized;
};

export const readStoredLanguage = () => {
  if (typeof window === "undefined") return "";
  return normalizeLanguage(window.localStorage.getItem(LANGUAGE_STORAGE_KEY));
};

export const writeLanguageCookie = (language?: string | null) => {
  if (typeof document === "undefined") return;

  const normalized = normalizeLanguage(language);
  if (!normalized) return;

  document.documentElement.lang = normalized;
  if (typeof window !== "undefined") {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, normalized);
  }
  document.cookie = `${LANGUAGE_COOKIE_KEY}=${encodeURIComponent(
    normalized,
  )}; path=/; max-age=${LANGUAGE_COOKIE_MAX_AGE_SECONDS}; SameSite=Lax`;
};
