/// <reference types="node" />
// node builtins are referenced here rather than added to tsconfig.app.json's
// `types`, for the reason matchesConditionParity.test.ts gives.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, it, expect } from 'vitest'
import { parseAndEvaluate } from '@/utils/transformationUtils'

// Every file in pkg/infra/evaluator/testdata/functions is one expression
// function's contract, read here and by
// pkg/infra/evaluator/function_fixture_test.go, so a case added there runs here
// too and a change on either side fails on the other. The editor evaluates
// expressions itself -- a condition value holding {{ ... }} is resolved in the
// browser for the Test button -- so a function the two sides disagree on
// previews one answer and ships another.
const FIXTURE_DIR_REL = 'pkg/infra/evaluator/testdata/functions'

function findFixtureDir(): string {
  let dir = process.cwd()
  for (let i = 0; i < 6; i++) {
    const candidate = resolve(dir, FIXTURE_DIR_REL)
    if (existsSync(candidate)) return candidate
    dir = resolve(dir, '..')
  }
  throw new Error(
    `could not find ${FIXTURE_DIR_REL} walking up from ${process.cwd()}. ` +
      'It holds the shared function contracts, read by the Go test too; if it moved, both readers move.',
  )
}

type FunctionFixture = {
  source?: Record<string, unknown>
  cases: { name: string; expr: string; want: unknown }[]
}

const dir = findFixtureDir()
const files = readdirSync(dir).filter((f) => f.endsWith('.json')).sort()

describe('expression functions agree with the Go evaluator', () => {
  it('there are fixtures to run', () => {
    expect(files.length).toBeGreaterThan(0)
  })

  describe.each(files)('%s', (file) => {
    const fixture: FunctionFixture = JSON.parse(readFileSync(join(dir, file), 'utf8'))

    // An emptied file would pass by having nothing to run.
    it('has cases', () => {
      expect(fixture.cases.length).toBeGreaterThan(0)
    })

    it.each(fixture.cases.map((c) => [c.name, c.expr, c.want] as const))('%s', (_name, expr, want) => {
      // toStrictEqual, so undefined does not pass for null.
      expect(parseAndEvaluate(expr, fixture.source ?? {})).toStrictEqual(want)
    })
  })
})
