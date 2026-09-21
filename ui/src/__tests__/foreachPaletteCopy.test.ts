import { describe, it, expect } from 'vitest'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { matchesQuery } from '@/pages/workflows/WorkflowEditor/utils/paletteSearch'

/**
 * The palette offers two items that both answer to "foreach", and they do
 * different things:
 *
 * - Logic & Flow -> `type: foreach` splits one message into N, one per array
 *   item, each carrying `_item`/`_index`. Downstream nodes run N times.
 * - Common Transformations -> `type: transformation, subType: foreach` writes
 *   the expanded array onto the *same* message under `resultField`. Downstream
 *   nodes run once.
 *
 * They were labelled "Foreach (Fan-out)" and "Foreach / Fanout", and the
 * transformation's description read "Iterate array items and fan out" -- a
 * fan-out it does not perform. Search normalises hyphens away, so a user typing
 * "fanout" got both, one under each name, with no way to tell which one fans
 * out.
 */

const findItem = (categoryTitle: string, predicate: (i: any) => boolean) => {
  const category = NODE_CATEGORIES.find((c) => c.title === categoryTitle)
  expect(category, `no palette category titled "${categoryTitle}"`).toBeTruthy()
  const item = category!.items.find(predicate)
  expect(item, `no matching item in "${categoryTitle}"`).toBeTruthy()
  return item as { label: string; description: string; type: string; subType: string }
}

const fanoutNode = () => findItem('Logic & Flow', (i) => i.type === 'foreach')
const expandTransformation = () =>
  findItem('Common Transformations', (i) => i.type === 'transformation' && i.subType === 'foreach')

describe('the two foreach palette entries', () => {
  it('do not share a label', () => {
    expect(fanoutNode().label).not.toBe(expandTransformation().label)
  })

  it('only lets the entry that actually fans out call itself a fan-out', () => {
    const transformation = expandTransformation()
    const claimsFanout = `${transformation.label} ${transformation.description}`

    expect(
      claimsFanout,
      `the transformation keeps one message; its copy must not promise a fan-out: "${claimsFanout}"`,
    ).not.toMatch(/fan\s*-?\s*out/i)

    const node = fanoutNode()
    expect(`${node.label} ${node.description}`).toMatch(/fan\s*-?\s*out/i)
  })

  it('says what each one does to the message count', () => {
    expect(expandTransformation().description).toMatch(/same record/i)
    expect(fanoutNode().description).toMatch(/one message per item/i)
  })

  it('sends a search for "fanout" to the node that fans out', () => {
    // Hyphens are normalised away on both sides, so "fanout" used to match the
    // transformation's label too.
    expect(matchesQuery(fanoutNode() as any, 'fanout')).toBe(true)
    expect(matchesQuery(expandTransformation() as any, 'fanout')).toBe(false)
  })

  it('still finds both when the user searches the name they share', () => {
    expect(matchesQuery(fanoutNode() as any, 'foreach')).toBe(true)
    expect(matchesQuery(expandTransformation() as any, 'foreach')).toBe(true)
  })
})
