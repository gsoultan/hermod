import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/api'
import { LIMITS, dataGuide, type GuideKind } from '@/lib/mlDataGuide'
import { isModelVHost, type DatasetColumn } from '@/lib/mlModels'
import { SCENARIOS, guessColumns, scenarioLabel, scenarioRoles, type Scenario } from '@/lib/mlRecommend'

// ---------------------------------------------------------------------------
// Dataset analysis: the profile the server computes over a dataset
// (pkg/ml/profile), said in plain words, and what the data is good for,
// measured against the requirements in mlDataGuide.ts.
// ---------------------------------------------------------------------------

/** What a column holds (pkg/ml/profile Kind*). */
export type ColumnKind = 'number' | 'category' | 'text' | 'date' | 'yes_no' | 'id' | 'empty'

export type Severity = 'high' | 'medium' | 'low'

export interface ColumnProfile {
  name: string
  /** As the dataset stores it. */
  type: DatasetColumn['type']
  kind: ColumnKind
  missing: number
  missing_share: number
  distinct: number
  numbers?: {
    min: number; max: number; mean: number; median: number; q1: number; q3: number; std: number; skew: number
    integer: boolean; outliers: number
  }
  top?: Array<{ value: string; rows: number; share: number }>
  other_share?: number
  dates?: { min: string; max: string; per: 'month' | 'year'; counts: Array<{ label: string; rows: number }>; future: number }
  histogram?: Array<{ from: number; to: number; rows: number }>
}

export interface ProfileWarning {
  code: string
  severity: Severity
  column?: string
  message: string
  fix: string
}

export interface DatasetProfile {
  dataset: string
  rows: number
  column_count: number
  sample_rows: number
  sampled: boolean
  max_sample_rows: number
  duplicate_rows: number
  columns: ColumnProfile[]
  warnings: ProfileWarning[]
  correlations: Array<{ a: string; b: string; r: number; rows: number }>
  computed_at: string
}

export const datasetProfileKey = (vhost: string, name: string) => ['ml-dataset-profile', vhost, name] as const

export async function getDatasetProfile(vhost: string, name: string, signal?: AbortSignal): Promise<DatasetProfile> {
  const res = await apiFetch(`/api/vhosts/${encodeURIComponent(vhost)}/ml/datasets/${encodeURIComponent(name)}/profile`, { signal, silent: true })
  return (await res.json()) as DatasetProfile
}

/** A dataset's analysis; it reads every sampled row, so it is kept for a while. */
export function useDatasetProfile(vhost: string, name: string) {
  return useQuery({
    queryKey: datasetProfileKey(vhost, name),
    queryFn: ({ signal }) => getDatasetProfile(vhost, name, signal),
    enabled: isModelVHost(vhost) && !!name,
    retry: false,
    staleTime: 5 * 60_000,
  })
}

const analyzedKey = (vhost: string) => `hermod.ml.analyzed.${vhost}`
const ANALYZED_EVENT = 'hermod-ml-analyzed'

/** Whether a dataset of the vhost was analyzed in this browser (step 2 of getting started). */
export function wasAnalyzed(vhost: string): boolean {
  try {
    return localStorage.getItem(analyzedKey(vhost)) === '1'
  } catch {
    return false
  }
}

/** Remembers that a dataset of the vhost was analyzed, and tells whoever shows it. */
export function markAnalyzed(vhost: string) {
  if (wasAnalyzed(vhost)) return
  try {
    localStorage.setItem(analyzedKey(vhost), '1')
  } catch {
    // Without storage the step is done once a model is trained.
  }
  window.dispatchEvent(new CustomEvent(ANALYZED_EVENT, { detail: vhost }))
}

/** wasAnalyzed, kept current as datasets are analyzed. */
export function useAnalyzed(vhost: string | null | undefined): boolean {
  const [seen, setSeen] = useState<Record<string, boolean>>({})
  useEffect(() => {
    const on = (e: Event) => {
      const v = (e as CustomEvent<string>).detail
      setSeen((s) => ({ ...s, [v]: true }))
    }
    window.addEventListener(ANALYZED_EVENT, on)
    return () => window.removeEventListener(ANALYZED_EVENT, on)
  }, [])
  return !!vhost && (!!seen[vhost] || wasAnalyzed(vhost))
}

/** The files a dataset is uploaded from (pkg/ml/worker FileFormats). */
export const FILE_FORMATS = ['csv', 'xlsx', 'parquet'] as const
export type FileFormat = (typeof FILE_FORMATS)[number]

