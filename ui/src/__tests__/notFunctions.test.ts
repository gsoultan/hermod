import { describe, expect, it } from 'vitest'
import { notFunctions } from '@/lib/functionCatalog'

// Each case is one the engine's test runs too
// (pkg/infra/evaluator/function_catalog_test.go, TestAValueThatCallsNoFunction):
// what is flagged here, and what the flag says happens, is what happens there.
describe('notFunctions', () => {
  it('names a call to a function the engine does not have', () => {
    expect(notFunctions('nwo()')).toEqual([{ name: 'nwo', becomes: 'nothing' }])
    expect(notFunctions('Paris (France)')).toEqual([{ name: 'Paris', becomes: 'nothing' }])
  })

  it('finds one inside another call', () => {
    expect(notFunctions('upper(nwo(source.name))')).toEqual([{ name: 'nwo', becomes: 'nothing' }])
  })

  // The engine takes a name with a dot in it for text, brackets and all.
  it('says a dotted name is written as text, and suggests the function meant', () => {
    expect(notFunctions('time.now()')).toEqual([{ name: 'time.now', becomes: 'text', suggestion: 'now' }])
    expect(notFunctions('strings.ToUpper(source.name)')).toEqual([{ name: 'strings.ToUpper', becomes: 'text' }])
  })

  it('suggests a function by another name for it', () => {
    expect(notFunctions('uppercase(source.name)')).toEqual([{ name: 'uppercase', becomes: 'nothing', suggestion: 'upper' }])
  })

  it('has nothing to say about a value the engine evaluates', () => {
    for (const value of [
      '',
      'source.name',
      '42',
      'true',
      'plain text',
      'upper(trim(source.name))',
      'toInt(source.total)',
      'TOINT(source.total)',
      "concat(source.a, ' (', source.b, ')')",
      'env("API_KEY")',
      'Hello {{upper(source.name)}}',
      'Hello {{.after.name}}',
    ]) {
      expect(notFunctions(value), value).toEqual([])
    }
  })

  it('leaves text alone that only holds brackets', () => {
    expect(notFunctions('see (note) below')).toEqual([])
    expect(notFunctions("'Paris (France)'")).toEqual([])
  })

  it('reads a template token by token', () => {
    expect(notFunctions('at {{nwo()}}')).toEqual([{ name: 'nwo', becomes: 'nothing' }])
    expect(notFunctions('at {{time.now()}}')).toEqual([{ name: 'time.now', becomes: 'text', suggestion: 'now' }])
    expect(notFunctions('Paris (France) {{source.name}}')).toEqual([])
  })
})
