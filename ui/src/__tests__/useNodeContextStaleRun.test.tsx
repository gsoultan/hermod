import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// A Test run is read ahead of everything else for a node's input. After the
// run, a node before this one can be reconfigured, or the wiring between them
// changed, and then the run's step for that node describes a message the
// workflow no longer produces. The canvas already greys such a run out
// (simulationOutdated); the field list and the Live Preview kept reading it,
// so a field renamed upstream stayed in Available Fields under its old name
// and the preview ran on the old input.

const sample = { code: 'C-1' }
const sources = [{ id: 'src-rec', sample: JSON.stringify(sample) }]

const src = { id: 'src', type: 'source', data: { ref_id: 'src-rec' } } as unknown as Node
const setX = { id: 'a', type: 'transformation', data: { transType: 'set', targetField: 'x' } } as unknown as Node
const next = { id: 'b', type: 'transformation', data: { transType: 'data_conversion' } } as unknown as Node
const edges = [
  { id: 'e1', source: 'src', target: 'a' },
  { id: 'e2', source: 'a', target: 'b' },
] as Edge[]

const run = [
  { node_id: 'src', payload: sample },
  { node_id: 'a', payload: { code: 'C-1', x: 'old' } },
]

const paths = (fields: { path: string }[]) => fields.map((f) => f.path)

describe('useNodeContext after the graph changed since the run', () => {
  beforeEach(() => {
    useWorkflowStore.setState({
      nodes: [src, setX, next],
      edges,
      nodeSamples: {},
      nodePreviews: {},
      testResults: run,
      testRunGraph: { nodes: [src, setX, next], edges },
    })
  })

  it('reads the run while nothing has changed', () => {
    const { result } = renderHook(() => useNodeContext(next, run, sources, []))
    expect(result.current.incomingPayload?.x).toBe('old')
    expect(paths(result.current.availableFields)).toContain('x')
    expect(result.current.inputSkipped).toBe(0)
  })

  it('drops the step of a node reconfigured since the run, and of everything after it', () => {
    const setY = { ...setX, data: { transType: 'set', targetField: 'y' } } as unknown as Node
    useWorkflowStore.setState({ nodes: [src, setY, next] })

    const { result } = renderHook(() => useNodeContext(next, run, sources, []))
    // The input falls back to the source's sample: the old output is gone...
    expect(result.current.incomingPayload).toEqual(sample)
    expect(paths(result.current.availableFields)).not.toContain('x')
    // ...the node's new target is offered from its configuration...
    expect(paths(result.current.availableFields)).toContain('y')
    // ...and the editor says the node in between is not in the input.
    expect(result.current.inputSkipped).toBe(1)
  })

  it('drops the step of a node whose input edge was rewired since the run', () => {
    // b now reads straight from the source; a's step is still valid for a.
    const rewired = [
      { id: 'e1', source: 'src', target: 'a' },
      { id: 'e3', source: 'src', target: 'b' },
    ] as Edge[]
    useWorkflowStore.setState({ edges: rewired })

    const { result: forB } = renderHook(() => useNodeContext(next, run, sources, []))
    expect(forB.current.incomingPayload).toEqual(sample)
    expect(paths(forB.current.availableFields)).not.toContain('x')

    const { result: forA } = renderHook(() => useNodeContext(setX, run, sources, []))
    expect(forA.current.incomingPayload).toEqual(sample)
  })

  it('keeps reading the run when it does not know what graph the run was on', () => {
    useWorkflowStore.setState({ nodes: [src, { ...setX, data: { transType: 'set', targetField: 'y' } } as unknown as Node, next], testRunGraph: null })
    const { result } = renderHook(() => useNodeContext(next, run, sources, []))
    expect(result.current.incomingPayload?.x).toBe('old')
  })
})
