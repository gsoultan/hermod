import type { Edge, Node } from '@xyflow/react'
import {
  edgeSimulationView,
  simulationHopCount,
  simulationOutdated,
} from '@/pages/workflows/WorkflowEditor/simulation/simulationPath'

/**
 * A simulation lit its whole path at once, so the order the message travelled
 * in was lost, and the edge into a node that failed on the message was drawn
 * as the same green success as every other. The engine already answers with
 * its steps in the order it ran them; that order is the replay.
 */
const node = (id: string, type = 'transformation') => ({ id, type, position: { x: 0, y: 0 }, data: { label: id } }) as Node
const nodes = [node('src', 'source'), node('split', 'condition'), node('a'), node('b'), node('join'), node('fail'), node('drop')]
const edges = [
  { id: 'e1', source: 'src', target: 'split' },
  { id: 'e2', source: 'split', target: 'a' },
  { id: 'e3', source: 'split', target: 'b' },
  { id: 'e4', source: 'a', target: 'join' },
  { id: 'e5', source: 'b', target: 'join' },
  { id: 'e6', source: 'join', target: 'fail' },
  { id: 'e7', source: 'join', target: 'drop' },
] as Edge[]
const graph = { nodes, edges }

// src -> split fans out to a and b, which join; join feeds a node that fails
// and a node that drops the message.
const steps = [
  { node_id: 'src', payload: { n: 1 }, taken_edges: ['e1'] },
  { node_id: 'split', payload: { n: 1 }, taken_edges: ['e2', 'e3'] },
  { node_id: 'a', payload: { n: 1 }, taken_edges: ['e4'] },
  { node_id: 'b', payload: { n: 1 }, taken_edges: ['e5'] },
  { node_id: 'join', payload: { n: 1 }, taken_edges: ['e6', 'e7'] },
  { node_id: 'fail', error: 'boom' },
  { node_id: 'fail', filtered: true },
  { node_id: 'drop', filtered: true },
]

describe('the order a simulated message travelled in', () => {
  it('numbers each edge by hop, and branches that ran side by side share one', () => {
    const hop = (id: string) => edgeSimulationView(steps, graph, null, id)?.hop
    expect([hop('e1'), hop('e2'), hop('e3'), hop('e4'), hop('e5'), hop('e6'), hop('e7')]).toEqual([1, 2, 2, 3, 3, 4, 4])
    expect(simulationHopCount(steps, graph)).toBe(4)
  })

  it('ends an edge the way the node it reached ended', () => {
    expect(edgeSimulationView(steps, graph, null, 'e1')?.state).toBe('taken')
    expect(edgeSimulationView(steps, graph, null, 'e6')?.state).toBe('into-error')
    expect(edgeSimulationView(steps, graph, null, 'e7')?.state).toBe('into-filtered')
  })

  it('says nothing about an edge added after the run, rather than calling it not taken', () => {
    expect(edgeSimulationView(steps, graph, null, 'e-new')).toEqual({ state: 'new', hop: null })
  })

  it('draws a replay up to the hop being crossed', () => {
    const at = (hop: number, id: string) => edgeSimulationView(steps, graph, { hop }, id)?.state
    expect(at(2, 'e1')).toBe('taken')
    expect(at(2, 'e2')).toBe('crossing')
    expect(at(2, 'e3')).toBe('crossing')
    expect(at(2, 'e4')).toBe('pending')
    expect(at(2, 'e6')).toBe('pending')
  })

  it('is undefined with no simulation shown', () => {
    expect(edgeSimulationView(null, null, null, 'e1')).toBeUndefined()
  })
})

describe('a simulation of a graph that has changed since', () => {
  it('is current while only positions or selection change', () => {
    const moved = nodes.map((n) => ({ ...n, position: { x: 50, y: 50 }, selected: true }))
    expect(simulationOutdated(steps, graph, moved, edges)).toBe(false)
  })

  it('is outdated once a node is configured, added or removed, or an edge is rewired', () => {
    const reconfigured = nodes.map((n) => (n.id === 'a' ? { ...n, data: { ...n.data, transType: 'set' } } : n))
    expect(simulationOutdated(steps, graph, reconfigured, edges)).toBe(true)
    expect(simulationOutdated(steps, graph, [...nodes, node('extra')], edges)).toBe(true)
    expect(simulationOutdated(steps, graph, nodes.slice(1), edges)).toBe(true)
    const rewired = edges.map((e) => (e.id === 'e6' ? { ...e, target: 'drop' } : e))
    expect(simulationOutdated(steps, graph, nodes, rewired)).toBe(true)
    expect(simulationOutdated(steps, graph, nodes, [...edges, { id: 'e8', source: 'a', target: 'b' } as Edge])).toBe(true)
  })

  it('is not outdated with no simulation shown', () => {
    expect(simulationOutdated(null, null, nodes, edges)).toBe(false)
  })
})
