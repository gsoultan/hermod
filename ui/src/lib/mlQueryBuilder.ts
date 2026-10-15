// The dataset query builder: a table, the columns to read, filters, a sort
// and a row limit, written as the SELECT the chosen database runs. It is a
// way to the query, not a second query language: what it writes is shown,
// and can be taken on as SQL.

/** A column as the server describes it for the builder. */
export interface SourceColumn {
  name: string
  type?: string
  is_pk?: boolean
  is_nullable?: boolean
}

export type FilterOp = 'eq' | 'neq' | 'gt' | 'gte' | 'lt' | 'lte' | 'contains' | 'starts' | 'empty' | 'not_empty'

export interface QueryFilter {
  column: string
  op: FilterOp
  value: string
}

export interface BuiltQuery {
  table: string
  /** The columns to read; none reads every column. */
  columns: string[]
  filters: QueryFilter[]
  sort?: { column: string; desc: boolean }
  /** Keep only the first rows; unset reads them all. */
  limit?: number
}

export const EMPTY_BUILT_QUERY: BuiltQuery = { table: '', columns: [], filters: [] }

/** The operators, in the words the builder shows. */
export const FILTER_OPS: Array<{ value: FilterOp; label: string }> = [
  { value: 'eq', label: 'is' },
  { value: 'neq', label: 'is not' },
  { value: 'gt', label: 'more than' },
  { value: 'gte', label: 'at least' },
  { value: 'lt', label: 'less than' },
  { value: 'lte', label: 'at most' },
  { value: 'contains', label: 'contains' },
  { value: 'starts', label: 'starts with' },
  { value: 'empty', label: 'is empty' },
  { value: 'not_empty', label: 'is not empty' },
]

export const filterNeedsValue = (op: FilterOp) => op !== 'empty' && op !== 'not_empty'

const COMPARE: Partial<Record<FilterOp, string>> = { eq: '=', neq: '<>', gt: '>', gte: '>=', lt: '<', lte: '<=' }

// The same quoting as pkg/infra/sqlutil.QuoteIdent, so a name reads the same
// in a statement the builder writes as in one the server writes.
function quoteSegment(engine: string, s: string): string {
  switch (engine) {
    case 'mysql':
    case 'mariadb':
    case 'sqlite':
      return '`' + s.replaceAll('`', '``') + '`'
    case 'mssql':
      return '[' + s.replaceAll(']', ']]') + ']'
    case 'oracle':
      // Oracle folds an unquoted name to upper case; quoting the folded name
      // means what unquoted DDL meant.
      return '"' + s.replaceAll('"', '""').toUpperCase() + '"'
    default:
      return '"' + s.replaceAll('"', '""') + '"'
  }
}

/** A table or column name quoted for the engine, one dotted segment at a time. */
export function quoteIdent(engine: string, name: string): string {
  return name.split('.').map((p) => quoteSegment(engine, p)).join('.')
}

const NUMERIC_TYPE = /int|numeric|decimal|real|float|double|number|money|serial/i
const BOOLEAN_TYPE = /bool|^bit$/i
const NUMBER = /^-?\d+(\.\d+)?$/

const text = (v: string) => `'${v.replaceAll("'", "''")}'`

// A literal of the column's type: a number stays a number for a numeric
// column, yes/no is the engine's boolean, anything else is quoted text.
function literal(engine: string, value: string, type: string | undefined): string {
  const v = value.trim()
  if (type && NUMERIC_TYPE.test(type) && NUMBER.test(v)) return v
  if (type && BOOLEAN_TYPE.test(type) && /^(true|false)$/i.test(v)) {
    const yes = v.toLowerCase() === 'true'
    return engine === 'mssql' ? (yes ? '1' : '0') : (yes ? 'TRUE' : 'FALSE')
  }
  return text(value)
}

function condition(engine: string, f: QueryFilter, types: Record<string, string | undefined>): string | null {
  if (!f.column) return null
  if (filterNeedsValue(f.op) && f.value.trim() === '') return null
  const col = quoteIdent(engine, f.column)
  switch (f.op) {
    case 'empty':
      return `${col} IS NULL`
    case 'not_empty':
      return `${col} IS NOT NULL`
    case 'contains':
      return `${col} LIKE ${text(`%${f.value}%`)}`
    case 'starts':
      return `${col} LIKE ${text(`${f.value}%`)}`
    default:
      return `${col} ${COMPARE[f.op]} ${literal(engine, f.value, types[f.column])}`
  }
}

/**
 * The SELECT for a built query on the engine; empty until a table is picked.
 * `types` holds each column's database type, for the filters' literals.
 */
export function buildSelect(engine: string, q: BuiltQuery, types: Record<string, string | undefined> = {}): string {
  if (!q.table) return ''
  const e = engine.toLowerCase()
  const limit = q.limit && q.limit > 0 ? Math.floor(q.limit) : undefined
  const cols = q.columns.length ? q.columns.map((c) => quoteIdent(e, c)).join(', ') : '*'
  const lines = [`SELECT ${e === 'mssql' && limit ? `TOP ${limit} ` : ''}${cols}`, `FROM ${quoteIdent(e, q.table)}`]
  const where = q.filters.map((f) => condition(e, f, types)).filter((c): c is string => !!c)
  if (where.length) lines.push(`WHERE ${where.join('\n  AND ')}`)
  if (q.sort?.column) lines.push(`ORDER BY ${quoteIdent(e, q.sort.column)}${q.sort.desc ? ' DESC' : ''}`)
  if (limit && e !== 'mssql') {
    lines.push(e === 'oracle' || e === 'db2' ? `FETCH FIRST ${limit} ROWS ONLY` : `LIMIT ${limit}`)
  }
  return lines.join('\n')
}