/** What the file picker offers: the extensions and their media types. */
export const FILE_ACCEPT = '.csv,.xlsx,.parquet,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.apache.parquet'

/** A file's format by its name, or null for one Hermod cannot read. */
export function fileFormat(fileName: string): FileFormat | null {
  const ext = fileName.toLowerCase().split('.').pop() ?? ''
  return (FILE_FORMATS as readonly string[]).includes(ext) ? (ext as FileFormat) : null
}

const fmt = (n: number) => n.toLocaleString('en-US')

/** "1 date", "2 numbers". */
const count = (n: number, one: string, many = `${one}s`) => `${fmt(n)} ${n === 1 ? one : many}`

/** "a, b and c". */
export function joinWords(words: string[]): string {
  if (words.length <= 1) return words.join('')
  return `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}`
}

const KIND_WORDS: Array<[ColumnKind, string, string]> = [
  ['number', 'number', 'numbers'],
  ['category', 'category', 'categories'],
  ['date', 'date', 'dates'],
  ['yes_no', 'yes/no column', 'yes/no columns'],
  ['text', 'free-text column', 'free-text columns'],
  ['id', 'ID', 'IDs'],
  ['empty', 'empty column', 'empty columns'],
]

/** The words for each kind of column, for labels. */
export const KIND_LABEL: Record<ColumnKind, string> = {
  number: 'Number', category: 'Category', date: 'Date', yes_no: 'Yes/no', text: 'Free text', id: 'ID', empty: 'Empty',
}

/** The analysis in a few plain sentences, for the top of the page. */
export function profileSummary(p: DatasetProfile): string[] {
  const kinds = KIND_WORDS
    .map(([k, one, many]) => [p.columns.filter((c) => c.kind === k).length, one, many] as const)
    .filter(([n]) => n > 0)
    .map(([n, one, many]) => count(n, one, many))
  const lines = [`${count(p.rows, 'row')} and ${count(p.column_count, 'column')}${kinds.length ? `: ${joinWords(kinds)}` : ''}.`]
  if (p.sampled) {
    lines.push(`Measured on ${fmt(p.sample_rows)} rows spread evenly over all ${fmt(p.rows)}, so the numbers are close estimates.`)
  }
  const date = p.columns.find((c) => c.kind === 'date' && c.dates)
  if (date?.dates) lines.push(`${date.name} runs from ${date.dates.min} to ${date.dates.max}.`)
  const gaps = p.columns.filter((c) => c.missing > 0 && c.kind !== 'empty').length
  if (gaps) lines.push(`${count(gaps, 'column has', 'columns have')} empty cells.`)
  const fix = p.warnings.filter((w) => w.severity !== 'low').length
  const know = p.warnings.length - fix
  if (!p.warnings.length) lines.push('No problems found.')
  else if (!fix) lines.push(`Nothing to fix; ${count(know, 'thing')} worth knowing, below.`)
  else lines.push(`${count(fix, 'thing')} worth fixing${know ? ` and ${fmt(know)} worth knowing` : ''}, below.`)
  return lines
}

/** Where training starts from an analysis: the dataset, and what to make of it. */
export interface TrainStart {
  dataset: string
  goal: 'predict' | 'recommend'
  target?: string
  scenario?: Scenario
}

/** One kind of model the data suits, why, and where training starts. */
export interface GoodFor {
  kind: GuideKind
  title: string
  ready: boolean
  reason: string
  start: TrainStart
  /** For recommendations: the scenarios the columns found are enough for. */
  scenarios?: Scenario[]
}

/** Column names that usually hold what is predicted. */
const ANSWER_NAME = /churn|label|target|class|outcome|status|result|fraud|default|convert|cancel|left|won|lost|bought|approved/i
const NUMBER_NAME = /amount|total|revenue|spend|price|sales|value|cost|days|duration|qty|quantity|score|profit|margin|income/i
/** A role column's words in a reason: "customer", "product". */
const ROLE_WORDS: Record<string, string> = { customer: 'customer', product: 'product', date: 'date', quantity: 'quantity', amount: 'amount' }

const usable = (c: ColumnProfile) => c.kind !== 'id' && c.kind !== 'empty' && c.kind !== 'text' && c.distinct > 1

