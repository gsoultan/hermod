import { describe, expect, it } from 'vitest'
import { FLOW_FILTERS, fromTo, matchesFilter } from '@/pages/workflows/flowsModel'

const wf = (over: Record<string, unknown>) => ({ id: 'x', name: 'x', active: true, status: 'running', ...over })

describe('fromTo', () => {
  it('reads the first source and the sinks off the diagram', () => {
    expect(fromTo({ nodes: [
      { id: '1', type: 'source', data: { label: 'orders-db' } },
      { id: '2', type: 'transformation', data: { label: 'Mask' } },
      { id: '3', type: 'sink', data: { label: 'snowflake' } },
      { id: '4', type: 'sink', data: { name: 'kafka' } },
    ] })).toBe('orders-db → snowflake, kafka')
  })
  it('says what is missing instead of printing nothing', () => {
    expect(fromTo({ nodes: [] })).toBe('No steps yet')
    expect(fromTo({ nodes: [{ id: '1', type: 'source', data: { label: 'events' } }] })).toBe('events → no sink yet')
    expect(fromTo({})).toBe('No steps yet')
  })
})

describe('the one filter', () => {
  it('offers All, Running, Problems, Off', () => {
    expect(FLOW_FILTERS.map((f) => f.label)).toEqual(['All', 'Running', 'Problems', 'Off'])
  })
  it('sorts a flow into one of them by its switch and its state', () => {
    const running = wf({})
    const broken = wf({ status: 'error' })
    const off = wf({ active: false, status: 'stopped' })
    expect([running, broken, off].filter((w) => matchesFilter(w, 'all'))).toHaveLength(3)
    expect([running, broken, off].filter((w) => matchesFilter(w, 'running'))).toEqual([running])
    expect([running, broken, off].filter((w) => matchesFilter(w, 'problems'))).toEqual([broken])
    expect([running, broken, off].filter((w) => matchesFilter(w, 'off'))).toEqual([off])
  })
})
