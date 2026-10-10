import type { Node } from '@xyflow/react'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * updateNodeConfig runs on every keystroke in a node's settings. Its no-op
 * guard serialised the whole graph twice to compare it -- every node's config,
 * including the sample rows saved on source nodes -- to find out whether the
 * one node being edited had changed. Only that node can have, so only that
 * node is compared.
 */
describe('updateNodeConfig', () => {
  let serialisedBystander = 0
  const bystander = (): Node =>
    ({
      id: 'big',
      type: 'source',
      position: { x: 0, y: 0 },
      data: {
        label: 'orders',
        lastSample: {
          toJSON() {
            serialisedBystander += 1
            return { after: { id: 1 } }
          },
        },
      },
    }) as unknown as Node
  const edited = (): Node =>
    ({ id: 'a', type: 'transformation', position: { x: 1, y: 0 }, data: { label: 'A', transType: 'set' } }) as Node

  beforeEach(() => {
    serialisedBystander = 0
    const a = edited()
    useWorkflowStore.setState({ nodes: [bystander(), a], selectedNode: a })
  })

  it('applies a change to the edited node and the selected copy', () => {
    useWorkflowStore.getState().updateNodeConfig('a', { label: 'Renamed' })

    const s = useWorkflowStore.getState()
    expect(s.nodes.find((n) => n.id === 'a')?.data.label).toBe('Renamed')
    expect(s.selectedNode?.data.label).toBe('Renamed')
  })

  it('keeps the same state when nothing changed', () => {
    const before = useWorkflowStore.getState().nodes

    useWorkflowStore.getState().updateNodeConfig('a', { label: 'A' })

    expect(useWorkflowStore.getState().nodes).toBe(before)
  })

  it('keeps the same state for a node that is not in the graph', () => {
    const before = useWorkflowStore.getState().nodes

    useWorkflowStore.getState().updateNodeConfig('missing', { label: 'x' })

    expect(useWorkflowStore.getState().nodes).toBe(before)
  })

  it('does not serialise the other nodes to find out', () => {
    useWorkflowStore.getState().updateNodeConfig('a', { label: 'Renamed' })
    useWorkflowStore.getState().updateNodeConfig('a', { label: 'Renamed' })

    expect(serialisedBystander).toBe(0)
  })
})
