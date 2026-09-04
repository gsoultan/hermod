import { renderHook, act, waitFor } from '@testing-library/react'
import { vi, beforeEach, afterEach } from 'vitest'
import { useLiveStatuses, LIVE_STATUS_LIMIT } from '@/hooks/useLiveStatuses'

/** Minimal controllable WebSocket double. */
class FakeSocket {
  static instances: FakeSocket[] = []
  readyState = 0
  onmessage: ((e: any) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  close() {
    this.closed = true
    this.readyState = 3
    this.onclose?.()
  }
  open() {
    this.readyState = 1
    this.onopen?.()
  }
  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const latest = () => FakeSocket.instances[FakeSocket.instances.length - 1]

describe('useLiveStatuses', () => {
  it('collects status updates keyed by workflow id', async () => {
    const { result } = renderHook(() => useLiveStatuses())
    act(() => latest().open())
    act(() => latest().emit({ workflow_id: 'w1', source_status: 'running' }))

    await waitFor(() => expect(result.current.statuses.w1?.source_status).toBe('running'))
    expect(result.current.connected).toBe(true)
  })

  /**
   * The map is keyed by an id that arrives over the network. Left unbounded it
   * grows for the lifetime of the page — the rule this codebase already states
   * for any map keyed by input from outside.
   */
  it('bounds the map and evicts the oldest entries', async () => {
    const { result } = renderHook(() => useLiveStatuses())
    act(() => latest().open())

    act(() => {
      for (let i = 0; i < LIVE_STATUS_LIMIT + 25; i++) {
        latest().emit({ workflow_id: `w${i}`, source_status: 'running' })
      }
    })

    // Wait for the coalesced flush to land, then check the bound holds.
    await waitFor(() =>
      expect(result.current.statuses[`w${LIVE_STATUS_LIMIT + 24}`]).toBeDefined()
    )
    expect(Object.keys(result.current.statuses).length).toBeLessThanOrEqual(LIVE_STATUS_LIMIT)
    // Oldest gone, newest kept.
    expect(result.current.statuses.w0).toBeUndefined()
  })

  /**
   * A dropped socket used to leave the badges frozen on their last value with
   * nothing to say so — an operations tool silently reporting a stopped
   * pipeline as running.
   */
  it('reports disconnection and reconnects with backoff', async () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useLiveStatuses())
    // open() sets state inside act, so this is already settled — waitFor would
    // hang here because it polls on real timers.
    act(() => latest().open())
    expect(result.current.connected).toBe(true)

    const first = latest()
    act(() => first.close())
    expect(result.current.connected).toBe(false)

    // A new socket is opened once the backoff elapses, not immediately.
    expect(FakeSocket.instances.length).toBe(1)
    await act(async () => {
      vi.advanceTimersByTime(5000)
    })
    expect(FakeSocket.instances.length).toBeGreaterThan(1)
  })

  it('closes the socket and stops reconnecting on unmount', async () => {
    const { unmount } = renderHook(() => useLiveStatuses())
    act(() => latest().open())
    const socket = latest()
    unmount()
    expect(socket.closed).toBe(true)
  })

  it('ignores malformed frames without dropping the connection', async () => {
    const { result } = renderHook(() => useLiveStatuses())
    act(() => latest().open())
    act(() => latest().onmessage?.({ data: 'not json' }))
    act(() => latest().emit({ no_workflow_id: true }))
    act(() => latest().emit({ workflow_id: 'w1', source_status: 'running' }))

    await waitFor(() => expect(result.current.statuses.w1).toBeDefined())
    expect(result.current.connected).toBe(true)
  })
})
