import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, screen, within, fireEvent, waitFor } from '@testing-library/react'
import type { Node, Edge } from '@xyflow/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { installReactFlowLayoutShims, renderCanvas } from '../test/reactFlowCanvas'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowInitialization } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowInitialization'
import { TAKEN_EDGE_MARKER_ID } from '@/pages/workflows/WorkflowEditor/simulation/SimulationOverlay'
import {
  edgeSimulationState,
  nodeSimulationResult,
  simulationCounts,
} from '@/pages/workflows/WorkflowEditor/simulation/simulationPath'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({}),
  useNavigate: () => () => {},
}))

/**
 * Running a simulation left the canvas exactly as it was.
 *
 * The toast said "Active paths are highlighted", and nothing was: the code that
 * highlighted them was deleted in a refactor and nothing replaced it. The edges
 * could not have shown a path anyway -- Data Pulse is on by default, so every
 * edge was already drawn as the same animated blue dash -- and the step list
 * the engine returned did not say which edges the message took, nor tell a node
 * nothing reached from one that dropped the message.
 */

// What POST /api/workflows/test answered for the graph below with the sample
// {"tier":"gold","region":"US"}, copied from the running handler rather than
// written by hand (TestSimulationEndpointReportsThePathTheMessageTook sends the
// same shape of request). The condition sends gold customers to `gold`, so
// `other` is never reached. `keep-eu` is reached and drops the message, so the
// node after it is never reached either.
const wire = [
  { node_id: 'src', node_type: 'source', payload: { region: 'US', tier: 'gold' }, taken_edges: ['e-in'] },
  { node_id: 'is-gold', node_type: 'condition', payload: { region: 'US', tier: 'gold' }, branch: 'true', taken_edges: ['e-true'] },
  { node_id: 'gold', node_type: 'transformation', payload: { lane: 'gold', region: 'US', tier: 'gold' }, taken_edges: ['e-eu'] },
  { node_id: 'other', node_type: 'transformation', filtered: true, skipped: true },
  { node_id: 'keep-eu', node_type: 'transformation', filtered: true },
  { node_id: 'sink-ish', node_type: 'transformation', filtered: true, skipped: true },
]

// A node that fails is reported twice: once with its error, then once more as
// having emitted nothing. Also copied from the handler's answer.
const failure = 'unknown transformation type "set": no transformer is registered under that name'
const failedRun = [
  { node_id: 'src', node_type: 'source', payload: { tier: 'gold' }, taken_edges: ['e-in'] },
  { node_id: 'is-gold', node_type: 'condition', payload: { tier: 'gold' }, branch: 'true', taken_edges: ['e-true'] },
  { node_id: 'gold', node_type: 'transformation', error: failure },
  { node_id: 'gold', node_type: 'transformation', filtered: true },
  { node_id: 'other', node_type: 'transformation', filtered: true, skipped: true },
  { node_id: 'keep-eu', node_type: 'transformation', filtered: true, skipped: true },
  { node_id: 'sink-ish', node_type: 'transformation', filtered: true, skipped: true },
]

const node = (id: string, type: string, label: string, x: number, y: number, extra: Record<string, unknown> = {}) =>
  ({ id, type, position: { x, y }, data: { label, ...extra } }) as Node

const nodes = [
  node('src', 'source', 'Orders', 0, 0, { ref_id: 'src-1' }),
  node('is-gold', 'condition', 'Is gold?', 300, 0),
  node('gold', 'transformation', 'Gold lane', 600, -150, { transType: 'set' }),
  node('other', 'transformation', 'Other lane', 600, 150, { transType: 'set' }),
  node('keep-eu', 'transformation', 'Keep EU', 900, -150, { transType: 'filter_data' }),
  node('sink-ish', 'transformation', 'Mark done', 1200, -150, { transType: 'set' }),
  // Not a workflow step: the engine never reports on it.
  node('memo', 'note', 'Remember to add a sink', 0, 300),
]

const edges = [
  { id: 'e-in', source: 'src', target: 'is-gold' },
  { id: 'e-true', source: 'is-gold', sourceHandle: 'true', target: 'gold', data: { label: 'true' } },
  { id: 'e-false', source: 'is-gold', sourceHandle: 'false', target: 'other', data: { label: 'false' } },
  { id: 'e-eu', source: 'gold', target: 'keep-eu' },
  { id: 'e-done', source: 'keep-eu', target: 'sink-ish' },
] as Edge[]

