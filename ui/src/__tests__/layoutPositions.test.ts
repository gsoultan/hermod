import { describe, it, expect } from 'vitest'
import { layoutPositions } from '@/workers/layoutPositions'

// Tidy up laid every step out as a 250x100 box. A step with several ways out
// is taller than that (a Switch with four cases is about 230px), so the step
// placed under it landed on top of its branch rows. Steps are now laid out at
// the size they were drawn.
describe('layoutPositions', () => {
  it('keeps steps in the same column apart by their measured heights', () => {
    const out = layoutPositions({
      id: 1,
      direction: 'LR',
      nodes: [
        { id: 'src' },
        { id: 'tall', width: 260, height: 240 },
        { id: 'below', width: 260, height: 84 },
      ],
      edges: [{ source: 'src', target: 'tall' }, { source: 'src', target: 'below' }],
    })
    const at = Object.fromEntries(out.nodes.map((n) => [n.id, n]))
    const [top, bottom] = at.tall.y < at.below.y ? [at.tall, { ...at.below }] : [at.below, at.tall]
    const topHeight = top === at.tall ? 240 : 84
    expect(bottom.y).toBeGreaterThanOrEqual(top.y + topHeight)
  })

  it('places a step by its top-left corner', () => {
    const out = layoutPositions({ id: 2, direction: 'LR', nodes: [{ id: 'a', width: 200, height: 80 }], edges: [] })
    expect(out.nodes[0]).toEqual({ id: 'a', x: 0, y: 0 })
  })
})
