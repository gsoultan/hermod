import { describe, expect, it } from 'vitest'
import { extractSubflow, guessInputs, type CanvasEdge, type CanvasNode } from '@/lib/subflowExtract'

const node = (id: string, type = 'transformation', data: Record<string, any> = {}, x = 0): CanvasNode =>
  ({ id, type, position: { x, y: 0 }, data: { label: id, ...data } })
const edge = (id: string, source: string, target: string, label?: string): CanvasEdge =>
  ({ id, source, target, sourceHandle: label ?? null, data: label ? { label } : {} })

// src → a → b → sink, with b's error edge going to dlq.
const nodes = [
  node('src', 'source', {}, 0), node('a', 'transformation', {}, 200), node('b', 'condition', {}, 400),
  node('sink', 'sink', {}, 600), node('dlq', 'sink', {}, 600),
]
const edges = [
  edge('e1', 'src', 'a'), edge('e2', 'a', 'b'), edge('e3', 'b', 'sink'), edge('e4', 'b', 'dlq', 'error'),
]
const opts = { callId: 'call', subflowId: 'sf1', name: 'Clean address', inputs: ['zip'], outputs: ['zip', 'city'] }

describe('turning a selection into a sub-flow', () => {
  it('moves the steps into a sub-flow between an input and a return', () => {
    const r = extractSubflow(nodes, edges, ['a', 'b'], opts)
    if ('error' in r) throw new Error(r.error)
    const types = r.subflow.nodes.map((n) => `${n.id}:${n.type}`)
    expect(types).toEqual(['input:subflow_input', 'a:transformation', 'b:condition', 'return:subflow_return'])
    expect(r.subflow.nodes[0].config.fields).toEqual([{ name: 'zip', required: true }])
    expect(r.subflow.edges.map((e) => `${e.source_id}>${e.target_id}`)).toEqual(['input>a', 'a>b', 'b>return'])
    // The error edge is left to the call: a failure the sub-flow does not
    // handle takes the call's error branch.
    expect(r.subflow.edges.some((e) => e.target_id === 'dlq')).toBe(false)
  })

  it('puts one call step in their place, wired as they were', () => {
    const r = extractSubflow(nodes, edges, ['a', 'b'], opts)
    if ('error' in r) throw new Error(r.error)
    expect(r.nodes.map((n) => n.id)).toEqual(['src', 'sink', 'dlq', 'call'])
    const call = r.nodes.find((n) => n.id === 'call')!
    expect(call.type).toBe('call_subflow')
    expect(call.data).toMatchObject({
      label: 'Clean address', subflow_id: 'sf1', subflow_name: 'Clean address', version: 1, returns: [''],
      inputs: [{ from: 'zip', to: 'zip' }],
      outputs: [{ from: 'zip', to: 'zip' }, { from: 'city', to: 'city' }],
    })
    expect(r.edges.map((e) => `${e.source}>${e.target}:${e.sourceHandle ?? ''}`)).toEqual([
      'src>call:', 'call>sink:', 'call>dlq:error',
    ])
  })

  it('gives each distinct way out its own named return', () => {
    // b is a condition: true goes to sink, false to dlq.
    const branched = [edge('e1', 'src', 'a'), edge('e2', 'a', 'b'), edge('t', 'b', 'sink', 'true'), edge('f', 'b', 'dlq', 'false')]
    const r = extractSubflow(nodes, branched, ['a', 'b'], opts)
    if ('error' in r) throw new Error(r.error)
    const returns = r.subflow.nodes.filter((n) => n.type === 'subflow_return')
    expect(returns.map((n) => n.config.name)).toEqual(['true', 'false'])
    const call = r.nodes.find((n) => n.id === 'call')!
    expect(call.data.returns).toEqual(['true', 'false'])
    expect(r.edges.filter((e) => e.source === 'call').map((e) => `${e.target}:${e.sourceHandle}:${e.data?.label}`))
      .toEqual(['sink:true:true', 'dlq:false:false'])
  })

  it('refuses a selection a sub-flow cannot hold', () => {
    expect(extractSubflow(nodes, edges, ['src', 'a'], opts)).toEqual({ error: expect.stringContaining('source') })
    expect(extractSubflow(nodes, edges, [], opts)).toEqual({ error: expect.stringContaining('Select') })
    expect(extractSubflow([...nodes, node('in', 'subflow_input')], edges, ['in', 'a'], opts))
      .toEqual({ error: expect.stringContaining('Sub-flow input') })
  })

  it('guesses the inputs from the fields the steps mention', () => {
    const steps = [node('a', 'transformation', { expression: 'upper(customer.zip)', target: 'out' })]
    expect(guessInputs(steps, ['customer.zip', 'customer.name', 'id'])).toEqual(['customer.zip'])
  })
})
