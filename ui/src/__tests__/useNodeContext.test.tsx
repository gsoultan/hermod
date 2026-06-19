import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// The live message captured for a RabbitMQ source node, mirroring the shape the
// backend records in nodeSamples (see internal/engine/registry getConsistentData).
const liveSample = {
  after: {
    created_at: '2026-06-19 01:53:01.228709+00',
    feature: 'AUTHENTICATION',
    id: '019edd94-dd24-75f3-ad51-fc736a4b8bb2',
    is_valid: true,
    name: 'CREATE_SESSIONS',
    session_id: '',
  },
  id: 'C/92640EB8',
  metadata: {
    _hermod_lineage: 'workflow_start',
    _source_node_id: 'node_1781254583417',
  },
  operation: 'create',
  schema: 'audit',
  table: 'events',
}

const resetStore = (nodes: Node[], edges: Edge[], nodeSamples: Record<string, any>) => {
  useWorkflowStore.setState({ nodes, edges, nodeSamples })
}

describe('useNodeContext live sample fallback', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {} })
  })

  it('derives available fields from live nodeSamples for a source node', () => {
    const sourceNode = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    resetStore([sourceNode], [], { src: liveSample })

    const { result } = renderHook(() => useNodeContext(sourceNode, null, [], []))

    expect(result.current.availableFields).toContain('after')
    // CDC "after" fields must be hoisted to the root so forms can reference them.
    expect(result.current.availableFields).toContain('feature')
    expect(result.current.availableFields).toContain('id')
    expect(result.current.availableFields).toContain('table')
  })

  it('derives upstream available fields from live nodeSamples for a downstream node', () => {
    const sourceNode = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
    const sinkNode = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    const edge = { id: 'e1', source: 'src', target: 'snk' } as Edge
    resetStore([sourceNode, sinkNode], [edge], { src: liveSample })

    const { result } = renderHook(() => useNodeContext(sinkNode, null, [], []))

    expect(result.current.availableFields).toContain('feature')
    expect(result.current.availableFields).toContain('table')
  })

  it('returns empty fields when no sample data is available', () => {
    const sinkNode = { id: 'snk', type: 'sink', data: { ref_id: 'r2' } } as unknown as Node
    resetStore([sinkNode], [], {})

    const { result } = renderHook(() => useNodeContext(sinkNode, null, [], []))

    expect(result.current.availableFields).toEqual([])
  })
})
