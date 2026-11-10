import { describe, it, expect } from 'vitest'
import { unroutedBranchEdges } from '@/pages/workflows/WorkflowEditor/nodes/branchHandleId'

// A workflow saved before edges followed their case can still hold an edge on
// a case's old name: a switch whose case is APPROVED, with one edge labelled
// APPROVED and a hidden one labelled APPROVE. The server now refuses to save
// it, so the editor finds such edges when it opens the workflow.

const edge = (id: string, source: string, handle?: string, label?: string) => ({
  id,
  source,
  sourceHandle: handle,
  data: label === undefined ? {} : { label },
})

const nodes = [
  { id: 'sw', type: 'switch', data: { cases: [{ label: 'SUBMIT' }, { label: 'APPROVED' }, { label: '' }] } },
  { id: 'rt', type: 'router', data: { rules: '[{"label":"europe"}]' } },
  { id: 'map', type: 'mapping', data: {} },
]

const ids = (edges: any[]) => unroutedBranchEdges(nodes, edges).map((e) => e.id)

describe('edges whose label names no case or rule', () => {
  it('finds an edge left on a renamed case', () => {
    expect(ids([
      edge('e1', 'sw', 'SUBMIT', 'SUBMIT'),
      edge('e2', 'sw', 'APPROVE', 'APPROVE'),
      edge('e3', 'sw', 'APPROVED', 'APPROVED'),
    ])).toEqual(['e2'])
  })

  it('keeps unnamed cases, default, and unlabelled edges', () => {
    expect(ids([
      edge('e1', 'sw', 'case_2', 'case_2'),
      edge('e2', 'sw', 'default', 'default'),
      edge('e3', 'sw'),
    ])).toEqual([])
  })

  it('reads the label before the handle, as the engine does', () => {
    expect(ids([edge('e1', 'sw', 'APPROVED', 'APPROVE')])).toEqual(['e1'])
  })

  it('checks router rules kept as a JSON string', () => {
    expect(ids([edge('e1', 'rt', 'eu', 'eu'), edge('e2', 'rt', 'europe', 'europe')])).toEqual(['e1'])
  })

  it('leaves edges out of other nodes alone', () => {
    expect(ids([edge('e1', 'map', 'anything', 'anything')])).toEqual([])
  })
})
