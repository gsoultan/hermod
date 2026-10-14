import { describe, it, expect } from 'vitest'
import {
  parseExpression,
  formatExpression,
  parseValue,
  formatValue,
  type ValueNode,
} from '@/lib/valueExpression'

const field = (path: string): ValueNode => ({ kind: 'field', path })
const secret = (name: string): ValueNode => ({ kind: 'secret', name })
const env = (name: string): ValueNode => ({ kind: 'secret', name, env: true })
const text = (value: string, bare = false): ValueNode => (bare ? { kind: 'text', value, bare } : { kind: 'text', value })
const call = (fn: string, ...args: ValueNode[]): ValueNode => ({ kind: 'call', fn, args })

describe('an expression read into a tree', () => {
  it.each<[string, string, ValueNode]>([
    ['a field', 'source.customer.email', field('customer.email')],
    ['a secret', 'secret.API_KEY', secret('API_KEY')],
    ['an env variable', 'env.REGION', env('REGION')],
    ['the older secret spelling', 'secret("API_KEY")', secret('API_KEY')],
    ['the older env spelling', "env('REGION')", env('REGION')],
    ['quoted text', '"eu-west"', text('eu-west')],
    ['single-quoted text', "' '", text(' ')],
    ['a number', '-5', text('-5', true)],
    ['a boolean', 'true', text('true', true)],
    ['a call', 'lower(source.email)', call('lower', field('email'))],
    ['a call with no arguments', 'now()', call('now')],
    [
      'a nested call',
      'upper(trim(coalesce(source.nickname, source.first_name, "friend")))',
      call('upper', call('trim', call('coalesce', field('nickname'), field('first_name'), text('friend')))),
    ],
    [
      'commas and brackets inside quotes',
      'concat(source.a, ", (", secret.SUFFIX)',
      call('concat', field('a'), text(', ('), secret('SUFFIX')),
    ],
  ])('reads %s', (_name, expr, want) => {
    expect(parseExpression(expr)).toEqual(want)
  })

  it('writes an older spelling in the standard one', () => {
    expect(formatExpression(parseExpression('concat(secret("A"), env("B"))')!)).toBe('concat(secret.A, env.B)')
  })

  it.each(['source.a + 1', 'lower(source.a', 'time.now()', 'Paris', ''])('cannot read %j', (expr) => {
    expect(parseExpression(expr)).toBeNull()
  })

  it.each([
    'lower(source.email)',
    'upper(trim(coalesce(source.nickname, source.first_name, "friend")))',
    'concat(source.a, ", (", secret.SUFFIX)',
    'upper(env.REGION)',
    'round(source.total, 2)',
    'now()',
  ])('writes %s back as it was read', (expr) => {
    expect(formatExpression(parseExpression(expr)!)).toBe(expr)
  })

  it('quotes text holding a double quote with single quotes', () => {
    expect(formatExpression(call('trim', text('say "hi"')))).toBe(`trim('say "hi"')`)
  })

  it('writes a placeholder for an argument nobody filled in yet', () => {
    expect(formatExpression(call('lower', field('')))).toBe('lower(source.)')
  })
})

