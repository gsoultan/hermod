import { describe, expect, it } from 'vitest'
import { dataGuide } from '@/lib/mlDataGuide'
import { kindFromType, learnFrom, matchGuide, profileColumn, sampleInsights } from '@/lib/mlDataInsights'

describe('what a sample says about each column', () => {
  it('tells IDs, yes/no, numbers, dates, categories and text apart', () => {
    const cols = ['customer_id', 'churned', 'monthly_spend', 'signup_date', 'plan', 'comment']
    const rows = Array.from({ length: 20 }, (_, i) => [
      `C-${i}`, i % 4 === 0 ? 'yes' : 'no', String(10 + i * 2.5), `2025-0${(i % 9) + 1}-1${i % 10}`,
      ['basic', 'pro', 'team'][i % 3], `a long free text comment about customer number ${i} and their visit`,
    ])
    const got = Object.fromEntries(sampleInsights(cols, rows).map((c) => [c.name, c]))
    expect(got.customer_id.kind).toBe('id')
    expect(got.churned.kind).toBe('yes_no')
    expect(got.churned.hint).toMatch(/good column to predict/)
    expect(got.monthly_spend.kind).toBe('number')
    expect(got.monthly_spend.hint).toMatch(/from 10 to 57\.5/)
    expect(got.signup_date.kind).toBe('date')
    expect(got.plan.kind).toBe('category')
    expect(got.plan.hint).toMatch(/3 different values/)
    expect(got.comment.kind).toBe('text')
    expect(got.comment.hint).toMatch(/free text/i)
  })

  it('takes a column named like an ID for one, even when it repeats and holds numbers', () => {
    expect(profileColumn('product_id', ['3', '3', '7', '12', '7', '3']).kind).toBe('id')
    // Unnamed, unique codes such as "INV-1001" are IDs too; plain unique numbers are not.
    expect(profileColumn('ref', ['INV-1001', 'INV-1002', 'INV-1003', 'INV-1004', 'INV-1005']).kind).toBe('id')
    expect(profileColumn('amount', ['10', '11', '12', '13', '14']).kind).toBe('number')
  })

  it('reads rows as maps too, and says when a column is mostly empty or one value', () => {
    const rows = Array.from({ length: 10 }, (_, i) => ({ region: 'EU', score: i < 6 ? null : i }))
    const [region, score] = sampleInsights(['region', 'score'], rows)
    expect(region.constant).toBe(true)
    expect(region.hint).toMatch(/one value only/)
    expect(score.filled).toBeCloseTo(0.4)
    expect(score.hint).toMatch(/60% of these rows are empty/)
    expect(profileColumn('nothing', [null, '', '  ']).kind).toBe('empty')
  })

  it('guesses from a database type before any row is read', () => {
    expect(kindFromType('order_id', 'INTEGER')).toBe('id')
    expect(kindFromType('amount', 'numeric(10,2)')).toBe('number')
    expect(kindFromType('ordered_at', 'timestamp with time zone')).toBe('date')
    expect(kindFromType('paid', 'boolean')).toBe('yes_no')
    expect(kindFromType('country', 'varchar')).toBe('text')
  })
})

describe('which columns fit a kind of model', () => {
  it('finds the person, item, date and quantity of order lines for recommendations', () => {
    const cols = [
      { name: 'order_id', kind: 'id' as const }, { name: 'cust_no', kind: 'id' as const }, { name: 'sku', kind: 'text' as const },
      { name: 'ordered_at', kind: 'date' as const }, { name: 'qty', kind: 'number' as const }, { name: 'channel', kind: 'category' as const },
    ]
    const m = matchGuide(dataGuide('recommend'), cols)
    expect(m.map((x) => [x.column.key, x.match])).toEqual([['user', 'cust_no'], ['item', 'sku'], ['strength', 'qty'], ['time', 'ordered_at']])
    expect(m.filter((x) => x.required).every((x) => x.match)).toBe(true)
  })

  it('picks the answer to predict and leaves the rest to learn from', () => {
    const cols = [
      { name: 'customer_id', kind: 'id' as const }, { name: 'age', kind: 'number' as const },
      { name: 'plan', kind: 'category' as const }, { name: 'is_churned', kind: 'yes_no' as const },
    ]
    const m = matchGuide(dataGuide('classification'), cols)
    expect(m.find((x) => x.column.key === 'target')?.match).toBe('is_churned')
    expect(learnFrom(m, cols)).toEqual(['age', 'plan'])
    // Nothing named like an answer: a yes/no column is the likeliest one.
    expect(matchGuide(dataGuide('classification'), [{ name: 'paid', kind: 'yes_no' }]).find((x) => x.column.key === 'target')?.match).toBe('paid')
  })

  it('says when a required column is missing', () => {
    const m = matchGuide(dataGuide('regression'), [{ name: 'country', kind: 'category' }])
    expect(m.find((x) => x.column.key === 'target')).toMatchObject({ required: true, match: undefined })
  })
})
