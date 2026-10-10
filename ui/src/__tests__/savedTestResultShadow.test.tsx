import { renderHook } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { simulationInputs } from '@/pages/workflows/WorkflowEditor/sampleCapture'

/**
 * A `testResult` saved in a node's config is a copy, the same as `lastSample`.
 *
 * Nothing in the editor writes it any more, but saving keeps whatever a node
 * already carries (the server strips it only from exports), so a workflow saved
 * by an older editor still has one. ownPayloadOf read it before anything else.
 * The refresh icon got past it once -- its own run is fed the new sample
 * directly -- but the field list before that run, and every later press of
 * Test, went back to the saved copy and put the old columns back on every node.
 */

const stale = { operation: 'snapshot', after: { id: 1, retired_col: 'gone' } }
const fresh = { operation: 'snapshot', after: { id: 1, added_col: 'new' } }

const src = {
  id: 'n-src', type: 'source', position: { x: 0, y: 0 },
  data: { label: 'orders', ref_id: 'src-1', testResult: { payload: stale } },
} as Node
const nodeA = { id: 'n-a', type: 'transformation', position: { x: 250, y: 0 }, data: { label: 'A', transType: 'mapping' } } as Node
const edges = [{ id: 'e1', source: 'n-src', target: 'n-a' }] as Edge[]

const sourceRecord = (sample: string) => ({
  id: 'src-1', name: 'orders', type: 'sqlite', vhost: 'default',
  config: { path: '/tmp/orders.db', tables: 'orders', use_cdc: 'false' },
  sample,
})

const paths = (fields: { path: string }[]) => fields.map((f) => f.path)

describe('a testResult saved with the workflow', () => {
  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [src, nodeA], edges, nodeSamples: {}, testResults: null, selectedNode: null })
  })

  it('does not hide the refreshed stored sample from the next node', () => {
    const { result } = renderHook(() => useNodeContext(nodeA, null, [sourceRecord(JSON.stringify(fresh))], []))

    expect(paths(result.current.availableFields)).toContain('added_col')
    expect(paths(result.current.availableFields)).not.toContain('retired_col')
  })

  it('does not feed Test the old copy after a refresh stored a new sample', () => {
    const inputs = simulationInputs([src, nodeA], [sourceRecord(JSON.stringify(fresh))], {})

    expect(inputs['n-src']).toEqual(fresh)
  })

  it('is still used for a source with nothing stored', () => {
    const inputs = simulationInputs([src, nodeA], [sourceRecord('')], {})

    expect(inputs['n-src']).toEqual(stale)
  })
})
