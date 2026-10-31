import { render } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import type { ComponentType } from 'react'
import { describe, it, expect } from 'vitest'
import { ConditionNode } from '@/pages/workflows/WorkflowEditor/nodes/ConditionNode'
import { ApprovalNode } from '@/pages/workflows/WorkflowEditor/nodes/ApprovalNode'
import { AIClassifyNode } from '@/pages/workflows/WorkflowEditor/nodes/AIClassifyNode'
import { DeduplicateNode, ForeachNode, RouterNode, SwitchNode } from '@/pages/workflows/WorkflowEditor/nodes/MiscNodes'

// A step with more than one way out used to float its branch names over the
// card at fixed offsets, where they ran into the step's name, while its
// handles were pinned 25px apart whatever the card drew. Each way out is now
// a row in the card's foot: its name, and its handle level with it.
const renderNode = (Node: ComponentType<any>, data: Record<string, unknown>) =>
  render(
    <MantineProvider>
      <ReactFlowProvider>
        <Node id="n1" data={{ label: 'Is Submission Required True?', ...data }} selected={false} />
      </ReactFlowProvider>
    </MantineProvider>,
  )

const branchRows = (container: HTMLElement) =>
  [...container.querySelectorAll('.react-flow__handle.source')].map((h) => {
    const row = h.closest('.hermod-branch')
    return { id: h.getAttribute('data-handleid'), row: row?.textContent ?? null, top: (h as HTMLElement).style.top }
  })

describe('steps with several ways out', () => {
  it.each([
    ['Condition', ConditionNode, {}, [['true', 'True'], ['false', 'False']]],
    ['Approval', ApprovalNode, {}, [['approved', 'Approved'], ['rejected', 'Rejected']]],
    ['Switch', SwitchNode, { cases: [{ label: 'eu' }, { label: '' }] }, [['eu', 'eu'], ['case_1', 'case_1'], ['default', 'default']]],
    ['Router', RouterNode, { rules: [{ label: 'big orders' }] }, [['big orders', 'big orders'], ['default', 'default']]],
    ['AI Classify', AIClassifyNode, { labels: 'billing' }, [['billing', 'billing'], ['unsure', 'unsure']]],
    ['Foreach', ForeachNode, {}, [[null, 'Each item'], ['error', 'Error']]],
    ['Deduplicate', DeduplicateNode, {}, [[null, 'Unique'], ['duplicate', 'Duplicate']]],
  ])('%s puts each handle on a row with its name', (_kind, Node, data, expected) => {
    const { container } = renderNode(Node, data)
    const rows = branchRows(container)
    expect(rows.map((r) => [r.id, r.row])).toEqual(expected)
    // Level with its row, not pinned at a fixed offset from the card's top.
    for (const r of rows) expect(r.top).toBe('50%')
  })

  it('draws no branch name outside its row', () => {
    const { container } = renderNode(ConditionNode, {})
    const names = [...container.querySelectorAll('*')].filter(
      (el) => el.children.length === 0 && /^(true|false)$/i.test(el.textContent ?? ''),
    )
    expect(names).toHaveLength(2)
    for (const el of names) expect(el.closest('.hermod-branch')).not.toBeNull()
  })
})
