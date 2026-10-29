import { applyDebuggerEvent, pausesOnEdge } from '@/pages/workflows/WorkflowEditor/live/edgePauses'

/**
 * The editor keeps the messages held on edge breakpoints from the debugger's
 * events: a pause adds one, and however a pause ends it goes.
 */
const paused = (pause_id: string, edge_id = 'e-1') => ({
  workflow_id: 'wf-1', node_id: 'n-2', msg_id: 'm-1', edge_id, pause_id, state: 'paused', data: { a: 1 },
})

describe('messages held on edge breakpoints', () => {
  it('are added when paused, once however often they are announced', () => {
    let pauses = applyDebuggerEvent({}, paused('e-1/m-1'))
    pauses = applyDebuggerEvent(pauses, paused('e-1/m-1'))
    expect(Object.keys(pauses)).toEqual(['e-1/m-1'])
    expect(pauses['e-1/m-1']).toMatchObject({ pauseId: 'e-1/m-1', edgeId: 'e-1', nodeId: 'n-2', msgId: 'm-1', data: { a: 1 } })
  })

  it('go when the pause ends, whichever way', () => {
    for (const state of ['resume', 'step', 'stopped', 'timeout']) {
      const pauses = applyDebuggerEvent({ 'e-1/m-1': applyDebuggerEvent({}, paused('e-1/m-1'))['e-1/m-1'] }, { ...paused('e-1/m-1'), state })
      expect(pauses).toEqual({})
    }
  })

  it('leave pauses before a node to the Debugger tab', () => {
    const before = {}
    expect(applyDebuggerEvent(before, { ...paused('m-1'), edge_id: undefined })).toBe(before)
  })

  it('are listed per edge, as the same list until that edge changes', () => {
    const pauses = applyDebuggerEvent(applyDebuggerEvent({}, paused('e-1/m-1')), paused('e-2/m-1', 'e-2'))
    const first = pausesOnEdge(pauses, 'e-1')
    expect(first.map((p) => p.pauseId)).toEqual(['e-1/m-1'])
    expect(pausesOnEdge(pauses, 'e-1')).toBe(first)
    expect(pausesOnEdge(pauses, 'e-3')).toEqual([])
  })
})
