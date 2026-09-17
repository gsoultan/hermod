import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// The exact JSON a batch_sql source's sample serialises to, taken from
// BatchSQLSource.Sample marshalled through DefaultMessage.MarshalJSON rather
// than written by hand. Sample sets an operation (snapshot), so MarshalJSON
// takes the CDC branch and the row's columns land under "after" — they are not
// merged into the root the way a source with no operation would be.
const batchSqlSample = {
  after: { id: 1, name: 'first', region: 'eu' },
  id: 'sample-query-1789554373',
  metadata: { sample: 'true' },
  operation: 'snapshot',
}

const paths = (fields: { path: string }[]) => fields.map((f) => f.path)

describe('available fields downstream of a batch_sql source', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [], edges: [], nodeSamples: {} })
  })

  it('offers the sampled columns to a db_lookup node wired to the source', () => {
    const sourceNode = { id: 'src', type: 'source', data: { ref_id: 'batch-1' } } as unknown as Node
    const lookupNode = {
      id: 'lookup',
      type: 'transformation',
      data: { config: { transType: 'db_lookup' } },
    } as unknown as Node
    const edge = { id: 'e1', source: 'src', target: 'lookup' } as Edge
    useWorkflowStore.setState({ nodes: [sourceNode, lookupNode], edges: [edge], nodeSamples: {} })

    const sources = [
      { id: 'batch-1', type: 'batch_sql', config: { source_id: 'pg-1', queries: '["SELECT id, name, region FROM orders"]' }, sample: JSON.stringify(batchSqlSample) },
    ]

    const { result } = renderHook(() => useNodeContext(lookupNode, null, sources, []))

    // Hoisted to the root, which is where every lookup key/template field looks.
    expect(paths(result.current.availableFields)).toContain('name')
    expect(paths(result.current.availableFields)).toContain('region')
    expect(paths(result.current.availableFields)).toContain('id')
  })

  // A batch_sql source carries no use_cdc key, and use_cdc is opt-out, so the
  // schema-propagation step reads it as CDC and prefixes inferred fields with
  // "after.". That only affects fields inferred from upstream transformations;
  // the sampled columns themselves must still be reachable unprefixed.
  it('keeps the sampled columns unprefixed even though batch_sql carries no use_cdc key', () => {
    const sourceNode = { id: 'src', type: 'source', data: { ref_id: 'batch-1' } } as unknown as Node
    const lookupNode = {
      id: 'lookup',
      type: 'transformation',
      data: { config: { transType: 'db_lookup' } },
    } as unknown as Node
    const edge = { id: 'e1', source: 'src', target: 'lookup' } as Edge
    useWorkflowStore.setState({ nodes: [sourceNode, lookupNode], edges: [edge], nodeSamples: {} })

    const sources = [
      { id: 'batch-1', type: 'batch_sql', config: { source_id: 'pg-1' }, sample: JSON.stringify(batchSqlSample) },
    ]

    const { result } = renderHook(() => useNodeContext(lookupNode, null, sources, []))

    expect(paths(result.current.availableFields)).toContain('region')
    expect(paths(result.current.availableFields)).toContain('after.region')
  })
})
