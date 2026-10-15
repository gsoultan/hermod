import { describe, expect, it } from 'vitest'
import { dataGuide } from '@/lib/mlDataGuide'
import {
  fileFormat, goodFor, profileSummary, type ColumnProfile, type DatasetProfile,
} from '@/lib/mlDataProfile'

/**
 * Dataset analysis: what the server measured (pkg/ml/profile), said in
 * plain words, and what the data is good for, by the requirements in
 * mlDataGuide.ts.
 */

const column = (name: string, kind: ColumnProfile['kind'], extra: Partial<ColumnProfile> = {}): ColumnProfile => ({
  name, kind, type: kind === 'number' || kind === 'id' ? 'number' : kind === 'yes_no' ? 'bool' : 'string',
  missing: 0, missing_share: 0, distinct: 50, ...extra,
})

const profileOf = (columns: ColumnProfile[], extra: Partial<DatasetProfile> = {}): DatasetProfile => ({
  dataset: 'orders', rows: 5000, column_count: columns.length, sample_rows: 5000, sampled: false, max_sample_rows: 50000,
  duplicate_rows: 0, columns, warnings: [], correlations: [], computed_at: '2026-10-15T00:00:00Z', ...extra,
})

const orderLines = profileOf([
  column('customer_id', 'category', { distinct: 400 }),
  column('product_id', 'category', { distinct: 120 }),
  column('order_date', 'date', { dates: { min: '2025-01-02', max: '2026-09-30', per: 'month', counts: [], future: 0 } }),
  column('quantity', 'number'),
  column('amount', 'number'),
])

const customers = profileOf([
  column('customer_id', 'id', { distinct: 5000 }),
  column('plan', 'category', { distinct: 3 }),
  column('tenure_months', 'number'),
  column('monthly_spend', 'number', { distinct: 900 }),
  column('churned', 'yes_no', { distinct: 2 }),
])

describe('fileFormat', () => {
  it('reads the format from the file name, and refuses others', () => {
    expect(fileFormat('sales.CSV')).toBe('csv')
    expect(fileFormat('book.xlsx')).toBe('xlsx')
    expect(fileFormat('part-0.parquet')).toBe('parquet')
    expect(fileFormat('old.xls')).toBeNull()
    expect(fileFormat('notes.txt')).toBeNull()
  })
})

describe('profileSummary', () => {
  it('says how big the data is and what its columns hold', () => {
    const lines = profileSummary(orderLines)
    expect(lines[0]).toBe('5,000 rows and 5 columns: 2 numbers, 2 categories and 1 date.')
    expect(lines).toContain('order_date runs from 2025-01-02 to 2026-09-30.')
    expect(lines).toContain('No problems found.')
  })

  it('says when it looked at a sample, and how many problems there are', () => {
    const p = profileOf([column('a', 'number')], {
      rows: 1_200_000, sample_rows: 50_000, sampled: true,
      warnings: [
        { code: 'mostly_empty', severity: 'medium', column: 'a', message: 'a is 70% empty.', fix: 'Fill it.' },
        { code: 'skewed', severity: 'low', column: 'a', message: 'a is skewed.', fix: 'Range.' },
      ],
    })
    const lines = profileSummary(p)
    expect(lines).toContain('Measured on 50,000 rows spread evenly over all 1,200,000, so the numbers are close estimates.')
    expect(lines).toContain('1 thing worth fixing and 1 worth knowing, below.')
  })
})

describe('goodFor', () => {
  it('sees order lines as ready for recommendations, naming the columns it found', () => {
    const hints = goodFor(orderLines)
    const rec = hints.find((h) => h.kind === 'recommend')!
    expect(rec.ready).toBe(true)
    expect(rec.title).toBe(dataGuide('recommend').title)
    expect(rec.reason).toBe('Has customer (customer_id), product (product_id), date (order_date), quantity (quantity) and amount (amount): ready for recommendations.')
    expect(rec.scenarios).toEqual(expect.arrayContaining(['products', 'reorder', 'audience', 'similar']))
    expect(rec.start).toEqual({ dataset: 'orders', goal: 'recommend', scenario: 'products' })
  })

  it('sees one row per customer with a yes/no column as ready to predict a category', () => {
    const hints = goodFor(customers)
    const cls = hints.find((h) => h.kind === 'classification')!
    expect(cls.ready).toBe(true)
    expect(cls.reason).toBe('churned has 2 answers, and 3 other columns can be learned from: ready to predict it.')
    expect(cls.start).toEqual({ dataset: 'orders', goal: 'predict', target: 'churned' })
    const reg = hints.find((h) => h.kind === 'regression')!
    expect(reg.start.target).toBe('monthly_spend')
    // Every customer once: not order lines.
    expect(hints.find((h) => h.kind === 'recommend')).toBeUndefined()
  })

  it('says why nothing is ready on too few rows', () => {
    const hints = goodFor({ ...customers, rows: 6 })
    expect(hints.every((h) => !h.ready)).toBe(true)
    expect(hints[0].reason).toMatch(/only 6 rows: training needs at least 10/i)
  })
})
