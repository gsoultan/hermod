import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import type { ComponentType } from 'react'
import { describe, it, expect } from 'vitest'
import { SinkNode } from '@/pages/workflows/WorkflowEditor/nodes/SourceSinkNodes'
import { AIAgentNode } from '@/pages/workflows/WorkflowEditor/nodes/AIAgentNode'
import { NoteNode } from '@/pages/workflows/WorkflowEditor/nodes/MiscNodes'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { latestRecord } from '@/pages/workflows/WorkflowEditor/nodes/latestRecord'

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

  // On a live run the step's latest record used to sit behind an unlabelled
  // eye icon. It is now a line on the card that shows the record itself and
  // opens it in full.
  it('shows the latest record on the card and opens it', () => {
    useWorkflowStore.setState({ nodeSamples: { n1: { order_id: 4211, region: 'eu', total: 129.5 } }, sampleInspectorOpened: false, sampleNodeId: null } as any)
    renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1' })
    const open = screen.getByRole('button', { name: /latest record/i })
    expect(open.textContent).toContain('"order_id":4211')
    expect(screen.queryByRole('button', { name: /^view$/i })).toBeNull()
    fireEvent.click(open)
    expect(useWorkflowStore.getState().sampleNodeId).toBe('n1')
    expect(useWorkflowStore.getState().sampleInspectorOpened).toBe(true)
    useWorkflowStore.setState({ nodeSamples: {}, sampleInspectorOpened: false, sampleNodeId: null } as any)
  })

  it('shows the newest of several samples', () => {
    useWorkflowStore.setState({ nodeSamples: { n1: [{ n: 1 }, { n: 2 }] } } as any)
    renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1' })
    expect(screen.getByRole('button', { name: /latest record/i }).textContent).toContain('{"n":2}')
    useWorkflowStore.setState({ nodeSamples: {} } as any)
  })

  it('shows no record line before any record has passed', () => {
    renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1' })
    expect(screen.queryByRole('button', { name: /latest record/i })).toBeNull()
  })

  // A live sample is the whole message: the row under `after`, plus the
  // message's id, operation, table and metadata. The card leads with the row.
  it('previews the row a message carries, not its envelope', () => {
    useWorkflowStore.setState({ nodeSamples: { n1: { after: { order_id: 4271, region: 'eu' }, id: 'm1', operation: 'create', table: 'webhook', metadata: { http_method: 'POST' } } } } as any)
    renderNode(SinkNode, { label: 'Warehouse', ref_id: 'k1' })
    const text = screen.getByRole('button', { name: /latest record/i }).textContent ?? ''
    expect(text).toContain('{"order_id":4271,"region":"eu"}')
    expect(text).not.toContain('after')
    useWorkflowStore.setState({ nodeSamples: {} } as any)
  })
})

describe('latestRecord', () => {
  it('takes the newest sample and its row', () => {
    expect(latestRecord([{ after: { n: 1 } }, { after: { n: 2 } }])).toEqual({ n: 2 })
  })
  it('takes the row before a delete', () => {
    expect(latestRecord({ before: { n: 1 }, after: null, operation: 'delete' })).toEqual({ n: 1 })
  })
  it('keeps a sample that is not a message as it is', () => {
    expect(latestRecord({ n: 1 })).toEqual({ n: 1 })
    expect(latestRecord('plain text')).toBe('plain text')
  })
})
