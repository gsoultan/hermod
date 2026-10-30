import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import type { Node } from '@xyflow/react'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({}),
  useNavigate: () => () => {},
}))

/**
 * A sink node that commits together with others says so on the canvas, by the
 * transaction's name, so the members of one transaction can be told apart
 * from the sinks that commit alone without opening each node.
 */
installReactFlowLayoutShims()

const node = (id: string, type: string, data: Record<string, unknown>, x: number) =>
  ({ id, type, position: { x, y: 0 }, data }) as Node

describe('sink nodes on the canvas', () => {
  beforeEach(() => {
    useWorkflowStore.setState({
      nodes: [
        node('src', 'source', { label: 'Orders', type: 'webhook', ref_id: 'src-1' }, 0),
        node('pg', 'sink', { label: 'Orders DB', type: 'postgres', ref_id: 'pg-1', transaction: 'orders' }, 400),
        node('my', 'sink', { label: 'Ledger DB', type: 'mysql', ref_id: 'my-1', transaction: 'orders' }, 400),
        node('log', 'sink', { label: 'Console', type: 'stdout', ref_id: 'out-1' }, 400),
      ],
      edges: [],
      testResults: null,
      active: false,
      nodeSamples: {},
      nodeMetrics: {},
      nodeErrorMetrics: {},
    })
  })

  it('names the transaction on each member and on nobody else', async () => {
    renderCanvas()
    for (const id of ['pg', 'my']) {
      const n = await screen.findByTestId(`rf__node-${id}`)
      expect(within(n).getByLabelText('Commits together in transaction orders')).toHaveTextContent('orders')
    }
    const alone = await screen.findByTestId('rf__node-log')
    expect(within(alone).queryByLabelText(/commits together/i)).not.toBeInTheDocument()
  })
})