describe('what a simulation did to each node', () => {
  it('reads each node the way the engine reported it', () => {
    expect(nodeSimulationResult(wire, 'src')?.status).toBe('passed')
    expect(nodeSimulationResult(wire, 'is-gold')).toMatchObject({ status: 'passed', branch: 'true' })
    expect(nodeSimulationResult(wire, 'gold')?.status).toBe('passed')
    // Reached, and emitted nothing.
    expect(nodeSimulationResult(wire, 'keep-eu')?.status).toBe('filtered')
    // Never reached: on the branch not taken, and after the node that dropped it.
    expect(nodeSimulationResult(wire, 'other')?.status).toBe('skipped')
    expect(nodeSimulationResult(wire, 'sink-ish')?.status).toBe('skipped')
  })

  it('reads a failed node as failed, though the engine also reports it filtered', () => {
    expect(nodeSimulationResult(failedRun, 'gold')).toMatchObject({ status: 'error', error: failure })
  })

  it('reads a node the run said nothing about as not reached', () => {
    // A node added after the run, or a source that had no sample to send.
    expect(nodeSimulationResult(wire, 'added-after-the-run')?.status).toBe('skipped')
  })

  it('reads nothing at all when no simulation has run', () => {
    expect(nodeSimulationResult(null, 'src')).toBeUndefined()
    expect(edgeSimulationState(null, 'e-in')).toBeUndefined()
    expect(simulationCounts(null, nodes)).toBeNull()
  })

  it('gives every caller the same answer until the next run', () => {
    // Every node and edge asks for its own entry from inside a store selector,
    // and the store is written on every telemetry frame. A fresh object per call
    // would re-render every node on the canvas on every frame.
    expect(nodeSimulationResult(wire, 'is-gold')).toBe(nodeSimulationResult(wire, 'is-gold'))
    expect(nodeSimulationResult(wire, 'added-after-the-run')).toBe(nodeSimulationResult(wire, 'another-one'))
  })
})

describe('the path the message took', () => {
  it('is the edges the engine forwarded the message along, and no others', () => {
    for (const id of ['e-in', 'e-true', 'e-eu']) {
      expect(edgeSimulationState(wire, id), id).toBe('taken')
    }
    for (const id of ['e-false', 'e-done']) {
      expect(edgeSimulationState(wire, id), id).toBe('untaken')
    }
  })
})

describe('the count of what the run did', () => {
  it('covers every workflow node on the canvas and leaves notes out', () => {
    expect(simulationCounts(wire, nodes)).toEqual({ passed: 3, filtered: 1, error: 0, skipped: 2 })
    expect(simulationCounts(failedRun, nodes)).toEqual({ passed: 2, filtered: 0, error: 1, skipped: 3 })
  })
})

// ---------------------------------------------------------------------------
// The canvas itself, drawn in jsdom with the layout stand-ins React Flow needs
// before it will draw an edge (see ../test/reactFlowCanvas).
// ---------------------------------------------------------------------------

installReactFlowLayoutShims()

const edgePath = async (id: string) => {
  const edge = await screen.findByTestId(`rf__edge-${id}`)
  const path = edge.querySelector('path.react-flow__edge-path')
  if (!path) throw new Error(`edge ${id} has no path`)
  return path
}

// The status a node's badge reads out; the badge's tooltip says the same.
const statusOf = async (nodeId: string) =>
  within(await screen.findByTestId(`rf__node-${nodeId}`)).getByText(/^Simulation:/)

