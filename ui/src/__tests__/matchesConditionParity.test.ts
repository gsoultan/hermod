/// <reference types="node" />
// node builtins are referenced here rather than added to tsconfig.app.json's
// `types`: that array is what keeps application code from reaching for fs, and
// widening it for one test file would spend that guarantee.

import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect } from 'vitest'
import { matchesCondition } from '@/utils/transformationUtils'

// The cases are not written here. They are read from the file the Go test
// reads, so adding one is a single edit and a change on either side fails on
// the other.
//
// Read with fs rather than imported: ui/tsconfig.app.json includes only `src`,
// so an import from outside it would sit outside the typecheck. Reaching for
// the path explicitly also makes the dependency visible -- this file is not
// self-contained on purpose.
//
// Walking up from cwd rather than from import.meta.url, which vitest does not
// hand over as a file: URL, and rather than assuming a cwd, so this works from
// the repository root and from ui/ alike.
const FIXTURE_REL = 'pkg/infra/evaluator/testdata/condition_cases.json'

function findFixture(): string {
  let dir = process.cwd()
  for (let i = 0; i < 6; i++) {
    const candidate = resolve(dir, FIXTURE_REL)
    if (existsSync(candidate)) return candidate
    dir = resolve(dir, '..')
  }
  throw new Error(
    `could not find ${FIXTURE_REL} walking up from ${process.cwd()}. ` +
      'It is the shared condition contract, read by the Go test too; if it moved, both readers move.',
  )
}

type OperatorCase = { operator: string; field: unknown; value: unknown }
type NumericCase = { name: string; field: unknown; value: string; equal: boolean }
type WideCase = { operator: string; value: string; expected: boolean }

const fixture: {
  operators: { matching: OperatorCase[]; notMatching: OperatorCase[] }
  numericEquality: { cases: NumericCase[] }
  wideInteger: { field: unknown; cases: WideCase[] }
} = JSON.parse(readFileSync(findFixture(), 'utf8'))

const check = (field: unknown, operator: string, value: unknown) =>
  matchesCondition({ f: field }, { field: 'f', operator, value } as never)

// matchesCondition is the editor's client-side twin of the engine's
// EvaluateConditions: the Test button, the switch preview and the filter
// preview all run it. When the two disagree the preview lies, which is worse
// than no preview -- the user tunes a condition until the editor says it
// matches, then ships something that does not.
//
// These cases are the Go table in pkg/infra/evaluator/condition_number_shape_test.go,
// transcribed. Change one side and this fails.
describe('matchesCondition agrees with the Go evaluator', () => {
  // Guards the fixture itself. An emptied file would let every case below pass
  // by having nothing to run, which is the quietest way for a shared contract
  // to stop being one. Mirrors TestTheConditionFixtureIsNotEmpty.
  it('the shared fixture actually has cases', () => {
    expect(fixture.operators.matching.length).toBeGreaterThanOrEqual(16)
    expect(fixture.operators.notMatching.length).toBeGreaterThan(0)
    expect(fixture.numericEquality.cases.length).toBeGreaterThanOrEqual(13)
    expect(fixture.wideInteger.cases.length).toBeGreaterThan(0)
  })

  describe('every operator the editor offers is implemented', () => {
    it.each(fixture.operators.matching.map((c) => [c.operator, c.field, c.value] as const))(
      '%s matches',
      (operator, field, value) => {
        expect(check(field, operator, value)).toBe(true)
      },
    )

    it.each(fixture.operators.notMatching.map((c) => [c.operator, c.field, c.value] as const))(
      '%s is a real negation, not a constant true',
      (operator, field, value) => {
        expect(check(field, operator, value)).toBe(false)
      },
    )
  })

  describe('a number equals its other spellings', () => {
    it.each(fixture.numericEquality.cases.map((c) => [c.name, c.field, c.value, c.equal] as const))(
      '%s',
      (_name, field, value, equal) => {
        expect(check(field, '=', value)).toBe(equal)
        // != must be the exact negation, or a config can satisfy both.
        expect(check(field, '!=', value)).toBe(!equal)
      },
    )
  })

  describe('a wide integer compares as the digits the user was shown', () => {
    it.each(
      fixture.wideInteger.cases.map((c) => [c.operator, c.value, c.expected] as const),
    )('%s %s -> %s', (operator, value, expected) => {
      expect(check(fixture.wideInteger.field, operator, value)).toBe(expected)
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
