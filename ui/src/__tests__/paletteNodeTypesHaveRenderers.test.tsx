import { describe, expect, it } from 'vitest'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

// React Flow draws a node whose type has no renderer as its built-in default
// node: a bare box with no name, no icon and no "+" output, and warns in the
// console on every render. Stateful Join and Circuit Breaker are in the
// palette and run on the engine, but had no renderer on either canvas.
describe('palette node types', () => {
  const types = [...new Set(NODE_CATEGORIES.flatMap((c: any) => c.items.map((i: any) => i.type as string)))]

  it.each(types)('the editor canvas has a renderer for %s', (type) => {
    expect(canvasNodeTypes[type as keyof typeof canvasNodeTypes]).toBeTruthy()
  })

  it.each(types)('the detail canvas has a renderer for %s', (type) => {
    expect(detailNodeTypes[type as keyof typeof detailNodeTypes]).toBeTruthy()
  })
})
