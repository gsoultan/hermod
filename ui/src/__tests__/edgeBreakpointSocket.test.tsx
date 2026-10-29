import { act, renderHook } from '@testing-library/react'
import { vi } from 'vitest'
import type { Edge } from '@xyflow/react'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useEdgeBreakpoints } from '@/pages/workflows/WorkflowEditor/hooks/useEdgeBreakpoints'
import { sendDebuggerCommand } from '@/pages/workflows/WorkflowEditor/live/breakpointSocket'

/**
 * The editor attaches to the debugger, in breakpoints mode, only while a
 * running workflow has a breakpoint to answer; attaching is what makes the
 * engine stop there.
 */
const sockets: FakeSocket[] = []
class FakeSocket {
  static OPEN = 1
  readyState = 1
  sent: string[] = []
  onmessage: ((e: MessageEvent) => void) | null = null
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  constructor(public url: string) {
    sockets.push(this)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {
    this.closed = true
  }
}

const withBreakpoint = [{ id: 'e-1', source: 'a', target: 'b', data: { breakpoint: true } }] as Edge[]
const without = [{ id: 'e-1', source: 'a', target: 'b' }] as Edge[]

beforeEach(() => {
  sockets.length = 0
  vi.stubGlobal('WebSocket', FakeSocket)
  useWorkflowStore.setState({ workflowStatus: 'running', edges: withBreakpoint, edgePauses: {} })
})
afterEach(() => vi.unstubAllGlobals())

describe('the editor debugger connection', () => {
  it('opens in breakpoints mode while a running workflow has a breakpoint', () => {
    renderHook(() => useEdgeBreakpoints('wf-1'))
    expect(sockets).toHaveLength(1)
    expect(sockets[0].url).toContain('/api/ws/debugger?workflow_id=wf-1&mode=breakpoints')
  })

  it('stays closed with no breakpoint, or with the workflow stopped', () => {
    useWorkflowStore.setState({ edges: without })
    renderHook(() => useEdgeBreakpoints('wf-1'))
    act(() => useWorkflowStore.setState({ edges: withBreakpoint, workflowStatus: 'Stopped' }))
    expect(sockets).toHaveLength(0)
  })

  it('keeps the pauses it hears about, sends commands, and forgets them when it closes', () => {
    const { unmount } = renderHook(() => useEdgeBreakpoints('wf-1'))
    act(() => sockets[0].onopen!())
    act(() => sockets[0].onmessage!({
      data: JSON.stringify({ workflow_id: 'wf-1', node_id: 'b', msg_id: 'm-1', edge_id: 'e-1', pause_id: 'e-1/m-1', state: 'paused', data: {} }),
    } as MessageEvent))
    expect(Object.keys(useWorkflowStore.getState().edgePauses)).toEqual(['e-1/m-1'])

    expect(sendDebuggerCommand({ action: 'resume', msg_id: 'm-1', pause_id: 'e-1/m-1' })).toBe(true)
    expect(JSON.parse(sockets[0].sent[0])).toEqual({ action: 'resume', msg_id: 'm-1', pause_id: 'e-1/m-1' })

    unmount()
    expect(sockets[0].closed).toBe(true)
    expect(useWorkflowStore.getState().edgePauses).toEqual({})
    expect(sendDebuggerCommand({ action: 'resume', msg_id: 'm-1', pause_id: 'e-1/m-1' })).toBe(false)
  })
})
