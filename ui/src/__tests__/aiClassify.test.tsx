import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { ReactFlowProvider } from '@xyflow/react'
import { useState } from 'react'
import { describe, it, expect } from 'vitest'
import {
  classifyBranchIds,
  classifyLabels,
  UNSURE_BRANCH,
} from '@/pages/workflows/WorkflowEditor/nodes/branchHandleId'
import { AIClassifyNode } from '@/pages/workflows/WorkflowEditor/nodes/AIClassifyNode'
import { AIClassifyConfig } from '@/components/workflow/Transformation/configs/ai/AIClassifyConfig'

// ai_classify returns the chosen label as the branch (nodes/ai/classify.go),
// or "unsure" (genai.UnsureLabel). The engine sends the message down the
// edges whose label -- or, without one, sourceHandle -- equals that branch
// (registry_workflow.go edgeLabel), and an edge drawn from a handle carries
// the handle's id. So each handle's id must be the label exactly as the
// engine names it: trimmed, with blank labels dropped (genai.parseLabel).
describe('ai_classify branch names', () => {
  it('reads labels in every shape genai.parseLabels accepts', () => {
    expect(classifyLabels([{ label: ' urgent ', description: 'Needs a reply today' }, { label: '' }, 'spam'])).toEqual([
      { label: 'urgent', description: 'Needs a reply today' },
      { label: 'spam', description: '' },
    ])
    expect(classifyLabels('[{"label":"a"},{"label":"b","description":"B"}]')).toEqual([
      { label: 'a', description: '' },
      { label: 'b', description: 'B' },
    ])
    expect(classifyLabels('a, b ,,c')).toEqual([
      { label: 'a', description: '' },
      { label: 'b', description: '' },
      { label: 'c', description: '' },
    ])
    expect(classifyLabels(undefined)).toEqual([])
  })

  it('draws one branch per label, as the engine names it, then unsure', () => {
    expect(UNSURE_BRANCH).toBe('unsure')
    expect(classifyBranchIds([{ label: ' billing ' }, { label: '' }, { label: 'tech support' }])).toEqual([
      'billing',
      'tech support',
      'unsure',
    ])
  })

  it('draws a repeated label, or one called unsure, only once', () => {
    expect(classifyBranchIds(['a', 'a', 'unsure'])).toEqual(['a', 'unsure'])
  })
})

describe('the ai_classify canvas node', () => {
  it('has a source handle for each label and for unsure, ids equal to the branch names', () => {
    const { container } = render(
      <MantineProvider>
        <ReactFlowProvider>
          <AIClassifyNode
            id="c1"
            data={{ label: 'Triage', labels: [{ label: 'billing' }, { label: ' refund ' }] }}
            selected={false}
          />
        </ReactFlowProvider>
      </MantineProvider>,
    )
    const ids = [...container.querySelectorAll('.react-flow__handle.source')].map((h) => h.getAttribute('data-handleid'))
    // BaseNode draws its children in two places, so each id appears twice.
    expect([...new Set(ids)]).toEqual(['billing', 'refund', 'unsure'])
    expect(screen.getAllByText('unsure').length).toBeGreaterThan(0)
  })
})

let saved: Record<string, any> = {}

function Harness({ initial }: { initial: Record<string, any> }) {
  const [config, setConfig] = useState(initial)
  saved = config
  return (
    <AIClassifyConfig
      config={config}
      nodeId="n1"
      updateNodeConfig={(_id: string, patch: any) => setConfig((c: any) => ({ ...c, ...patch }))}
    />
  )
}

const renderConfig = (initial: Record<string, any>) => {
  saved = initial
  return render(
    <MantineProvider>
      <Harness initial={initial} />
    </MantineProvider>,
  )
}

describe('AI Classify settings', () => {
  it('asks for at least one label', () => {
    renderConfig({ provider: 'anthropic' })
    expect(screen.getByText(/at least one label is required/i)).toBeInTheDocument()
  })

  it('stores labels as a list of {label, description}', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic' })
    await user.click(screen.getByRole('button', { name: /add label/i }))
    fireEvent.change(screen.getByRole('textbox', { name: /label 1 name/i }), { target: { value: 'billing' } })
    fireEvent.change(screen.getByRole('textbox', { name: /label 1 description/i }), {
      target: { value: 'Invoices and payments' },
    })
    expect(saved.labels).toEqual([{ label: 'billing', description: 'Invoices and payments' }])
    expect(screen.queryByText(/at least one label is required/i)).toBeNull()
  })

  it('edits labels that arrived as text', () => {
    renderConfig({ provider: 'anthropic', labels: 'billing, refund' })
    expect(screen.getByRole('textbox', { name: /label 2 name/i })).toHaveValue('refund')
    fireEvent.change(screen.getByRole('textbox', { name: /label 2 name/i }), { target: { value: 'refunds' } })
    expect(saved.labels).toEqual([
      { label: 'billing', description: '' },
      { label: 'refunds', description: '' },
    ])
  })

  it('removes a label', async () => {
    const user = userEvent.setup()
    renderConfig({ provider: 'anthropic', labels: [{ label: 'a' }, { label: 'b' }] })
    await user.click(screen.getByRole('button', { name: /remove label 1/i }))
    expect(saved.labels).toEqual([{ label: 'b' }])
  })

  it('warns about a repeated label and about the reserved unsure', () => {
    renderConfig({ provider: 'anthropic', labels: [{ label: 'a' }, { label: 'a' }, { label: 'unsure' }] })
    expect(screen.getByText(/more than once/i)).toBeInTheDocument()
    expect(screen.getByText(/already the branch/i)).toBeInTheDocument()
  })

  it('saves the threshold as text, and the default output fields are shown', () => {
    renderConfig({ provider: 'anthropic', labels: [{ label: 'a' }] })
    fireEvent.change(screen.getByRole('textbox', { name: /confidence threshold/i }), { target: { value: '0.7' } })
    expect(saved.threshold).toBe('0.7')
    expect(screen.getByRole('textbox', { name: /label field/i })).toHaveAttribute('placeholder', 'ai_label')
    expect(screen.getByRole('textbox', { name: /confidence field/i })).toHaveAttribute('placeholder', 'ai_confidence')
  })
})
