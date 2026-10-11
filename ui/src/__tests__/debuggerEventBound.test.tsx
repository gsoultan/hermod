import { render, screen, waitFor, act } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi, afterEach, beforeEach } from 'vitest'
import { WorkflowDebugger } from '@/pages/workflows/WorkflowDebugger'

class FakeSocket {
  static instances: FakeSocket[] = []
  onmessage: ((e: any) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  send() {}
  close() {}
}

beforeEach(() => {
  if (typeof Element.prototype.scrollTo !== 'function') Element.prototype.scrollTo = function scrollTo() {}
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
})
afterEach(() => vi.unstubAllGlobals())

/**
 * The debugger appended every event it was sent and never let one go, so a
 * busy pipeline grew the list, and the page, for as long as it stayed open.
 */
describe('debugger event list', () => {
  it('keeps the most recent events and drops the oldest', async () => {
    render(
      <MantineProvider>
        <WorkflowDebugger workflowId="wf-1" />
      </MantineProvider>,
    )
    const socket = FakeSocket.instances[0]
    const total = 520

    act(() => {
      for (let i = 0; i < total; i++) {
        socket.onmessage?.({
          data: JSON.stringify({ type: 'step', msg_id: `m${i}`, node_id: 'n1', timestamp: '2026-10-09T10:00:00Z', data: null }),
        })
      }
    })

    await waitFor(() => expect(screen.getByText(`m${total - 1}`)).toBeInTheDocument())
    expect(screen.getAllByText(/^m\d+$/).length).toBeLessThanOrEqual(500)
    expect(screen.queryByText('m0')).toBeNull()
  }, 20000)
})
