import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { afterEach, describe, expect, it } from 'vitest'
import { detailNodeTypes, detailEdgeTypes, LiveNote, toViewEdges } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { LiveEdge } from '@/pages/workflows/WorkflowEditor/components/LiveEdge'
import { SampleInspector } from '@/pages/workflows/WorkflowEditor/components/SampleInspector'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// The read-only view of a flow drew its own plain cards and lines and threw
// away every status frame but "running" or "stopped", so a running flow looked
// exactly like a stopped one: no counts, no moving lines, no latest record and
// no eye to open it. It now reads the same live state the editor does.

const wrap = (ui: React.ReactNode) =>
  render(
    <MantineProvider>
      <ReactFlowProvider>{ui}</ReactFlowProvider>
    </MantineProvider>,
  )

const SinkCard = detailNodeTypes.sink as any
const StepCard = detailNodeTypes.transformation as any

const live = (extra: Record<string, unknown> = {}) =>
  useWorkflowStore.setState({
    nodes: [
      { id: 'n-snk', type: 'sink', position: { x: 0, y: 0 }, data: { ref_id: 'k1' } },
      { id: 'n-map', type: 'transformation', position: { x: 0, y: 0 }, data: {} },
    ],
    workflowStatus: 'Running',
    statusFeedStale: false,
    nodeMetrics: {},
    nodeErrorMetrics: {},
    nodeSamples: {},
    sinkStatuses: {},
    sinkCBStatuses: {},
    sinkBufferFill: {},
    nodeBusy: {},
    nodeHeld: {},
    nodeErrorFlows: {},
    edgeFlows: {},
    sampleInspectorOpened: false,
    sampleNodeId: null,
    ...extra,
  } as any)

afterEach(() => live({ workflowStatus: 'Stopped', nodes: [] }))

describe('read-only step card on a live run', () => {
  it('says how many records the step has processed, and its errors', () => {
    live({ nodeMetrics: { 'n-map': 1204 }, nodeErrorMetrics: { 'n-map': 3 } })
    wrap(<StepCard id="n-map" type="transformation" data={{ label: 'Map order' }} />)
    const stats = screen.getByText('1,204 processed').closest('.hermod-step-stats')
    expect(stats).not.toBeNull()
    expect(stats!.textContent).toContain('3 errors')
  })

  it('shows the latest record with the eye, and opens it in full', () => {
    live({ nodeSamples: { 'n-snk': { after: { order_id: 4211 }, id: 'm1', operation: 'create' } } })
    wrap(<SinkCard id="n-snk" type="sink" data={{ label: 'Warehouse', ref_id: 'k1' }} />)
    const open = screen.getByRole('button', { name: /latest record/i })
    expect(open.textContent).toContain('{"order_id":4211}')
    fireEvent.click(open)
    expect(useWorkflowStore.getState().sampleNodeId).toBe('n-snk')
    expect(useWorkflowStore.getState().sampleInspectorOpened).toBe(true)
  })

  it('says what is wrong at a step', () => {
    live({ sinkStatuses: { k1: 'error: connection refused' } })
    wrap(<SinkCard id="n-snk" type="sink" data={{ label: 'Warehouse', ref_id: 'k1' }} />)
    expect(screen.getByRole('note', { name: /problem/i })).toBeInTheDocument()
  })

  it('offers no editing on the card', () => {
    live({ nodeMetrics: { 'n-map': 5 } })
    wrap(<StepCard id="n-map" type="transformation" data={{ label: 'Map order' }} />)
    expect(screen.queryByRole('button', { name: /remove/i })).toBeNull()
  })

  it('draws a stopped step without live lines', () => {
    live({ workflowStatus: 'Stopped' })
    const { container } = wrap(<StepCard id="n-map" type="transformation" data={{ label: 'Map order' }} />)
    expect(container.querySelector('.hermod-step-stats')).toBeNull()
    expect(screen.queryByRole('button', { name: /latest record/i })).toBeNull()
  })
})

describe('read-only canvas note', () => {
  it('says the flow is live while it runs', () => {
    live()
    wrap(<LiveNote />)
    expect(screen.getByTestId('flow-live-note')).toHaveAttribute('data-state', 'live')
    expect(screen.getByText(/^Live$/)).toBeInTheDocument()
  })

  it('says live data has paused when the feed goes quiet', () => {
    live({ statusFeedStale: true })
    wrap(<LiveNote />)
    expect(screen.getByTestId('flow-live-note')).toHaveAttribute('data-state', 'stale')
    expect(screen.getByText(/reconnecting/i)).toBeInTheDocument()
  })

  it('says the flow is stopped, and how to see live data', () => {
    live({ workflowStatus: 'Stopped' })
    wrap(<LiveNote />)
    expect(screen.getByTestId('flow-live-note')).toHaveAttribute('data-state', 'stopped')
    expect(screen.getByText(/turn it on to see live data/i)).toBeInTheDocument()
  })
})

describe('read-only canvas lines', () => {
  it('are live edges that cannot be edited', () => {
    expect(detailEdgeTypes.live).toBe(LiveEdge)
    expect(detailEdgeTypes.default).toBe(LiveEdge)
    const [edge] = toViewEdges({ edges: [{ id: 'e1', source_id: 'a', target_id: 'b', source_handle: 'true', config: {} }] })
    expect(edge).toMatchObject({ id: 'e1', source: 'a', target: 'b', type: 'live', deletable: false })
    // React Flow's own dash ran on every line of an active flow, moving or not.
    expect(edge.animated).toBeFalsy()
  })
})

describe('sample inspector', () => {
  it('opens a step\'s latest record from the store', () => {
    live({
      nodes: [{ id: 'n-snk', type: 'sink', position: { x: 0, y: 0 }, data: { label: 'Warehouse' } }],
      nodeSamples: { 'n-snk': { order_id: 7 } },
      sampleInspectorOpened: true,
      sampleNodeId: 'n-snk',
    })
    render(<MantineProvider><SampleInspector /></MantineProvider>)
    expect(screen.getByText('Warehouse')).toBeInTheDocument()
    expect((screen.getByLabelText('Record') as HTMLTextAreaElement).value).toContain('"order_id": 7')
  })
})
