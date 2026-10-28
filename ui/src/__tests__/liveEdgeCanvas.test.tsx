import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { advanceFlow } from '@/pages/workflows/WorkflowEditor/live/edgeFlow'

vi.mock('@tanstack/react-router', () => ({ useParams: () => ({}), useNavigate: () => () => {} }))

installReactFlowLayoutShims()

/**
 * What a live run looks like on the canvas. Every edge used to be the same
 * animated blue dash whether or not anything flowed, sped up by a lifetime
 * total; a stopped workflow animated as busily as a running one.
 */
const T = 1_793_000_000_000
const moving = (rate: number) => advanceFlow(advanceFlow(undefined, 0, T - 1000), rate, T)

const n = (id: string, type: string, label: string, x: number, extra: Record<string, unknown> = {}) =>
  ({ id, type, position: { x, y: 0 }, data: { label, ...extra } }) as Node
const nodes = [
  n('src', 'source', 'Orders CDC', 0, { ref_id: 'src-1' }),
  n('lookup', 'transformation', 'Customer lookup', 300, { transType: 'set' }),
  n('out', 'sink', 'Kafka EU', 600, { ref_id: 'snk-eu' }),
]
const edges = [
  { id: 'e-1', source: 'src', target: 'lookup', type: 'live' },
  { id: 'e-2', source: 'lookup', target: 'out', type: 'live' },
] as Edge[]

const edgeEl = async (id: string) => screen.findByTestId(`rf__edge-${id}`)
const pathOf = async (id: string) => (await edgeEl(id)).querySelector('path.react-flow__edge-path') as SVGPathElement
const dotsOf = async (id: string) => (await edgeEl(id)).querySelector('path.hermod-edge-dots')

beforeEach(() => {
  useWorkflowStore.setState({
    nodes,
    edges,
    testResults: null,
    testRunGraph: null,
    simReplay: null,
    pulseEnabled: true,
    workflowStatus: 'running',
    statusFeedStale: false,
    edgeFlows: {},
    nodeErrorFlows: {},
    nodeBusy: {},
    nodeHeld: {},
    nodeLatencies: {},
    nodeMetrics: { src: 52_311, lookup: 52_311, out: 52_311 },
    nodeErrorMetrics: {},
    nodeSamples: {},
    sourceStatus: 'running',
    sinkStatuses: { 'snk-eu': 'running' },
    sinkCBStatuses: { 'snk-eu': 'closed' },
    sinkBufferFill: { 'snk-eu': 0 },
  })
})

describe('edges on a live run', () => {
  it('do not move on a stopped workflow, however much it once carried', async () => {
    useWorkflowStore.setState({ workflowStatus: 'Stopped', edgeFlows: { 'e-1': moving(900) } })
    renderCanvas()
    expect(await pathOf('e-1')).toHaveAttribute('data-flow-state', 'idle')
    expect(await dotsOf('e-1')).toBeNull()
  })

  it('move only where messages cross, and show the rate', async () => {
    useWorkflowStore.setState({ edgeFlows: { 'e-1': moving(142), 'e-2': advanceFlow(advanceFlow(undefined, 9, T - 1000), 9, T) } })
    renderCanvas()

    const busy = await pathOf('e-1')
    expect(busy).toHaveAttribute('data-flow-state', 'flowing')
    expect(busy).toHaveAttribute('data-flow-band', '3')
    expect(await dotsOf('e-1')).toHaveClass('hermod-edge-dots', 'band-3')
    expect(within(await screen.findByTestId('edge-chip-e-1')).getByText('142/s')).toBeInTheDocument()

    expect(await pathOf('e-2')).toHaveAttribute('data-flow-state', 'quiet')
    expect(await dotsOf('e-2')).toBeNull()
  })

  it('stop moving, but keep the numbers, with Data Pulse off', async () => {
    useWorkflowStore.setState({ pulseEnabled: false, edgeFlows: { 'e-1': moving(142) } })
    renderCanvas()
    expect(await pathOf('e-1')).toHaveAttribute('data-flow-state', 'flowing')
    expect(await dotsOf('e-1')).toBeNull()
    expect(await screen.findByTestId('edge-chip-e-1')).toHaveTextContent('142/s')
  })

  it('say when the node they lead to is stuck', async () => {
    useWorkflowStore.setState({ nodeBusy: { lookup: { in_flight: 2, since_progress_ms: 61_000 } } })
    renderCanvas()
    expect(await pathOf('e-1')).toHaveAttribute('data-flow-state', 'hung')
    expect(await screen.findByTestId('edge-chip-e-1')).toHaveTextContent('stuck 1m')
  })

  it('say when the node they lead to is failing', async () => {
    useWorkflowStore.setState({ edgeFlows: { 'e-1': moving(40) }, nodeErrorFlows: { lookup: moving(3) } })
    renderCanvas()
    expect(await pathOf('e-1')).toHaveAttribute('data-flow-state', 'failing')
    expect(await screen.findByTestId('edge-chip-e-1')).toHaveTextContent('40/s · 3 err/s')
  })

  it('stop claiming anything when the status feed is lost', async () => {
    useWorkflowStore.setState({ statusFeedStale: true, edgeFlows: { 'e-1': moving(142) } })
    renderCanvas()
    expect(await pathOf('e-1')).toHaveAttribute('data-flow-state', 'stale')
    expect(await dotsOf('e-1')).toBeNull()
  })
})

describe('the live run banner', () => {
  it('names every problem on the canvas, worst first', async () => {
    useWorkflowStore.setState({
      nodeBusy: { lookup: { in_flight: 1, since_progress_ms: 45_000 } },
      sinkBufferFill: { 'snk-eu': 0.9 },
    })
    renderCanvas()
    const banner = await screen.findByRole('status', { name: /live run/i })
    expect(banner).toHaveTextContent('Customer lookup: no progress for 45s')
    expect(banner).toHaveTextContent('Kafka EU: falling behind, buffer 90%')
  })

  it('says the workflow has stalled, and that the feed was lost', async () => {
    useWorkflowStore.setState({ workflowStatus: 'stalled', statusFeedStale: true })
    renderCanvas()
    const banner = await screen.findByRole('status', { name: /live run/i })
    expect(banner).toHaveTextContent(/stalled/i)
    expect(banner).toHaveTextContent(/live status lost/i)
  })

  it('is not shown for a healthy run or a stopped workflow', async () => {
    renderCanvas()
    await pathOf('e-1')
    expect(screen.queryByRole('status', { name: /live run/i })).toBeNull()
  })
})

describe('a node on a live run', () => {
  it('says on the node itself what is wrong there', async () => {
    useWorkflowStore.setState({ nodeBusy: { lookup: { in_flight: 1, since_progress_ms: 45_000 } } })
    renderCanvas()
    const node = await screen.findByTestId('rf__node-lookup')
    expect(within(node).getByRole('note', { name: /problem: no progress for 45s/i })).toBeInTheDocument()
  })

  it('says nothing on a healthy node', async () => {
    renderCanvas()
    const node = await screen.findByTestId('rf__node-lookup')
    expect(within(node).queryByRole('note')).toBeNull()
  })
})
