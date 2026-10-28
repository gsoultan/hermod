import { act, renderHook } from '@testing-library/react'
import { vi } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowWebSockets, STATUS_STALE_AFTER_MS } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowWebSockets'

/**
 * The status socket is the editor's only view of a running workflow. These
 * pin what it turns each frame into: rates from the engine's running totals,
 * the nodes with work stuck in them, and whether the feed itself can still be
 * believed.
 */
const sockets: FakeSocket[] = []
class FakeSocket {
  onmessage: ((e: MessageEvent) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {
    sockets.push(this)
  }
  close() {}
}
const status = () => sockets.filter((s) => s.url.includes('/ws/status')).at(-1)!
const send = (frame: Record<string, unknown>) =>
  act(() => status().onmessage!({ data: JSON.stringify({ workflow_id: 'wf-1', ...frame }) } as MessageEvent))

const T = 1_793_000_000_000

beforeEach(() => {
  sockets.length = 0
  vi.stubGlobal('WebSocket', FakeSocket)
  useWorkflowStore.setState({
    workflowStatus: 'running',
    edgeFlows: {},
    nodeErrorFlows: {},
    nodeBusy: {},
    nodeHeld: {},
    nodeLatencies: {},
    statusFeedStale: false,
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('status frames', () => {
  it('turn edge totals into rates by the time the engine read them', () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    act(() => status().onopen!())

    send({ edge_counts: { 'e-1': 52_000, 'e-2': 10 }, sampled_at: T })
    send({ edge_counts: { 'e-1': 52_150, 'e-2': 10 }, sampled_at: T + 1000 })

    const { edgeFlows } = useWorkflowStore.getState()
    expect(edgeFlows['e-1'].rate).toBeCloseTo(150)
    expect(edgeFlows['e-1'].band).toBe(3)
    // A large total that did not move is quiet, not fast.
    expect(edgeFlows['e-2'].band).toBe(0)
  })

  it('turn node error totals into recent errors', () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    send({ node_error_metrics: { lookup: 4 }, sampled_at: T })
    send({ node_error_metrics: { lookup: 7 }, sampled_at: T + 1000 })
    expect(useWorkflowStore.getState().nodeErrorFlows.lookup.band).toBeGreaterThan(0)
  })

  it('report the nodes with work in flight, and none once a frame lists none', () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    send({ sampled_at: T, node_busy: { lookup: { in_flight: 1, since_progress_ms: 45_000 } }, node_held: { map: 1 }, node_latencies: { lookup: 5e6 } })
    expect(useWorkflowStore.getState().nodeBusy.lookup.since_progress_ms).toBe(45_000)
    expect(useWorkflowStore.getState().nodeHeld.map).toBe(1)
    expect(useWorkflowStore.getState().nodeLatencies.lookup).toBe(5e6)

    // The engine leaves an empty map out of the frame.
    send({ sampled_at: T + 1000 })
    expect(useWorkflowStore.getState().nodeBusy).toEqual({})
    expect(useWorkflowStore.getState().nodeHeld).toEqual({})
  })
})

describe('the status feed', () => {
  it('is stale as soon as the socket closes, and live again with the next frame', () => {
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    act(() => status().onopen!())
    send({ sampled_at: T })
    expect(useWorkflowStore.getState().statusFeedStale).toBe(false)

    act(() => status().onclose!())
    expect(useWorkflowStore.getState().statusFeedStale).toBe(true)

    send({ sampled_at: T + 5000 })
    expect(useWorkflowStore.getState().statusFeedStale).toBe(false)
  })

  it('is stale when a running workflow has sent nothing for too long', () => {
    vi.useFakeTimers()
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    act(() => status().onopen!())
    send({ sampled_at: T })

    act(() => vi.advanceTimersByTime(STATUS_STALE_AFTER_MS - 1000))
    expect(useWorkflowStore.getState().statusFeedStale).toBe(false)
    act(() => vi.advanceTimersByTime(2000))
    expect(useWorkflowStore.getState().statusFeedStale).toBe(true)
  })

  it('is not stale for a stopped workflow, which has nothing to send', () => {
    vi.useFakeTimers()
    useWorkflowStore.setState({ workflowStatus: 'Stopped' })
    renderHook(() => useWorkflowWebSockets('wf-1', false, true))
    act(() => status().onopen!())
    act(() => vi.advanceTimersByTime(STATUS_STALE_AFTER_MS * 3))
    expect(useWorkflowStore.getState().statusFeedStale).toBe(false)
  })
})
