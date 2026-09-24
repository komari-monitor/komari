import i18next from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import en from "./locales/en.json";
import zh_CN from "./locales/zh_CN.json";
import zh_TW from "./locales/zh_TW.json";
import ja_JP from "./locales/ja_JP.json";
import id_ID from "./locales/id_ID.json";
import {
  LANGUAGE_STORAGE_KEY,
  readStoredLanguage,
  writeLanguageCookie,
} from "@/utils/language";

// 不添加 name 字段的语言将不会在语言切换菜单中显示
// not adding the name field will hide the language from the language switcher menu
const resources = {
  en: {
    translation: en,
  },
  "en-US": {
    translation: en,
    name: "English",
  },
  "en-GB": {
    translation: en,
  },
  "en-CA": {
    translation: en,
  },
  "en-AU": {
    translation: en,
  },
  "zh": {
    translation: zh_CN,
  },
  "zh-CN": {
    translation: zh_CN,
    name: "简体中文",
  },
  "zh-SG": {
    translation: zh_CN,  // Singapore uses Simplified Chinese
  },
  "zh-TW": {
    translation: zh_TW,
    name: "繁體中文",
  },
  "zh-HK": {
    translation: zh_TW,  // Hong Kong uses Traditional Chinese
  },
  "zh-MO": {
    translation: zh_TW,  // Macau uses Traditional Chinese
  },
  "ja": {
    translation: ja_JP,
  },
  "ja-JP": {
    translation: ja_JP,
    name: "日本語",
  },
  "id": {
    translation: id_ID,
  },
  "id-ID": {
    translation: id_ID,
    name: "Bahasa Indonesia",
  },
};

writeLanguageCookie(readStoredLanguage());

i18next.on("languageChanged", (language) => {
  writeLanguageCookie(language);
});

const i18n = i18next;

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: "en-US",
    interpolation: {
      escapeValue: false, // React handles XSS
    },
    detection: {
      order: ["localStorage", "navigator"],
      caches: ["localStorage"],
      lookupLocalStorage: LANGUAGE_STORAGE_KEY,
    },
  });

export default i18n;
export { resources };
