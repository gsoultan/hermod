import { describe, expect, it } from 'vitest'
import { callDepth, isSubflow, subflowFields, subflowReturns, versionLabel } from '@/lib/subflow'

const nodes = [
  { id: 'in', type: 'subflow_input', config: { fields: [{ name: 'zip', required: true }, { name: 'street' }, { name: '' }] } },
  { id: 'geo', type: 'transformation', config: {} },
  { id: 'ok', type: 'subflow_return', config: { name: 'valid' } },
  { id: 'bad', type: 'subflow_return', config: { name: 'review' } },
  { id: 'dup', type: 'subflow_return', config: { name: 'valid' } },
]

describe('sub-flow helpers', () => {
  it('reads the declared inputs, skipping unnamed ones', () => {
    expect(subflowFields(nodes)).toEqual([
      { name: 'zip', required: true },
      { name: 'street', required: false },
    ])
  })

  it('lists each return name once, in order', () => {
    expect(subflowReturns(nodes)).toEqual(['valid', 'review'])
    expect(subflowReturns([{ id: 'r', type: 'subflow_return', config: {} }])).toEqual([''])
  })

  it('reads nothing from a version listed without its nodes', () => {
    // GET /versions lists versions with nodes: null; only GET /versions/{n} has them.
    expect(subflowFields(null)).toEqual([])
    expect(subflowReturns(null)).toEqual([])
  })

  it('knows a sub-flow by its input step', () => {
    expect(isSubflow(nodes)).toBe(true)
    expect(isSubflow([{ id: 's', type: 'source' }])).toBe(false)
    expect(isSubflow(undefined)).toBe(false)
  })

  it('counts how deep a spliced step sits', () => {
    expect(callDepth('geo')).toBe(0)
    expect(callDepth('call~geo')).toBe(1)
    expect(callDepth('call~inner~geo')).toBe(2)
  })

  it('names a pin', () => {
    expect(versionLabel('latest')).toBe('Latest saved (not pinned)')
    expect(versionLabel(4)).toBe('v4')
    expect(versionLabel(undefined)).toBe('Not pinned')
  })
})
