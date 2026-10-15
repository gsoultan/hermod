// What the upload dialog shows of a file before sending it: a name for the
// dataset, its size, and for a CSV the header and first rows. The server
// reads the file for real; this is only a look at it.

/** A dataset name from a file name: the stem, with what a name may not hold made "_". */
export function datasetNameFromFile(fileName: string): string {
  const stem = fileName.replace(/\.[^.]+$/, '')
  return stem
    .replace(/[^A-Za-z0-9_.-]+/g, '_')
    .replace(/_+/g, '_')
    .replace(/^[^A-Za-z0-9]+/, '')
    .replace(/_+$/, '')
    .slice(0, 128)
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 2 ** 20) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 2 ** 20).toFixed(1)} MB`
}

export interface CsvPreview {
  columns: string[]
  rows: string[][]
  delimiter: string
}

const DELIMITERS = [',', ';', '\t', '|']

// The delimiter is the one the header line splits on most.
function guessDelimiter(text: string): string {
  const header = text.split(/\r?\n/, 1)[0] ?? ''
  let best = ','
  let most = 0
  for (const d of DELIMITERS) {
    const n = header.split(d).length - 1
    if (n > most) {
      best = d
      most = n
    }
  }
  return best
}

/**
 * The header and first rows of CSV text. `truncated` says the text is the
 * start of a longer file, so its last line may be cut off and is dropped.
 */
export function parseCsvPreview(text: string, { maxRows = 8, truncated = false }: { maxRows?: number; truncated?: boolean } = {}): CsvPreview {
  const delimiter = guessDelimiter(text)
  const records: string[][] = []
  let record: string[] = []
  let field = ''
  let quoted = false
  let i = 0
  const endRecord = () => {
    record.push(field)
    records.push(record)
    record = []
    field = ''
  }
  while (i < text.length && records.length <= maxRows) {
    const c = text[i]
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') {
        field += '"'
        i++
      } else if (c === '"') {
        quoted = false
      } else {
        field += c
      }
    } else if (c === '"' && field === '') {
      quoted = true
    } else if (c === delimiter) {
      record.push(field)
      field = ''
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++
      endRecord()
    } else {
      field += c
    }
    i++
  }
  // The last line has no line break: it is whole only if the text is.
  if (records.length <= maxRows && (field !== '' || record.length) && !truncated) endRecord()
  const [columns = [], ...rows] = records.filter((r) => r.length > 1 || r[0] !== '')
  return { columns, rows: rows.slice(0, maxRows), delimiter }
}
