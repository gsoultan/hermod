/// <reference types="node" />
// node builtins are referenced here rather than added to tsconfig.app.json's
// `types`, for the reason matchesConditionParity.test.ts gives.

import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect } from 'vitest'
import { branchHandleId } from '@/pages/workflows/WorkflowEditor/nodes/branchHandleId'

// The cases are read from the file the Go test reads, so a change on either side
// fails on the other. Walking up from cwd works from the repository root and
// from ui/ alike.
const FIXTURE_REL = 'internal/engine/registry/nodes/control/testdata/branch_names.json'

function findFixture(): string {
  let dir = process.cwd()
  for (let i = 0; i < 6; i++) {
    const candidate = resolve(dir, FIXTURE_REL)
    if (existsSync(candidate)) return candidate
    dir = resolve(dir, '..')
  }
  throw new Error(
    `could not find ${FIXTURE_REL} walking up from ${process.cwd()}. ` +
      'It is the branch-name contract, read by the Go test too; if it moved, both readers move.',
  )
}

type BranchCase = { node: 'router' | 'switch'; label: string; index: number; want: string }

const fixture: { cases: BranchCase[] } = JSON.parse(readFileSync(findFixture(), 'utf8'))

// An edge drawn from a rule's or case's handle carries the handle's id as its
// label, and the engine sends a message down the edges labelled with the branch
// it chose. An unnamed rule's handle was rule_N while the engine chose the empty
// branch, and an empty branch takes every edge: a message matching that rule
// went down every route out of the node.
describe("the editor's handle ids are the engine's branch names", () => {
  it('has cases to check', () => {
    expect(fixture.cases.length).toBeGreaterThan(0)
  })

  it.each(fixture.cases)('$node rule $index labelled "$label" is drawn as $want', ({ node, label, index, want }) => {
    expect(branchHandleId(node, label, index)).toBe(want)
  })
})
