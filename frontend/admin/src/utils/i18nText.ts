export type I18nText = string | Record<string, string>;

// 语言键规范化：zh-CN / zh_CN / zh-cn / zh_cn 都归一为 "zhcn"。
const normalizeLang = (value: string) =>
  value.trim().toLowerCase().replace(/[-_]/g, "");

export function resolveI18nText(
  text: I18nText | undefined,
  language: string,
): string | undefined {
  if (text === undefined || text === null) return undefined;
  if (typeof text === "string") return text;

  const dict = text;
  const lang = (language || "").trim();
  if (!lang) {
    const first = Object.values(dict)[0];
    return first;
  }

  // 精确匹配（zh-CN / zh_CN 等原样键）
  if (dict[lang] !== undefined) return dict[lang];

  // 规范化匹配：zh-CN 命中 zh_CN，zh_cn 命中 zh-CN
  const langNorm = normalizeLang(lang);
  if (langNorm) {
    for (const [k, v] of Object.entries(dict)) {
      if (normalizeLang(k) === langNorm) return v;
    }
  }

  // 基础语言匹配（zh-CN -> zh）
  const base = lang.split(/[-_]/)[0];
  if (base && dict[base] !== undefined) return dict[base];
  if (base) {
    const baseNorm = normalizeLang(base);
    for (const [k, v] of Object.entries(dict)) {
      if (normalizeLang(k) === baseNorm) return v;
    }
    // 语言族前缀：manifest 只有 zh_CN 时，zh 也命中
    for (const [k, v] of Object.entries(dict)) {
      if (normalizeLang(k).startsWith(baseNorm)) return v;
    }
  }

  // 最后兜底：第一个值
  return Object.values(dict)[0];
}