import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, screen, fireEvent } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { REPLAY_HOP_MS } from '@/pages/workflows/WorkflowEditor/simulation/SimulationReplay'
import { applyDebuggerEvent } from '@/pages/workflows/WorkflowEditor/live/edgePauses'

const sent: unknown[] = []
vi.mock('@/pages/workflows/WorkflowEditor/live/breakpointSocket', () => ({
  sendDebuggerCommand: (cmd: unknown) => {
    sent.push(cmd)
    return true
  },
}))
vi.mock('@tanstack/react-router', () => ({ useParams: () => ({}), useNavigate: () => () => {} }))

installReactFlowLayoutShims()

/**
 * A message held on an edge breakpoint shows on that edge, with what it
 * carries, and is let go from there. A simulation replay stops at an edge
 * with a breakpoint, as a live run does.
 */
const n = (id: string, type: string, x: number) => ({ id, type, position: { x, y: 0 }, data: { label: id } }) as Node
const nodes = [n('src', 'source', 0), n('map', 'transformation', 300), n('out', 'transformation', 600)]
const edges = [
  { id: 'e-1', source: 'src', target: 'map', type: 'live' },
  { id: 'e-2', source: 'map', target: 'out', type: 'live', data: { breakpoint: true } },
] as Edge[]
const steps = [
  { node_id: 'src', payload: { a: 1 }, taken_edges: ['e-1'] },
  { node_id: 'map', payload: { a: 2 }, taken_edges: ['e-2'] },
  { node_id: 'out', payload: { a: 2 } },
]

const sim = (id: string) =>
  screen.getByTestId(`rf__edge-${id}`).querySelector('path.react-flow__edge-path')!.getAttribute('data-simulation')
const flow = (id: string) =>
  screen.getByTestId(`rf__edge-${id}`).querySelector('path.react-flow__edge-path')!.getAttribute('data-flow-state')

beforeEach(() => {
  sent.length = 0
  useWorkflowStore.setState({
    nodes, edges, testResults: null, testRunGraph: null, simReplay: null, replaySpeed: 1,
    workflowStatus: 'running', edgePauses: {}, edgeFlows: {}, nodeHeld: {}, statusFeedStale: false,
  })
})
afterEach(() => vi.useRealTimers())

describe('a live message held on an edge breakpoint', () => {
  it('shows on its edge with its payload, and is resumed from there', async () => {
    useWorkflowStore.setState({
      edgePauses: applyDebuggerEvent({}, {
        workflow_id: 'wf-1', node_id: 'out', msg_id: 'm-1', edge_id: 'e-2', pause_id: 'e-2/m-1', state: 'paused', data: { order: 42 },
      }),
    })
    renderCanvas()
    await screen.findByTestId('rf__edge-e-2')

    expect(flow('e-2')).toBe('paused')
    const chip = screen.getByTestId('edge-chip-e-2')
    expect(chip).toHaveTextContent('paused')

    fireEvent.mouseEnter(chip)
    expect(await screen.findByText(/"order": 42/)).toBeInTheDocument()
    fireEvent.click((await screen.findByText('Resume')).closest('button')!)

    expect(sent).toEqual([{ action: 'resume', msg_id: 'm-1', pause_id: 'e-2/m-1' }])
    expect(useWorkflowStore.getState().edgePauses).toEqual({})
  })
})

describe('a simulation replay', () => {
  it('stops at an edge with a breakpoint, and carries on when resumed', async () => {
    renderCanvas()
    await screen.findByTestId('rf__edge-e-1')
    vi.useFakeTimers()

    act(() => useWorkflowStore.getState().setTestResults(steps, { replay: true }))
    act(() => vi.advanceTimersByTime(0))
    expect([sim('e-1'), sim('e-2')]).toEqual(['crossing', 'pending'])

    act(() => vi.advanceTimersByTime(REPLAY_HOP_MS))
    expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'crossing'])
    act(() => vi.advanceTimersByTime(REPLAY_HOP_MS * 5))
    expect(useWorkflowStore.getState().simReplay).toEqual({ hop: 2, playing: false, breakpoint: 'e-2' })
    expect(screen.getByText('Paused at a breakpoint')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Resume' }))
    act(() => vi.advanceTimersByTime(REPLAY_HOP_MS))
    expect(useWorkflowStore.getState().simReplay).toBeNull()
    expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'taken'])
  })

  it('goes straight to the first breakpoint with reduced motion', async () => {
    const original = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as any
    try {
      renderCanvas()
      await screen.findByTestId('rf__edge-e-1')
      act(() => useWorkflowStore.getState().setTestResults(steps, { replay: true }))
      expect(useWorkflowStore.getState().simReplay).toEqual({ hop: 2, playing: false, breakpoint: 'e-2' })
    } finally {
      window.matchMedia = original
    }
  })
})
