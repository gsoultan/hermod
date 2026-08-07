import { describe, it, expect } from 'vitest'
import { toGroupedSelectData } from '@/utils/selectData'

// Passing `{ value, label, group }` straight to a Mantine Select crashes its
// combobox parser with "Cannot read properties of undefined (reading 'map')".
// It needs `{ group, items: [...] }`, which is what this builds.
describe('toGroupedSelectData', () => {
  it('nests options under their group', () => {
    const out = toGroupedSelectData([
      { value: 'postgres', label: 'PostgreSQL', group: 'Databases' },
      { value: 'mysql', label: 'MySQL', group: 'Databases' },
      { value: 'kafka', label: 'Kafka', group: 'Messaging' },
    ])
    expect(out).toEqual([
      { group: 'Databases', items: [
        { value: 'postgres', label: 'PostgreSQL' },
        { value: 'mysql', label: 'MySQL' },
      ] },
      { group: 'Messaging', items: [{ value: 'kafka', label: 'Kafka' }] },
    ])
  })

  it('preserves first-appearance group order', () => {
    const out = toGroupedSelectData([
      { value: 'a', label: 'A', group: 'Second' },
      { value: 'b', label: 'B', group: 'First' },
      { value: 'c', label: 'C', group: 'Second' },
    ]) as { group: string }[]
    expect(out.map((g) => g.group)).toEqual(['Second', 'First'])
  })

  it('puts ungrouped options before any group header', () => {
    const out = toGroupedSelectData([
      { value: 'x', label: 'X', group: 'G' },
      { value: 'plain', label: 'Plain' },
    ])
    expect(out[0]).toEqual({ value: 'plain', label: 'Plain' })
  })

  it('treats a blank group as ungrouped', () => {
    const out = toGroupedSelectData([{ value: 'a', label: 'A', group: '   ' }])
    expect(out).toEqual([{ value: 'a', label: 'A' }])
  })

  it('falls back to the value when a label is missing', () => {
    const out = toGroupedSelectData([{ value: 'raw', label: undefined as any, group: 'G' }])
    expect(out).toEqual([{ group: 'G', items: [{ value: 'raw', label: 'raw' }] }])
  })

  // Selects render before their data loads; returning [] must never throw.
  it('handles empty, null and undefined input', () => {
    expect(toGroupedSelectData([])).toEqual([])
    expect(toGroupedSelectData(null)).toEqual([])
    expect(toGroupedSelectData(undefined)).toEqual([])
  })

  it('skips malformed entries rather than throwing', () => {
    const out = toGroupedSelectData([
      { value: 'ok', label: 'OK', group: 'G' },
      null as any,
      { label: 'no value' } as any,
    ])
    expect(out).toEqual([{ group: 'G', items: [{ value: 'ok', label: 'OK' }] }])
  })
})
