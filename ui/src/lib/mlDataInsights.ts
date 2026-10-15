// What a few rows say about a dataset before it is one: what each column
// holds, with a hint in plain words, and which columns fit what a kind of
// model needs. It reads a sample (a query preview, a file's first lines) or,
// before any row is read, a table's column names and types. It guides; the
// server's analysis of the stored dataset is the check that counts.

import type { GuideColumn, KindGuide } from './mlDataGuide'

export type InsightKind = 'id' | 'number' | 'yes_no' | 'date' | 'category' | 'text' | 'empty'

export interface ColumnInsight {
  name: string
  kind: InsightKind
  /** The share of the sample's rows that hold a value, 0 to 1. */
  filled: number
  distinct: number
  /** What the column holds and what to do about it, in a sentence or two. */
  hint: string
  /** The column holds one value only: no model learns from it. */
  constant?: boolean
}

export const INSIGHT_LABEL: Record<InsightKind, string> = {
  id: 'ID', number: 'Number', yes_no: 'Yes / no', date: 'Date', category: 'Category', text: 'Text', empty: 'Empty',
}

const ID_NAME = /(^id$|_id$|^id_|Id$|uuid|guid|^key$|_key$|_code$|^code$|_no$)/
const YES_NO = new Set(['yes', 'no', 'true', 'false', 'y', 'n', 't', 'f', '0', '1'])
const NUMBER = /^-?\d+(\.\d+)?([eE][-+]?\d+)?$/
const DATE = /^\d{4}-\d{2}-\d{2}([ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$|^\d{1,2}[/.]\d{1,2}[/.]\d{2,4}$/
/** At most this many different values in a sample makes a text column a category. */
const CATEGORY_MAX = 12

const isEmpty = (v: unknown) => v === null || v === undefined || (typeof v === 'string' && v.trim() === '')
const pct = (x: number) => `${Math.round(x * 100)}%`

/** "ID (12), SG (5) and MY (3)": the commonest values of a sample. */
function topValues(values: string[], n = 3): string {
  const counts = new Map<string, number>()
  for (const v of values) counts.set(v, (counts.get(v) ?? 0) + 1)
  const top = [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, n).map(([v, c]) => `${v} (${c})`)
  return top.length > 1 ? `${top.slice(0, -1).join(', ')} and ${top[top.length - 1]}` : top[0] ?? ''
}

const fmtNum = (n: number) => (Number.isInteger(n) ? n.toLocaleString('en-US') : n.toLocaleString('en-US', { maximumFractionDigits: 2 }))

/** What one column of a sample holds. */
export function profileColumn(name: string, raw: unknown[]): ColumnInsight {
  const values = raw.filter((v) => !isEmpty(v)).map((v) => (typeof v === 'string' ? v.trim() : String(v)))
  const filled = raw.length ? values.length / raw.length : 0
  const distinct = new Set(values).size
  const base = { name, filled, distinct }
  if (values.length === 0) {
    return { ...base, kind: 'empty', hint: 'Empty in these rows. Leave it out, or check the query or file reads it.' }
  }
  const constant = distinct === 1 && values.length >= 5
  const gaps = filled < 0.7 ? ` ${pct(1 - filled)} of these rows are empty: fill them in or leave the column out.` : ''
  const same = constant ? ' It holds one value only, so no model can learn from it.' : ''
  const numbers = values.every((v) => NUMBER.test(v))
  const unique = distinct === values.length

  // Named like an ID, it is one, repeated or not: an order line's
  // product_id repeats, and its numbers are labels, not amounts.
  if (ID_NAME.test(name) || (unique && values.length >= 5 && !numbers && values.every((v) => /\d/.test(v) && !v.includes(' ') && !DATE.test(v)))) {
    return { ...base, kind: 'id', hint: `Looks like an ID. It tells rows apart, and links people and items for recommendations, but a model does not learn from it.${gaps}` }
  }
  if (distinct <= 2 && values.every((v) => YES_NO.has(v.toLowerCase()))) {
    return { ...base, kind: 'yes_no', constant, hint: `Yes or no: ${topValues(values)}. A good column to predict, or to learn from.${same}${gaps}` }
  }
  if (numbers) {
    const nums = values.map(Number)
    const min = Math.min(...nums)
    const max = Math.max(...nums)
    return {
      ...base, kind: 'number', constant,
      hint: `Numbers from ${fmtNum(min)} to ${fmtNum(max)}. Learn from it, or predict it to estimate a number.${same}${gaps}`,
    }
  }
  if (values.every((v) => DATE.test(v))) {
    const sorted = [...values].sort()
    return {
      ...base, kind: 'date', constant,
      hint: `Dates from ${sorted[0].slice(0, 10)} to ${sorted[sorted.length - 1].slice(0, 10)}. Needed for forecasts and reorders; when a row happened.${same}${gaps}`,
    }
  }
  if (distinct <= CATEGORY_MAX || distinct <= values.length / 3) {
    return {
      ...base, kind: 'category', constant,
      hint: `${distinct} different values, mostly ${topValues(values)}. Learn from it, or predict it as a category.${same}${gaps}`,
    }
  }
  const long = values.reduce((n, v) => n + v.length, 0) / values.length > 30
  return {
    ...base, kind: 'text',
    hint: long
      ? `Free text. Most models skip it; a short category made from it helps more.${gaps}`
      : `Many different values. If it names things (people, products), treat it like an ID.${gaps}`,
  }
}

/** Every column of a sample: rows as lists in the columns' order, or as maps. */
export function sampleInsights(columns: string[], rows: Array<unknown[] | Record<string, unknown>>): ColumnInsight[] {
  return columns.map((c, i) => profileColumn(c, rows.map((r) => (Array.isArray(r) ? r[i] : r[c]))))
}

/** A column's kind from its database type and name alone, before any row is read. */
export function kindFromType(name: string, type = ''): InsightKind {
  const t = type.toLowerCase()
  if (ID_NAME.test(name)) return 'id'
  if (/bool|^bit$/.test(t)) return 'yes_no'
  if (/date|time/.test(t)) return 'date'
  if (/int|numeric|decimal|real|float|double|number|money|serial/.test(t)) return 'number'
  return 'text'
}

/** A column as matching needs it: its name and what it holds. */
export interface ColumnShape {
  name: string
  kind: InsightKind
}

/** A guide column, and the data's column that fits it. */
export interface GuideMatch {
  column: GuideColumn
  required: boolean
  match?: string
}

// The words a column fitting a guide column usually has in its name, by the
// last part of the guide column's key ("transactions.customer" -> customer).
const SYNONYMS: Record<string, string[]> = {
  customer: ['customer', 'cust', 'client', 'account', 'user', 'member', 'buyer', 'outlet', 'store'],
  user: ['customer', 'cust', 'client', 'account', 'user', 'member', 'buyer', 'person', 'visitor'],
  product: ['product', 'item', 'sku', 'article', 'prod', 'material'],
  item: ['product', 'item', 'sku', 'article', 'prod', 'song', 'movie', 'content'],
  date: ['date', 'day', 'time', 'created', 'ordered', 'when', '_at'],
  time: ['date', 'day', 'time', 'created', 'ordered', 'when', '_at'],
  quantity: ['qty', 'quantity', 'units', 'count', 'pieces'],
  strength: ['qty', 'quantity', 'rating', 'score', 'count', 'units', 'plays', 'views'],
  amount: ['amount', 'total', 'value', 'revenue', 'sales', 'price', 'spend'],
}
const CLASS_TARGET = ['churn', 'label', 'target', 'class', 'status', 'outcome', 'converted', 'fraud', 'left', 'cancel', 'is_', 'has_', 'will_']
const NUMBER_TARGET = ['amount', 'total', 'spend', 'revenue', 'price', 'value', 'days', 'next_', 'sales', 'cost']

/** The kinds a column fitting a guide column may be. */
function fits(key: string, guideKind: string, kind: InsightKind): boolean {
  if (key === 'target') {
    return guideKind === 'regression' ? kind === 'number' : kind === 'yes_no' || kind === 'category' || kind === 'number'
  }
  if (['customer', 'user', 'product', 'item', 'id', 'salesman'].includes(key)) return kind === 'id' || kind === 'text' || kind === 'number' || kind === 'category'
  if (key === 'date' || key === 'time') return kind === 'date' || kind === 'text'
  if (['quantity', 'strength', 'amount', 'lat', 'lon'].includes(key)) return kind === 'number'
  return kind !== 'empty'
}

const norm = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '_')

function score(guide: KindGuide, g: GuideColumn, c: ColumnShape): number {
  const key = g.key.split('.').pop() as string
  if (!fits(key, guide.kind, c.kind)) return 0
  const name = norm(c.name)
  if (g.header && name === norm(g.header)) return 100
  const words = key === 'target'
    ? (guide.kind === 'regression' ? NUMBER_TARGET : CLASS_TARGET)
    : SYNONYMS[key] ?? [key, ...norm(g.header).split('_').filter((w) => w.length > 2)]
  if (words.some((w) => name.includes(w))) return 60
  // A yes/no column is the likeliest answer to predict a category.
  if (key === 'target' && guide.kind === 'classification' && c.kind === 'yes_no') return 40
  return 0
}

/**
 * Which of the data's columns fit the guide's columns: each guide column
 * that names one (not a whole table) gets the best-fitting column not
 * already taken, required columns first. "Columns to learn from" are the
 * rest, so they are not matched one by one.
 */
export function matchGuide(guide: KindGuide, columns: ColumnShape[]): GuideMatch[] {
  const taken = new Set<string>()
  const wanted = [
    ...guide.required.map((column) => ({ column, required: true })),
    ...guide.optional.map((column) => ({ column, required: false })),
  ].filter((w) => w.column.header && !/features$/.test(w.column.key))
  return wanted.map((w) => {
    let best: string | undefined
    let top = 0
    for (const c of columns) {
      if (taken.has(c.name)) continue
      const s = score(guide, w.column, c)
      if (s > top) {
        top = s
        best = c.name
      }
    }
    if (best) taken.add(best)
    return { ...w, match: best }
  })
}

/** The columns left to learn from: not matched, not IDs, not empty. */
export function learnFrom(matches: GuideMatch[], columns: ColumnShape[]): string[] {
  const used = new Set(matches.map((m) => m.match).filter(Boolean))
  return columns.filter((c) => !used.has(c.name) && c.kind !== 'id' && c.kind !== 'empty').map((c) => c.name)
}
