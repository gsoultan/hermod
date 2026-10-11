import { renderHook, act, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { vi, afterEach, beforeEach } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { useDashboardStream } from '@/hooks/useDashboardStream'

class FakeSocket {
  static instances: FakeSocket[] = []
  onmessage: ((e: any) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  close() {}
  emit(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }
}

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
})
afterEach(() => vi.unstubAllGlobals())

/**
 * The throughput chart is per vhost, but switching vhost kept the points
 * already drawn and only replaced them if the new vhost's history loaded. When
 * it did not — no history yet, or the request failed — the new vhost's chart
 * went on showing the old one's traffic.
 */
describe('dashboard throughput series', () => {
  it('starts empty for a newly selected vhost whose history is unavailable', async () => {
    signInAs()
    server.use(
      http.get('/api/dashboard/history', ({ request }) =>
        new URL(request.url).searchParams.get('vhost') === 'east'
          ? HttpResponse.json([])
          : HttpResponse.json({ error: 'unavailable' }, { status: 500 }),
      ),
      http.get('/api/dashboard/stats', () => HttpResponse.json({ total_processed: 0, throughput: 0 })),
    )

    const { result, rerender } = renderHook(({ vhost }) => useDashboardStream(vhost), { initialProps: { vhost: 'east' } })
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
    // Let east's (empty) history land before drawing live points over it.
    await new Promise((r) => setTimeout(r, 50))
    act(() => {
      FakeSocket.instances[0].emit({ total_processed: 10, throughput: 42 })
      FakeSocket.instances[0].emit({ total_processed: 20, throughput: 43 })
    })
    await waitFor(() => expect(result.current.series).toEqual([42, 43]))

    rerender({ vhost: 'west' })
    // West's history request fails; give it time to.
    await new Promise((r) => setTimeout(r, 100))

    expect(result.current.series).toEqual([])
  })
})
