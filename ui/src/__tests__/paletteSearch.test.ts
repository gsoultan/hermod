import { describe, it, expect } from 'vitest'
import { matchesQuery, filterCategories, countMatches } from '@/pages/workflows/WorkflowEditor/utils/paletteSearch'

const cats = [
  {
    title: 'Common Transformations',
    group: 'transformations',
    items: [
      { type: 'transformation', label: 'Mapping', subType: 'mapping', description: 'Map fields and reshape payloads' },
      { type: 'transformation', label: 'Filter', subType: 'filter_data', description: 'Keep or drop records by condition' },
    ],
  },
  {
    title: 'Databases',
    group: 'sources',
    items: [
      { type: 'source', label: 'PostgreSQL', subType: 'postgresql', description: 'Read tables or CDC' },
    ],
  },
  {
    title: 'Warehouses',
    group: 'sinks',
    items: [
      { type: 'sink', label: 'Postgres Sink', subType: 'postgresql', description: 'Write rows' },
    ],
  },
]

describe('palette search', () => {
  it('matches on label, sub-type and description', () => {
    const item = cats[0].items[0]
    expect(matchesQuery(item, 'mapping')).toBe(true)
    expect(matchesQuery(item, 'reshape')).toBe(true) // description
    expect(matchesQuery(item, 'MAPPING')).toBe(true) // case-insensitive
    expect(matchesQuery(item, 'kafka')).toBe(false)
  })

  // A user typing "filter data" should find "Filter", whose sub-type is
  // filter_data. Matching the raw string would miss it, because the label has
  // no underscore and the sub-type has no space.
  it('ignores spaces and underscores so filter_data is found by "filter data"', () => {
    const filter = cats[0].items[1]
    expect(matchesQuery(filter, 'filter data')).toBe(true)
    expect(matchesQuery(filter, 'filter_data')).toBe(true)
  })

  it('drops categories left with no matching items', () => {
    const out = filterCategories(cats, 'postgres')
    expect(out.map((c) => c.title)).toEqual(['Databases', 'Warehouses'])
    expect(out[0].items).toHaveLength(1)
  })

  it('returns every category unchanged for an empty query', () => {
    expect(filterCategories(cats, '   ')).toEqual(cats)
  })

  // The panel is tabbed, so a match can easily be one tab away. Searching
  // "postgres" from the Transformations tab should be able to say where the
  // results actually are rather than just showing nothing.
  it('counts matches per tab so an empty tab can point at a full one', () => {
    expect(countMatches(cats, 'postgres')).toEqual({
      sources: 1,
      sinks: 1,
      transformations: 0,
    })
  })

  it('counts nothing for a query that matches nowhere', () => {
    expect(countMatches(cats, 'nonesuch')).toEqual({
      sources: 0,
      sinks: 0,
      transformations: 0,
    })
  })
})
