import { describe, it, expect } from 'vitest'
import { simulateTransformation } from '@/utils/transformationUtils'
import { formatValue, parseValue } from '@/lib/valueExpression'

// A Switch case value is a template whose field token is {{path}}; the
// shared value model reads and writes it without changing its shape.
const CASE_TOKENS = { field: (path: string) => `{{${path}}}` }

describe('case values keep their saved shape', () => {
  it.each(['active', '{{customer.region}}', '{{lower(source.region)}}', '{{upper("eu")}}', '{{abs(-5)}}', '{{lower(upper(source.a))}}'])(
    'round-trips %s',
    (saved) => {
      expect(formatValue(parseValue(saved, 'token')!, 'token', CASE_TOKENS)).toBe(saved)
    },
  )

  it('round-trips a switch field', () => {
    for (const saved of ['status', 'lower(source.status)', 'concat(source.a, source.b)']) {
      expect(formatValue(parseValue(saved, 'field')!, 'field')).toBe(saved)
    }
  })
})

describe('switch preview with functions', () => {
  it('applies the function on both sides', () => {
    const res = simulateTransformation(
      'switch',
      { field: 'lower(source.status)', cases: [{ label: 'hit', operator: '=', value: '{{lower(source.expected)}}' }] },
      { status: 'ACTIVE', expected: 'Active' },
    )
    expect(res.metadata.matchedLabel).toBe('hit')
  })

  it.each([
    ['len', '{{len("abcd")}}', 4],
    ['floor', '{{floor(4.7)}}', 4],
    ['ceil', '{{ceil(3.2)}}', 4],
    ['normalize_space', '{{normalize_space("a   b")}}', 'a b'],
  ])('runs %s', (_fn, value, amount) => {
    const res = simulateTransformation('switch', { field: 'amount', cases: [{ label: 'hit', operator: '=', value }] }, { amount })
    expect(res.metadata.matchedLabel).toBe('hit')
  })
})
