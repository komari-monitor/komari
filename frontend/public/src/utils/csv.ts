const CSV_ESCAPE_NEEDED_REGEX = /[",\n\r]/
const CSV_QUOTE_REGEX = /"/g
// Detect formula prefixes even when hidden behind leading whitespace, BOM, or NBSP.
// eslint-disable-next-line regexp/no-useless-flag
const CSV_FORMULA_INJECTION_REGEX = /^\s*[=+\-@|]/i

export function sanitizeCsvCell(value: unknown): string {
  const text = String(value ?? '')
  return CSV_FORMULA_INJECTION_REGEX.test(text) ? `'${text}` : text
}

export function sanitizeCsvValue(value: unknown): string {
  return sanitizeCsvCell(value)
}

export function escapeCsvCell(value: unknown): string {
  const text = sanitizeCsvCell(value)
  if (CSV_ESCAPE_NEEDED_REGEX.test(text))
    return `"${text.replace(CSV_QUOTE_REGEX, '""')}"`
  return text
}

export function toCsvRows(rows: Array<Array<unknown>>): string {
  return rows.map(row => row.map(escapeCsvCell).join(',')).join('\r\n')
}
