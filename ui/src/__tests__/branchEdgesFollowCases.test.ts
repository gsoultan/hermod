import { describe, it, expect } from 'vitest'
import type { Edge } from '@xyflow/react'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// An edge out of a switch case or router rule is drawn from the case's handle
// and carries the handle's id as its label, which is the branch the engine
// sends a message down. Renaming the case renamed the handle and left the edge
// on the old id: the canvas stopped drawing it, the save kept it, and every
// message matching the renamed case stopped at the switch without an error.

const edge = (id: string, handle: string, target: string): Edge => ({
  id,
  source: 'sw',
  target,
  sourceHandle: handle,
  data: { label: handle },
})

function setup(type: 'switch' | 'router', items: any[], edges: Edge[]) {
  const key = type === 'switch' ? 'cases' : 'rules'
  useWorkflowStore.setState({
    nodes: [{ id: 'sw', type, position: { x: 0, y: 0 }, data: { [key]: items } }],
    edges,
  })
}

const handles = () =>
  useWorkflowStore.getState().edges.map((e) => [e.id, e.sourceHandle, (e.data as any)?.label])

describe('edges follow the switch case or router rule they were drawn from', () => {
  it('renaming a case moves its edge to the new name', () => {
    setup('switch', [{ label: 'SUBMIT' }, { label: 'REVISON' }], [
      edge('e1', 'SUBMIT', 'a'),
      edge('e2', 'REVISON', 'b'),
      edge('e3', 'default', 'c'),
    ])
    useWorkflowStore.getState().updateNodeConfig('sw', { cases: [{ label: 'SUBMIT' }, { label: 'REVISION' }] })
    expect(handles()).toEqual([
      ['e1', 'SUBMIT', 'SUBMIT'],
      ['e2', 'REVISION', 'REVISION'],
      ['e3', 'default', 'default'],
    ])
  })

  it('removing a case removes its edge and renumbers the unnamed cases after it', () => {
    setup('switch', [{ label: 'A' }, { label: '' }, { label: '' }], [
      edge('e1', 'A', 'a'),
      edge('e2', 'case_1', 'b'),
      edge('e3', 'case_2', 'c'),
    ])
    useWorkflowStore.getState().updateNodeConfig('sw', { cases: [{ label: '' }, { label: '' }] })
    expect(handles()).toEqual([
      ['e2', 'case_0', 'case_0'],
      ['e3', 'case_1', 'case_1'],
    ])
  })

  it('renaming a router rule kept as a JSON string moves its edge too', () => {
    setup('router', [{ label: 'eu' }] as any, [edge('e1', 'eu', 'a')])
    useWorkflowStore.setState({
      nodes: [{ id: 'sw', type: 'router', position: { x: 0, y: 0 }, data: { rules: '[{"label":"eu"}]' } }],
    })
    useWorkflowStore.getState().updateNodeConfig('sw', { rules: '[{"label":"europe"}]' })
    expect(handles()).toEqual([['e1', 'europe', 'europe']])
  })

  it('adding a case leaves existing edges alone', () => {
    setup('switch', [{ label: 'A' }], [edge('e1', 'A', 'a')])
    useWorkflowStore.getState().updateNodeConfig('sw', { cases: [{ label: 'A' }, { label: 'case_2' }] })
    expect(handles()).toEqual([['e1', 'A', 'A']])
  })

  it('a name two cases share moves neither edge', () => {
    setup('switch', [{ label: 'SUBMIT' }, { label: 'SUBMIT' }], [
      edge('e1', 'SUBMIT', 'a'),
      edge('e2', 'SUBMIT', 'b'),
    ])
    useWorkflowStore.getState().updateNodeConfig('sw', { cases: [{ label: 'SUBMIT' }, { label: 'SUBMITX' }] })
    expect(handles()).toEqual([
      ['e1', 'SUBMIT', 'SUBMIT'],
      ['e2', 'SUBMIT', 'SUBMIT'],
    ])
  })
})