/**
 * What the data is good for: each kind of model Hermod trains whose columns
 * the data has, by the column types and names, best first, with whether it
 * is ready and why in words. A kind the data has no columns for is left out.
 */
export function goodFor(p: DatasetProfile): GoodFor[] {
  const out: GoodFor[] = []
  const tooFew = p.rows < LIMITS.minRows
    ? `Only ${fmt(p.rows)} rows: training needs at least ${LIMITS.minRows}. Add more rows first.`
    : null
  const start = (goal: TrainStart['goal'], more: Partial<TrainStart> = {}): TrainStart => ({ dataset: p.dataset, goal, ...more })
  const byName = new Map(p.columns.map((c) => [c.name, c]))

  // Recommendations: order lines, where a customer appears on several rows.
  const cols: DatasetColumn[] = p.columns.map((c) => ({ name: c.name, type: c.type }))
  const guess = guessColumns('transactions', cols)
  const customer = guess.customer ? byName.get(guess.customer) : undefined
  const repeats = !!customer && customer.kind !== 'empty' && customer.distinct < (p.rows - customer.missing) * 0.9
  if (customer && repeats) {
    if (guess.date && byName.get(guess.date)?.kind !== 'date') delete guess.date
    const scenarios = SCENARIOS.map((s) => s.value).filter((s) => {
      const tx = scenarioRoles(s).find((r) => r.role === 'transactions')
      return !!tx && tx.columns.filter((c) => c.required).every((c) => !!guess[c.key])
    })
    if (scenarios.length) {
      const found = Object.keys(ROLE_WORDS).filter((k) => guess[k]).map((k) => `${ROLE_WORDS[k]} (${guess[k]})`)
      const reason = `Has ${joinWords(found)}: ready for ${scenarios.length === SCENARIOS.length || scenarios.length > 2
        ? 'recommendations' : joinWords(scenarios.map((s) => scenarioLabel(s).toLowerCase()))}.`
      out.push({
        kind: 'recommend', title: dataGuide('recommend').title, ready: !tooFew, reason: tooFew ?? reason, scenarios,
        start: start('recommend', { scenario: scenarios.includes('products') ? 'products' : scenarios[0] }),
      })
    }
  }

  // Predicting a category: a column with a few answers, and others to learn from.
  const answers = p.columns
    .filter((c) => (c.kind === 'yes_no' || c.kind === 'category' || (c.kind === 'number' && c.numbers?.integer))
      && c.distinct >= 2 && c.distinct <= LIMITS.maxAutoClasses)
    .sort((a, b) => Number(ANSWER_NAME.test(b.name)) - Number(ANSWER_NAME.test(a.name))
      || Number(b.kind === 'yes_no') - Number(a.kind === 'yes_no') || a.distinct - b.distinct)
  const target = answers[0]
  if (target) {
    const others = p.columns.filter((c) => c !== target && usable(c)).length
    const reason = others
      ? `${target.name} has ${target.distinct} answers, and ${count(others, 'other column')} can be learned from: ready to predict it.`
      : `${target.name} has ${target.distinct} answers, but no other column to learn from. Add columns about each row.`
    out.push({ kind: 'classification', title: dataGuide('classification').title, ready: !tooFew && others > 0,
      reason: tooFew ?? reason, start: start('predict', { target: target.name }) })
  }

  // Predicting a number: a number column with many values.
  const amounts = p.columns
    .filter((c) => c.kind === 'number' && c.distinct > LIMITS.maxAutoClasses)
    .sort((a, b) => Number(NUMBER_NAME.test(b.name)) - Number(NUMBER_NAME.test(a.name)) || b.distinct - a.distinct)
  const amount = amounts[0]
  if (amount) {
    const others = p.columns.filter((c) => c !== amount && usable(c)).length
    const reason = others
      ? `${amount.name} is a number, and ${count(others, 'other column')} can be learned from: ready to estimate it.`
      : `${amount.name} is a number, but there is no other column to learn from. Add columns about each row.`
    out.push({ kind: 'regression', title: dataGuide('regression').title, ready: !tooFew && others > 0,
      reason: tooFew ?? reason, start: start('predict', { target: amount.name }) })
  }
  return out
}

/** The severities, worst first, with their words and colour. */
export const SEVERITY: Record<Severity, { label: string; color: string }> = {
  high: { label: 'Fix before training', color: 'red' },
  medium: { label: 'Worth fixing', color: 'yellow' },
  low: { label: 'Good to know', color: 'blue' },
}
