import { describe, it, expect } from 'vitest'
import {
  parseCaseValue,
  formatCaseValue,
  parseSwitchField,
  formatSwitchField,
} from '@/components/workflow/Transformation/configs/logic/switchOperand'
import { simulateTransformation } from '@/utils/transformationUtils'

describe('case value operand', () => {
  it.each([
    ['plain text', 'active', { fn: '', source: 'value', text: 'active' }],
    ['a number', 100, { fn: '', source: 'value', text: '100' }],
    ['a field', '{{customer.region}}', { fn: '', source: 'field', text: 'customer.region' }],
    ['a dotted template field', '{{.limit}}', { fn: '', source: 'field', text: 'limit' }],
    ['a function of a field', '{{lower(source.region)}}', { fn: 'lower', source: 'field', text: 'region' }],
    ['a function of text', '{{upper("eu")}}', { fn: 'upper', source: 'value', text: 'eu' }],
    ['a function of a number', '{{abs(-5)}}', { fn: 'abs', source: 'value', text: '-5' }],
  ])('reads %s', (_name, saved, want) => {
    expect(parseCaseValue(saved)).toEqual(want)
  })

  it.each([
    '{{concat(source.a, source.b)}}',
    '{{lower(upper(source.a))}}',
    'id-{{limit}}',
    '{{unknown_fn(source.a)}}',
  ])('keeps %s as the text it was saved as', (saved) => {
    const op = parseCaseValue(saved)
    expect(op).toEqual({ fn: '', source: 'value', text: saved })
    expect(formatCaseValue(op)).toBe(saved)
  })

  it.each([
    [{ fn: '', source: 'value', text: 'active' }, 'active'],
    [{ fn: '', source: 'field', text: 'limit' }, '{{limit}}'],
    [{ fn: 'lower', source: 'field', text: 'region' }, '{{lower(source.region)}}'],
    [{ fn: 'upper', source: 'value', text: 'eu' }, '{{upper("eu")}}'],
    [{ fn: 'trim', source: 'value', text: 'say "hi"' }, `{{trim('say "hi"')}}`],
    [{ fn: 'lower', source: 'field', text: '' }, ''],
  ] as const)('writes %o as %s', (op, want) => {
    expect(formatCaseValue(op)).toBe(want)
  })
})

describe('switch field operand', () => {
  it('reads and writes a function of a field', () => {
    expect(parseSwitchField('lower(source.status)')).toEqual({ fn: 'lower', source: 'field', text: 'status' })
    expect(formatSwitchField({ fn: 'lower', source: 'field', text: 'status' })).toBe('lower(source.status)')
  })

  it('leaves a plain field and a hand-written expression as saved', () => {
    expect(parseSwitchField('status')).toEqual({ fn: '', source: 'field', text: 'status' })
    const custom = 'concat(source.a, source.b)'
    expect(formatSwitchField(parseSwitchField(custom))).toBe(custom)
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