describe('the canvas after a simulation', () => {
  beforeEach(() => {
    useWorkflowStore.setState({
      nodes,
      edges,
      testResults: null,
      active: false,
      pulseEnabled: true,
      nodeSamples: {},
      nodeMetrics: {},
      nodeErrorMetrics: {},
      edgeThroughput: {},
      deadLetterSinkID: '',
    })
  })

  it('draws the path the message took and sets the branches it did not take apart', async () => {
    useWorkflowStore.setState({ testResults: wire })
    renderCanvas()

    // What is drawn, not only what the edge is labelled: an edge the message
    // took points with the path's own arrowhead at full strength, one it did
    // not take fades back, and the two are not drawn in the same colour.
    const takenStrokes = new Set<string>()
    for (const id of ['e-in', 'e-true', 'e-eu']) {
      const path = (await edgePath(id)) as SVGPathElement
      expect(path, id).toHaveAttribute('data-simulation', 'taken')
      expect(path, id).toHaveAttribute('marker-end', `url(#${TAKEN_EDGE_MARKER_ID})`)
      expect(path.style.opacity === '' || Number(path.style.opacity) === 1, id).toBe(true)
      takenStrokes.add(path.style.stroke)
    }
    for (const id of ['e-false', 'e-done']) {
      const path = (await edgePath(id)) as SVGPathElement
      expect(path, id).toHaveAttribute('data-simulation', 'untaken')
      expect(path.getAttribute('marker-end'), id).not.toBe(`url(#${TAKEN_EDGE_MARKER_ID})`)
      expect(Number(path.style.opacity), id).toBeLessThan(1)
      expect(takenStrokes.has(path.style.stroke), id).toBe(false)
    }
    // The arrowhead the path points with is on the page.
    expect(document.getElementById(TAKEN_EDGE_MARKER_ID)?.tagName.toLowerCase()).toBe('marker')
  })

  it('says on each node what happened to the message there', async () => {
    useWorkflowStore.setState({ testResults: wire })
    renderCanvas()

    expect(await statusOf('src')).toHaveTextContent(/^Simulation: passed/)
    expect(await statusOf('is-gold')).toHaveTextContent(/^Simulation: passed.*branch "true"/)
    expect(await statusOf('keep-eu')).toHaveTextContent(/^Simulation: filtered/)
    expect(await statusOf('other')).toHaveTextContent(/^Simulation: not reached/)
    expect(await statusOf('sink-ish')).toHaveTextContent(/^Simulation: not reached/)
  })

  it('names the error on the node that failed', async () => {
    useWorkflowStore.setState({ testResults: failedRun })
    renderCanvas()

    const status = await statusOf('gold')
    expect(status).toHaveTextContent(/^Simulation: failed/)
    expect(status).toHaveTextContent(new RegExp(failure.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  })

  it('sums the run up in one place, and clearing it restores the canvas', async () => {
    useWorkflowStore.setState({ testResults: wire })
    renderCanvas()

    const summary = await screen.findByRole('region', { name: /simulation result/i })
    expect(summary).toHaveTextContent('3 passed')
    expect(summary).toHaveTextContent('1 filtered')
    expect(summary).toHaveTextContent('2 not reached')
    expect(summary).not.toHaveTextContent(/failed/)

    fireEvent.click(within(summary).getByRole('button', { name: /clear simulation/i }))

    expect(useWorkflowStore.getState().testResults).toBeNull()
    await waitFor(() => expect(screen.queryByRole('region', { name: /simulation result/i })).toBeNull())
    expect(screen.queryByText(/^Simulation:/)).toBeNull()
    expect(document.querySelector('[data-simulation]')).toBeNull()
  })

  it('leaves the canvas alone when no simulation has run', async () => {
    renderCanvas()

    await screen.findByTestId('rf__node-src')
    await edgePath('e-in')
    expect(screen.queryByText(/^Simulation:/)).toBeNull()
    expect(document.querySelector('[data-simulation]')).toBeNull()
    expect(screen.queryByRole('region', { name: /simulation result/i })).toBeNull()
  })
})

// The store outlives the editor page, so a run stayed in it when the operator
// opened another workflow. That used to show only as a Clear Simulation button
// where Test should be; with the run drawn on the canvas it painted the next
// workflow with the last one's result -- a summary bar, and every node greyed
// out, or marked passed where the two workflows happened to share a node ID.
describe('a simulation belongs to the workflow it ran on', () => {
  it('is dropped when another workflow is opened', async () => {
    signInAs()
    server.use(
      http.get('/api/workflows/wf-other', () =>
        HttpResponse.json({
          id: 'wf-other', name: 'Another workflow', vhost: 'default',
          // Shares a node ID with the workflow that ran.
          nodes: [{ id: 'src', type: 'source', ref_id: 'src-9', x: 0, y: 0, config: { label: 'Customers' } }],
          edges: [],
        })
      )
    )
    useWorkflowStore.setState({ nodes, edges, testResults: wire, name: 'The one that ran' })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    renderHook(() => useWorkflowInitialization('wf-other', 'default'), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    await waitFor(() => expect(useWorkflowStore.getState().name).toBe('Another workflow'))
    expect(useWorkflowStore.getState().testResults).toBeNull()
    expect(nodeSimulationResult(useWorkflowStore.getState().testResults, 'src')).toBeUndefined()
  })
})
