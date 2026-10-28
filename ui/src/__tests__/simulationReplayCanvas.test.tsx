import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, screen, fireEvent } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { REPLAY_HOP_MS } from '@/pages/workflows/WorkflowEditor/simulation/SimulationReplay'

vi.mock('@tanstack/react-router', () => ({ useParams: () => ({}), useNavigate: () => () => {} }))

installReactFlowLayoutShims()

/**
 * A simulation asked for from the editor replays the message hop by hop, then
 * settles on the whole path; the replay can be stepped through by hand.
 */
const n = (id: string, type: string, x: number) => ({ id, type, position: { x, y: 0 }, data: { label: id } }) as Node
const nodes = [n('src', 'source', 0), n('map', 'transformation', 300), n('out', 'transformation', 600)]
const edges = [
  { id: 'e-1', source: 'src', target: 'map', type: 'live' },
  { id: 'e-2', source: 'map', target: 'out', type: 'live' },
] as Edge[]
const steps = [
  { node_id: 'src', payload: { a: 1 }, taken_edges: ['e-1'] },
  { node_id: 'map', payload: { a: 2 }, taken_edges: ['e-2'] },
  { node_id: 'out', payload: { a: 2 } },
]

const sim = (id: string) =>
  screen.getByTestId(`rf__edge-${id}`).querySelector('path.react-flow__edge-path')!.getAttribute('data-simulation')

beforeEach(() => {
  useWorkflowStore.setState({ nodes, edges, testResults: null, testRunGraph: null, simReplay: null, replaySpeed: 1, workflowStatus: 'Stopped' })
})
afterEach(() => vi.useRealTimers())

describe('replaying a simulation', () => {
  it('crosses one hop at a time and then shows the whole path', async () => {
    renderCanvas()
    await screen.findByTestId('rf__edge-e-1')
    vi.useFakeTimers()

    act(() => useWorkflowStore.getState().setTestResults(steps, { replay: true }))
    act(() => vi.advanceTimersByTime(0))
    expect([sim('e-1'), sim('e-2')]).toEqual(['crossing', 'pending'])

    act(() => vi.advanceTimersByTime(REPLAY_HOP_MS))
    expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'crossing'])

    act(() => vi.advanceTimersByTime(REPLAY_HOP_MS))
    expect(useWorkflowStore.getState().simReplay).toBeNull()
    expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'taken'])
  })

  it('goes straight to the whole path with reduced motion', async () => {
    const original = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as any
    try {
      renderCanvas()
      await screen.findByTestId('rf__edge-e-1')
      act(() => useWorkflowStore.getState().setTestResults(steps, { replay: true }))
      expect(useWorkflowStore.getState().simReplay).toBeNull()
      expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'taken'])
    } finally {
      window.matchMedia = original
    }
  })

  it('can be stepped back through by hand', async () => {
    useWorkflowStore.setState({ testResults: steps, testRunGraph: { nodes, edges } })
    renderCanvas()
    await screen.findByTestId('rf__edge-e-1')

    fireEvent.click(screen.getByRole('button', { name: 'Step back' }))
    expect(screen.getByText('hop 1 / 2')).toBeInTheDocument()
    expect([sim('e-1'), sim('e-2')]).toEqual(['crossing', 'pending'])

    fireEvent.click(screen.getByRole('button', { name: 'Step forward' }))
    fireEvent.click(screen.getByRole('button', { name: 'Step forward' }))
    expect(useWorkflowStore.getState().simReplay).toBeNull()
    expect([sim('e-1'), sim('e-2')]).toEqual(['taken', 'taken'])
  })

  it('marks the path as out of date once the graph is rewired', async () => {
    useWorkflowStore.setState({ testResults: steps, testRunGraph: { nodes, edges } })
    renderCanvas()
    await screen.findByTestId('rf__edge-e-1')
    act(() => useWorkflowStore.getState().setEdges((eds) => [...eds, { id: 'e-3', source: 'src', target: 'out', type: 'live' }]))
    expect(await screen.findByText('Graph changed since this run')).toBeInTheDocument()
    expect(sim('e-3')).toBe('new')
  })
})