describe('a template value', () => {
  it.each<[string, string, ValueNode]>([
    ['plain text', 'Order confirmed', text('Order confirmed')],
    ['a field token', '{{.customer.email}}', field('customer.email')],
    ['a field token without the dot', '{{limit}}', field('limit')],
    ['a source field token', '{{source.limit}}', field('limit')],
    ['a secret token', '{{secret.API_KEY}}', secret('API_KEY')],
    ['an env token', '{{ env.REGION }}', env('REGION')],
    ['the older secret token', '{{secret("API_KEY")}}', secret('API_KEY')],
    ['the whole-value secret spelling', '{{secret:API_KEY}}', secret('API_KEY')],
    // A leading dot is a field of the record, as {{.after.id}} is.
    ['a record field named secret', '{{.secret.API_KEY}}', field('secret.API_KEY')],
    ['a call token', '{{lower(source.email)}}', call('lower', field('email'))],
    [
      'a nested call token',
      '{{upper(coalesce(source.a, secret.B))}}',
      call('upper', call('coalesce', field('a'), secret('B'))),
    ],
  ])('reads %s', (_name, saved, want) => {
    expect(parseValue(saved, 'token')).toEqual(want)
  })

  it.each(['Hello {{.name}}', '{{.a}}{{.b}}', '{{a + b}}'])('leaves %j to be edited as text', (saved) => {
    expect(parseValue(saved, 'token')).toBeNull()
  })

  it.each<[ValueNode, string]>([
    [text('Order confirmed'), 'Order confirmed'],
    [field('customer.email'), '{{.customer.email}}'],
    [secret('API_KEY'), '{{secret.API_KEY}}'],
    [env('REGION'), '{{env.REGION}}'],
    [call('lower', field('email')), '{{lower(source.email)}}'],
    [call('upper', call('trim', secret('X'))), '{{upper(trim(secret.X))}}'],
  ])('writes %j as %s', (node, want) => {
    expect(formatValue(node, 'token')).toBe(want)
  })

  it('writes the field and secret tokens the input asks for', () => {
    const tokens = { field: (p: string) => `{{${p}}}`, secret: (n: string) => `secret:${n}` }
    expect(formatValue(field('limit'), 'token', tokens)).toBe('{{limit}}')
    expect(formatValue(secret('K'), 'token', tokens)).toBe('secret:K')
    // Inside a call the expression spelling is the only one the engine reads.
    expect(formatValue(call('lower', field('limit')), 'token', tokens)).toBe('{{lower(source.limit)}}')
  })

  it('reads a secret written the way the input asks for', () => {
    expect(parseValue('secret:K', 'token')).toEqual(secret('K'))
  })
})

describe('an expression value (Set Fields)', () => {
  it('reads a bare call', () => {
    expect(parseValue('lower(source.email)', 'expression')).toEqual(call('lower', field('email')))
  })

  it('reads bare text as text', () => {
    expect(parseValue('pending', 'expression')).toEqual(text('pending'))
  })

  it('writes a call bare and a field as a token', () => {
    expect(formatValue(call('lower', field('email')), 'expression')).toBe('lower(source.email)')
    expect(formatValue(field('email'), 'expression')).toBe('{{.email}}')
    expect(formatValue(secret('K'), 'expression')).toBe('{{secret.K}}')
  })

  it('reads a bare secret.NAME, which the engine reads as one', () => {
    expect(parseValue('secret.K', 'expression')).toEqual(secret('K'))
    expect(parseValue('env.REGION', 'expression')).toEqual(env('REGION'))
  })
})

describe('a field-or-expression value (a condition field)', () => {
  it('reads a bare path as a field', () => {
    expect(parseValue('status', 'field')).toEqual(field('status'))
    expect(parseValue('lower(source.status)', 'field')).toEqual(call('lower', field('status')))
  })

  it('writes a field as a bare path and a call bare', () => {
    expect(formatValue(field('status'), 'field')).toBe('status')
    expect(formatValue(call('lower', field('status')), 'field')).toBe('lower(source.status)')
    expect(formatValue(text('x'), 'field')).toBe('"x"')
  })

  // There a bare dotted name is a record field, so a secret on its own keeps
  // the function spelling; inside a call it is an argument like any other.
  it('writes a secret so it is not read as a field', () => {
    expect(formatValue(secret('K'), 'field')).toBe('secret("K")')
    expect(formatValue(env('R'), 'field')).toBe('env("R")')
    expect(parseValue('secret.K', 'field')).toEqual(field('secret.K'))
    expect(formatValue(call('lower', secret('K')), 'field')).toBe('lower(secret.K)')
  })
})
