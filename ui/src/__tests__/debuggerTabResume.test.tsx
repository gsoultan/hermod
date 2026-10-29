import { render, screen, act, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi, afterEach, beforeEach } from 'vitest'
import { WorkflowDebugger } from '@/pages/workflows/WorkflowDebugger'

class FakeSocket {
  static instances: FakeSocket[] = []
  sent: string[] = []
  onmessage: ((e: any) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  constructor(public url: string) {
    FakeSocket.instances.push(this)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {}
}

beforeEach(() => {
  if (typeof Element.prototype.scrollTo !== 'function') Element.prototype.scrollTo = function scrollTo() {}
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket as any)
})
afterEach(() => vi.unstubAllGlobals())

/**
 * The engine says a message is held in `state`; the tab read `status`, so it
 * never showed a held message as paused and never offered to resume it.
 */
describe('the Debugger tab', () => {
  it('offers to resume a held message, and answers it by its pause ID', () => {
    render(
      <MantineProvider>
        <WorkflowDebugger workflowId="wf-1" />
      </MantineProvider>,
    )
    const socket = FakeSocket.instances[0]
    act(() => {
      socket.onmessage?.({
        data: JSON.stringify({ workflow_id: 'wf-1', node_id: 'out', edge_id: 'e-2', msg_id: 'm-1', pause_id: 'e-2/m-1', state: 'paused', data: { a: 1 } }),
      })
    })

    expect(screen.getByText('PAUSED')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }))
    expect(JSON.parse(socket.sent[0])).toEqual({ action: 'resume', msg_id: 'm-1', pause_id: 'e-2/m-1' })
  })
})
