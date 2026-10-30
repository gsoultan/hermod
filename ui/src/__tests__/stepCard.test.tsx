import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import type { ComponentType } from 'react'
import { describe, it, expect } from 'vitest'
import { SinkNode } from '@/pages/workflows/WorkflowEditor/nodes/SourceSinkNodes'
import { AIAgentNode } from '@/pages/workflows/WorkflowEditor/nodes/AIAgentNode'
import { NoteNode } from '@/pages/workflows/WorkflowEditor/nodes/MiscNodes'

const renderNode = (Node: ComponentType<any>, data: Record<string, unknown>) =>
  render(
    <MantineProvider>
      <ReactFlowProvider>
        <Node id="n1" data={data} selected={false} />
      </ReactFlowProvider>
    </MantineProvider>,
  )

// A step's numbers and warnings used to hang off its edges as coloured
// bubbles ("1,204", "3 ERR", "⚠️ 2 FAILED", a "CIRCUIT BREAKER: OPEN" strip
// in 8px type over the title). They now read as one quiet line in the card.
describe('step card', () => {
  it('says how a step is doing in a line inside the card', () => {
    renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1', metric: 1204, errorCount: 3, dlqCount: 2, cbStatus: 'open' })
    const stats = screen.getByText('1,204 processed').closest('.hermod-step-stats')
    expect(stats).not.toBeNull()
    expect(stats!.textContent).toContain('3 errors')
    expect(stats!.textContent).toContain('2 failed')
    expect(screen.getByText('Circuit open').closest('.hermod-step-stats')).toBe(stats)
    expect(screen.queryByText(/ERR$|FAILED|CIRCUIT BREAKER/)).toBeNull()
  })

  it('shows no stats line on a step with nothing to report', () => {
    const { container } = renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1' })
    expect(container.querySelector('.hermod-step-stats')).toBeNull()
  })

  it('keeps an agent step\'s tags inside the card body', () => {
    renderNode(AIAgentNode, { label: 'Triage', tools: [] })
    expect(screen.getByText('0 tools').closest('.hermod-step-body')).not.toBeNull()
  })

  it('draws a note like a step card, with a quiet placeholder', () => {
    const { container } = renderNode(NoteNode, {})
    expect(container.querySelector('.hermod-note')).not.toBeNull()
    expect(screen.getByText('Empty note')).toBeInTheDocument()
    expect(screen.queryByText('NOTE')).toBeNull()
  })
})
