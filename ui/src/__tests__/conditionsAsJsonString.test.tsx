import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ConditionConfig } from '../components/workflow/Transformation/configs/logic/ConditionConfig'
import { FilterConfig } from '../components/workflow/Transformation/configs/data/FilterConfig'

/**
 * A workflow saved through the API can carry `conditions` as a JSON string
 * rather than an array. The engine reads both (evaluator.ParseObjectList), so
 * such a workflow runs, but the editor handed the string straight to
 * FilterEditor, whose `.map` threw and took the whole editor down when the
 * node was opened.
 */

const fields = [{ path: 'after.status' }]

type Form = typeof ConditionConfig | typeof FilterConfig

const renderForm = (Form: Form, conditions: unknown) => {
  const updateNodeConfig = vi.fn()
  render(
    <MantineProvider>
      <Form
        config={{ conditions }}
        updateNodeConfig={updateNodeConfig}
        nodeId="n1"
        availableFields={fields}
      />
    </MantineProvider>,
  )
  return updateNodeConfig
}

describe.each([
  ['If (ConditionConfig)', ConditionConfig, /every message takes the TRUE branch/i],
  ['Filter (FilterConfig)', FilterConfig, /no filters defined/i],
])('%s reads conditions saved as a JSON string', (_name, Form, emptyText) => {
  it('renders each condition from the string', async () => {
    renderForm(Form, JSON.stringify([{ field: 'after.status', operator: '=', value: 'paid' }]))
    expect(await screen.findByDisplayValue('paid')).toBeInTheDocument()
    expect(screen.getByDisplayValue('after.status')).toBeInTheDocument()
  })

  it('still renders conditions saved as an array', async () => {
    renderForm(Form, [{ field: 'after.status', operator: '=', value: 'shipped' }])
    expect(await screen.findByDisplayValue('shipped')).toBeInTheDocument()
  })

  it.each([
    ['malformed JSON', '[{"field":'],
    ['a JSON object', '{"field":"x"}'],
    ['an empty string', ''],
  ])('shows an empty list for %s instead of crashing', async (_label, raw) => {
    renderForm(Form, raw)
    expect(await screen.findByText(emptyText)).toBeInTheDocument()
  })
})
