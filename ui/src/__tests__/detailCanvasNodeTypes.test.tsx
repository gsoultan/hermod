import { describe, expect, it } from 'vitest'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'

// React Flow draws a node whose type has no renderer as its built-in default
// node: a bare box with no handles. The read-only canvas on the workflow
// detail page had no renderer for validator, approval, log, collect,
// deduplicate or note, so those nodes showed up there unlabelled and
// unconnected. Every type the editor can draw must be drawable there too.
describe('workflow detail canvas', () => {
  it.each(Object.keys(canvasNodeTypes))('has a renderer for %s nodes', (type) => {
    expect(detailNodeTypes[type as keyof typeof detailNodeTypes]).toBeTruthy()
  })
})
