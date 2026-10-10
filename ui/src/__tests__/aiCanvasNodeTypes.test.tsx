import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { describe, expect, it } from 'vitest'
import { TransformationNode } from '@/pages/workflows/WorkflowEditor/nodes/MiscNodes'
import { canvasNodeTypes } from '@/pages/workflows/WorkflowEditor/components/FlowCanvas'
import { detailNodeTypes } from '@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas'
import { AIClassifyNode } from '@/pages/workflows/WorkflowEditor/nodes/AIClassifyNode'
import { guideFor } from '@/lib/transformationGuide'

// React Flow draws a node whose type has no renderer as its built-in default
// node: no label outputs, so an ai_classify node could not be wired to its
// branches in the editor, and showed as a bare box on the detail page.
describe('ai_classify on the canvases', () => {
  it('is drawn by AIClassifyNode in the editor', () => {
    expect(canvasNodeTypes.ai_classify).toBe(AIClassifyNode)
  })

  it('has a renderer on the workflow detail page', () => {
    expect(detailNodeTypes.ai_classify).toBeTruthy()
  })
})

describe('AI node guides', () => {
  it.each(['ai_prompt', 'ai_extract', 'ai_embed', 'ai_classify'])('%s is described in plain words', (key) => {
    const g = guideFor(key)
    expect(g.what).not.toBe('')
    expect(g.title).not.toContain('_')
  })
})

describe('AI transformations on the canvas', () => {
  it.each([
    ['ai_prompt', 'AI Prompt'],
    ['ai_extract', 'AI Extract'],
    ['ai_embed', 'AI Embed'],
  ])('a %s node is labelled %s, not "Transformation"', (transType, label) => {
    render(
      <MantineProvider>
        <ReactFlowProvider>
          <TransformationNode id="t1" data={{ label: 'Step', transType }} selected={false} />
        </ReactFlowProvider>
      </MantineProvider>,
    )
    expect(screen.getAllByText(label).length).toBeGreaterThan(0)
    expect(screen.queryByText('Transformation')).toBeNull()
  })
})
