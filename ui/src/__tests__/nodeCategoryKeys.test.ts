import { describe, it, expect } from 'vitest'
import { NODE_CATEGORIES, categoryKey } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

/**
 * React keys for the node palette's category cards.
 *
 * The palette renders categories three ways: the whole list on the combined
 * tab, and two group-filtered lists for Sources and Sinks. Category titles are
 * only unique *within* a group -- "Databases", "Messaging & Streams" and
 * "Social Media" each name both a source group and a sink group -- so keying
 * the combined list by title alone gave three pairs of colliding keys and a
 * React duplicate-key warning on every render of the panel.
 *
 * Colliding keys are not a cosmetic problem: React may reuse or drop the wrong
 * child, so a category's contents can render under another category's heading.
 */
describe('node palette category keys', () => {
  it('titles alone collide, which is why a composite key exists', () => {
    const titles = NODE_CATEGORIES.map((c) => c.title)
    const duplicated = titles.filter((t, i) => titles.indexOf(t) !== i)

    // If this ever becomes empty the collision is gone and categoryKey could be
    // simplified -- but until then, title is not a usable key.
    expect(new Set(duplicated)).toEqual(
      new Set(['Databases', 'Messaging & Streams', 'Social Media']),
    )
  })

  it('produces a unique key for every category', () => {
    const keys = NODE_CATEGORIES.map(categoryKey)
    const duplicated = keys.filter((k, i) => keys.indexOf(k) !== i)

    expect(duplicated, `duplicate palette keys: ${duplicated.join(', ')}`).toEqual([])
    expect(new Set(keys).size).toBe(NODE_CATEGORIES.length)
  })

  it('stays unique within each group, so the filtered tabs are safe too', () => {
    for (const group of ['sources', 'sinks', 'transformations']) {
      const keys = NODE_CATEGORIES.filter((c) => c.group === group).map(categoryKey)
      expect(new Set(keys).size, `duplicate keys within ${group}`).toBe(keys.length)
    }
  })

  it('does not depend on array position, so reordering cannot remount everything', () => {
    const cat = NODE_CATEGORIES[0]
    expect(categoryKey(cat)).toBe(categoryKey({ ...cat }))
    expect(categoryKey(cat)).toContain(cat.title)
  })
})
