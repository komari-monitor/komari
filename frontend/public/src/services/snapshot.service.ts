import { escapeCsvCell, toCsvRows } from '@/utils/csv'

const UTF8_BOM = String.fromCharCode(0xFEFF)
const JSON_NEWLINE_REGEX = /\n/g

export interface SnapshotCsvColumn<T> {
  label: string
  value: (row: T) => string | number
}

export function buildSnapshotCsv<T>(columns: Array<SnapshotCsvColumn<T>>, rows: T[]): string {
  return toCsvRows([
    columns.map(column => column.label),
    ...rows.map(row => columns.map(column => column.value(row))),
  ])
}

export async function buildSnapshotCsvAsync<T>(columns: Array<SnapshotCsvColumn<T>>, rows: T[], yieldToBrowser: () => Promise<void>, chunkSize = 250): Promise<string> {
  const csvRows: string[] = [columns.map(column => escapeCsvCell(column.label)).join(',')]
  for (let index = 0; index < rows.length; index++) {
    const row = rows[index]
    if (!row)
      continue
    csvRows.push(columns.map(column => escapeCsvCell(column.value(row))).join(','))
    if ((index + 1) % chunkSize === 0)
      await yieldToBrowser()
  }
  return csvRows.join('\r\n')
}

function stringifyPretty(value: unknown): string {
  return JSON.stringify(value, null, 2) ?? 'null'
}

function indentJson(json: string, spaces: number): string {
  const padding = ' '.repeat(spaces)
  return json.replace(JSON_NEWLINE_REGEX, `\n${padding}`)
}

export async function buildSnapshotJsonAsync<T>(metadata: Record<string, unknown>, rows: T[], buildNode: (row: T) => unknown, yieldToBrowser: () => Promise<void>, chunkSize = 250): Promise<string> {
  const parts = Object.entries(metadata).map(([key, value]) => `  ${JSON.stringify(key)}: ${indentJson(stringifyPretty(value), 2)}`)
  const nodeParts: string[] = []

  for (let index = 0; index < rows.length; index++) {
    const row = rows[index]
    if (!row)
      continue
    nodeParts.push(`    ${indentJson(stringifyPretty(buildNode(row)), 4)}`)
    if ((index + 1) % chunkSize === 0)
      await yieldToBrowser()
  }

  return [
    '{',
    `${parts.join(',\n')}${parts.length > 0 ? ',\n' : ''}  "nodes": [`,
    nodeParts.join(',\n'),
    '  ]',
    '}',
  ].join('\n')
}

export function downloadText(filename: string, content: string, type: string, options?: { bom?: boolean }): void {
  const blob = new Blob([options?.bom ? UTF8_BOM : '', content], { type })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
