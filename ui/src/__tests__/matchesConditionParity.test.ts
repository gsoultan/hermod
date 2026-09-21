import { describe, it, expect } from 'vitest'
import { matchesCondition } from '@/utils/transformationUtils'

// matchesCondition is the editor's client-side twin of the engine's
// EvaluateConditions: the Test button, the switch preview and the filter
// preview all run it. When the two disagree the preview lies, which is worse
// than no preview -- the user tunes a condition until the editor says it
// matches, then ships something that does not.
//
// These cases are the Go table in pkg/infra/evaluator/condition_number_shape_test.go,
// transcribed. Change one side and this fails.
describe('matchesCondition agrees with the Go evaluator', () => {
  describe('every operator the editor offers is implemented', () => {
    const cases: [string, any, any, boolean][] = [
      ['=', 'abc', 'abc', true],
      ['!=', 'abc', 'zzz', true],
      ['>', 10, 5, true],
      ['>=', 10, 5, true],
      ['<', 5, 10, true],
      ['<=', 5, 10, true],
      ['contains', 'abc', 'b', true],
      ['not_contains', 'abc', 'zzz', true],
      ['regex', 'abc', '^a', true],
      ['not_regex', 'abc', 'zzz', true],
      // Aliases: stored configs and the API use these spellings.
      ['eq', 'abc', 'abc', true],
      ['neq', 'abc', 'zzz', true],
      ['gt', 10, 5, true],
      ['gte', 10, 5, true],
      ['lt', 5, 10, true],
      ['lte', 5, 10, true],
    ]

    it.each(cases)('%s matches', (operator, field, value, expected) => {
      expect(matchesCondition({ f: field }, { field: 'f', operator, value })).toBe(expected)
    })

    it.each([
      ['not_contains', 'abc', 'b'],
      ['not_regex', 'abc', '^a'],
      ['neq', 'abc', 'abc'],
    ] as [string, any, any][])('%s is a real negation, not a constant false', (operator, field, value) => {
      expect(matchesCondition({ f: field }, { field: 'f', operator, value })).toBe(false)
    })
  })

  describe('a number equals its other spellings', () => {
    it.each([
      [100, '100.00', true],
      [100.5, '100.50', true],
      [100, '0100', true],
      [100, '+100', true],
      [100, '1e2', true],
      [100, '100', true],
      [100, '101', false],
      [100, 'abc', false],
      // A string field keeps string equality, so an id does not lose its zeros.
      ['007', '7', false],
      ['007', '007', true],
      ['100.0', '100.00', false],
      [true, '1', false],
      [0, '', false],
    ] as [any, string, boolean][])('%s = %s -> %s', (field, value, expected) => {
      expect(matchesCondition({ f: field }, { field: 'f', operator: '=', value })).toBe(expected)
      expect(matchesCondition({ f: field }, { field: 'f', operator: '!=', value })).toBe(!expected)
    })
  })

  describe('a wide integer compares as the digits the user was shown', () => {
    const id = 1704207845
    it.each([
      ['=', '1704207845', true],
      ['=', '1704207846', false],
      ['contains', '042078', true],
      ['contains', 'e+09', false],
      ['regex', '^1704', true],
      ['regex', '^\\d+$', true],
      ['>', '1704207844', true],
      ['<', '1704207846', true],
    ] as [string, string, boolean][])('%s %s -> %s', (operator, value, expected) => {
      expect(matchesCondition({ id }, { field: 'id', operator, value })).toBe(expected)
    })
  })

  // Number() turns null, '', false and [] all into 0; Go's ToFloat64 converts
  // none of them. An absent field compared greater than -1 here and not in the
  // engine.
  describe('an absent field is not zero', () => {
    it.each([
      [{}, '>', '-1'],
      [{ f: null }, '>', '-1'],
      [{ f: '' }, '>', '-1'],
    ] as [any, string, string][])('%o %s %s is false', (payload, operator, value) => {
      expect(matchesCondition(payload, { field: 'f', operator, value })).toBe(false)
    })

    // Where neither side is a number the engine compares the text, and these
    // are what that comes to. Nonsense comparisons, but both sides must agree
    // on the same nonsense.
    it.each([
      [{ f: true }, '>', '0', true],
      [{ f: [] }, '>', '-1', true],
    ] as [any, string, string, boolean][])('%o %s %s is %s', (payload, operator, value, expected) => {
      expect(matchesCondition(payload, { field: 'f', operator, value })).toBe(expected)
    })
  })

  // An array or object field is searched as JSON, with object keys sorted, so
  // it matches what Go's json.Marshal produces and what the sample panel shows.
  describe('a composite field is searched as JSON', () => {
    it.each([
      [{ f: [1, 2] }, 'contains', '[1,2]', true],
      [{ f: ['a', 'b'] }, 'contains', '["a","b"]', true],
      [{ f: { a: 1 } }, 'contains', '{"a":1}', true],
      [{ f: { b: 2, a: 1 } }, '=', '{"a":1,"b":2}', true],
      [{ f: { nested: { z: 1, y: 2 } } }, '=', '{"nested":{"y":2,"z":1}}', true],
      [{ f: { status: 'ok', amount: 1704207845 } }, 'contains', '"amount":1704207845', true],
      [{ f: { a: 1 } }, 'contains', 'map[', false],
      [{ f: { a: 1 } }, 'contains', '[object Object]', false],
    ] as [any, string, string, boolean][])('%o %s %s -> %s', (payload, operator, value, expected) => {
      expect(matchesCondition(payload, { field: 'f', operator, value })).toBe(expected)
    })
  })

  // RFC1123 leads with the weekday, so its text order is not its time order.
  // The engine parses it; comparing the text sorted "Fri" before "Mon".
  describe('an RFC1123 date compares chronologically', () => {
    const earlier = 'Mon, 02 Jan 2024 15:04:05 GMT'
    const later = 'Fri, 06 Dec 2024 15:04:05 GMT'
    it('orders by time, not by weekday name', () => {
      expect(matchesCondition({ f: later }, { field: 'f', operator: '>', value: earlier })).toBe(true)
      expect(matchesCondition({ f: earlier }, { field: 'f', operator: '>', value: later })).toBe(false)
    })
  })

  // The ISO formats are zero-padded and big-endian, so their text order is
  // already their time order and both sides agree without parsing anything.
  describe('an ISO date compares chronologically', () => {
    it.each([
      ['2024-12-06T15:04:05Z', '2024-01-02T15:04:05Z', true],
      ['2024-01-02 15:04:05', '2024-01-02 09:00:00', true],
      ['2024-01-02', '2024-12-06', false],
    ] as [string, string, boolean][])('%s > %s -> %s', (field, value, expected) => {
      expect(matchesCondition({ f: field }, { field: 'f', operator: '>', value })).toBe(expected)
    })
  })
})
