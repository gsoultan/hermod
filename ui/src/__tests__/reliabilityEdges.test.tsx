import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({}),
  useNavigate: () => () => {},
}))

installReactFlowLayoutShims()

/**
 * A workflow with a dead-letter sink is meant to show where failed messages go:
 * a dashed orange edge from every other sink to the dead-letter sink, and with
 * Prioritize DLQ on, a dashed blue recovery edge from it back to the source.
 *
 * Neither was ever drawn. Both start at a sink node, a sink has no source
 * handle, and React Flow draws no edge whose source it cannot attach to. The
 * recovery edge also ends at a source node, which has no target handle. And had
 * they been drawn, they would not have looked like this: they fell to the live
 * edge component, which ignores an edge's own style.
 */

const nodes = [
  { id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders', ref_id: 'src-1' } },
  { id: 'main', type: 'sink', position: { x: 450, y: 0 }, data: { label: 'Warehouse', ref_id: 'snk-main' } },
  { id: 'dlq', type: 'sink', position: { x: 450, y: 300 }, data: { label: 'Parking lot', ref_id: 'snk-dlq' } },
] as Node[]

const edges = [{ id: 'e-main', source: 'src', target: 'main' }] as Edge[]

const edgePath = async (id: string) => {
  const edge = await screen.findByTestId(`rf__edge-${id}`)
  const path = edge.querySelector('path.react-flow__edge-path') as SVGPathElement | null
  if (!path) throw new Error(`edge ${id} has no path`)
  return path
}

describe('the dead-letter edges', () => {
  beforeEach(() => {
    useWorkflowStore.setState({
      nodes,
      edges,
      deadLetterSinkID: 'snk-dlq',
      prioritizeDLQ: false,
      testResults: null,
      active: false,
      pulseEnabled: true,
      nodeSamples: {},
      nodeMetrics: {},
      nodeErrorMetrics: {},
      edgeThroughput: {},
    })
  })

  it('draws a dashed orange edge from every other sink to the dead-letter sink', async () => {
    renderCanvas()

    const path = await edgePath('reliability_main_dlq')
    expect(path.style.stroke).toBe('var(--mantine-color-orange-6)')
    expect(path.style.strokeDasharray).toMatch(/^6(px)?,? 6(px)?$/)
    expect(screen.getByText('DLQ')).toBeInTheDocument()
  })

  it('draws the recovery edge back to the source when the dead-letter queue drains first', async () => {
    useWorkflowStore.setState({ prioritizeDLQ: true })
    renderCanvas()

    const path = await edgePath('recovery_dlq_src')
    expect(path.style.stroke).toBe('var(--mantine-color-blue-6)')
    expect(path.style.strokeDasharray).toMatch(/^6(px)?,? 6(px)?$/)
    expect(screen.getByText('RECOVERY')).toBeInTheDocument()
  })

  it('draws neither without a dead-letter sink', async () => {
    useWorkflowStore.setState({ deadLetterSinkID: '', prioritizeDLQ: true })
    renderCanvas()

    await edgePath('e-main')
    expect(screen.queryByTestId('rf__edge-reliability_main_dlq')).toBeNull()
    expect(screen.queryByTestId('rf__edge-recovery_dlq_src')).toBeNull()
  })

  it('offers no way to draw a connection of your own from those anchors', async () => {
    renderCanvas()

    await edgePath('reliability_main_dlq')
    const anchors = document.querySelectorAll(
      '[data-handleid="dlq"], [data-handleid="dlq-in"], [data-handleid="recovery-out"], [data-handleid="recovery"]'
    )
    expect(anchors.length).toBeGreaterThan(0)
    for (const handle of anchors) {
      expect(handle.classList.contains('connectable'), handle.getAttribute('data-handleid') ?? '').toBe(false)
    }
  })
})
