import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// With no run to read from, a node's input falls back to the nearest payload up
// the graph -- usually the source's sample -- and every node in between is
// skipped: its changes are not in what the node is tested and previewed on. The
// count of those nodes is what lets the editor say so.

const sample = JSON.stringify({ operation: 'snapshot', after: { user_id: 'u-1', scheduled_at: '2026-09-27T22:00:00Z' } })
const sources = [{ id: 'src-rec', sample }]

const src = { id: 'src', type: 'source', data: { ref_id: 'src-rec' } } as unknown as Node
const conv = { id: 'conv', type: 'transformation', data: { transType: 'data_conversion' } } as unknown as Node
const lookup = { id: 'lookup', type: 'transformation', data: { transType: 'api_lookup' } } as unknown as Node
const edges = [
  { id: 'e1', source: 'src', target: 'conv' },
  { id: 'e2', source: 'conv', target: 'lookup' },
] as Edge[]

describe('useNodeContext inputSkipped', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [src, conv, lookup], edges, nodeSamples: {} })
  })

  it('counts the nodes between the sample and this node when nothing has run', () => {
    const { result } = renderHook(() => useNodeContext(lookup, null, sources, []))
    expect(result.current.incomingPayload?.user_id).toBe('u-1')
    expect(result.current.inputSkipped).toBe(1)
  })

  it('is zero once the node before this one has run', () => {
    const run = [
      { node_id: 'src', payload: JSON.parse(sample) },
      { node_id: 'conv', payload: { operation: 'snapshot', after: { user_id: 'u-1', scheduled_at: '2026-09-28' } } },
    ]
    const { result } = renderHook(() => useNodeContext(lookup, run, sources, []))
    expect(result.current.incomingPayload?.scheduled_at).toBe('2026-09-28')
    expect(result.current.inputSkipped).toBe(0)
  })

  it('is zero for a node fed straight by the source', () => {
    const { result } = renderHook(() => useNodeContext(conv, null, sources, []))
    expect(result.current.inputSkipped).toBe(0)
  })
})
