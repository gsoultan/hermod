import { renderHook, fireEvent } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useWorkflowHotkeys } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowHotkeys'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * The Delete key did nothing: the binding was 'delete, backspace', which
 * useHotkeys reads as one key, not two. Its handler also removed only the
 * selected node and edges, not the edges wired to that node, which would have
 * stayed in the store pointing at nothing and been saved that way.
 */
describe('Delete key on the canvas', () => {
  const node = (id: string, selected = false): Node =>
    ({ id, type: 'transformation', position: { x: 0, y: 0 }, data: { label: id }, selected }) as Node

  beforeEach(() => {
    useWorkflowStore.setState({
      nodes: [node('a'), node('b', true), node('c')],
      edges: [
        { id: 'a-b', source: 'a', target: 'b' },
        { id: 'b-c', source: 'b', target: 'c' },
        { id: 'a-c', source: 'a', target: 'c' },
      ] as Edge[],
      selectedNode: null,
    })
  })

  it('removes the edges of the node it deletes', () => {
    renderHook(() => useWorkflowHotkeys(() => {}, () => {}))

    fireEvent.keyDown(document.documentElement, { key: 'Delete' })

    const { nodes, edges } = useWorkflowStore.getState()
    expect(nodes.map((n) => n.id)).toEqual(['a', 'c'])
    expect(edges.map((e) => e.id)).toEqual(['a-c'])
  })
})
