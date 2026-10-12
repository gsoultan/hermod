import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { SwitchConfig } from '@/components/workflow/Transformation/configs/logic/SwitchConfig'
import { simulateTransformation } from '@/utils/transformationUtils'

// A case compares the switch field against either a fixed value or another
// field of the same record. A field is stored as a {{path}} reference, the
// form the engine's condition evaluator already resolves, so saved configs
// keep their shape and nothing on the backend has to learn a new key.
// Stateful like the real editor, so typed input accumulates.
const renderConfig = (initial: any) => {
  const updateNodeConfig = vi.fn()
  function Harness() {
    const [config, setConfig] = useState(initial)
    return (
      <SwitchConfig
        config={config}
        nodeId="n1"
        updateNodeConfig={(id, patch) => {
          updateNodeConfig(id, patch)
          setConfig((c: any) => ({ ...c, ...patch }))
        }}
        availableFields={['amount', 'limit', 'customer.region']}
      />
    )
  }
  render(
    <MantineProvider>
      <Harness />
    </MantineProvider>,
  )
  return updateNodeConfig
}

// Choose an option from one combobox; each Select keeps its own list mounted.
const pick = async (user: ReturnType<typeof userEvent.setup>, input: HTMLElement, name: string) => {
  await user.click(input)
  const list = await waitFor(() => {
    const el = document.getElementById(input.getAttribute('aria-controls') ?? '')
    if (!el) throw new Error('option list not open')
    return el
  })
  await user.click(within(list).getByRole('option', { name, hidden: true }))
}

const caseCard = (n: number) => within(screen.getByRole('group', { name: `Case ${n}` }))

describe('SwitchConfig case value source', () => {
  it('shows a static value in Value mode', () => {
    renderConfig({ field: 'amount', cases: [{ label: 'high', operator: '>', value: '100' }] })

    expect(caseCard(1).getByRole('radio', { name: 'Value' })).toBeChecked()
    expect(caseCard(1).getByLabelText('Compare to')).toHaveValue('100')
  })

  it('shows a saved field reference in Field mode with the field name', () => {
    renderConfig({ field: 'amount', cases: [{ label: 'high', operator: '>', value: '{{limit}}' }] })

    expect(caseCard(1).getByRole('radio', { name: 'Field' })).toBeChecked()
    expect(caseCard(1).getByLabelText('Compare to field')).toHaveValue('limit')
  })

  it('saves a picked field as a {{path}} reference', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'high', operator: '>', value: '' }],
    })

    await user.click(caseCard(1).getByRole('radio', { name: 'Field' }))
    await user.type(caseCard(1).getByLabelText('Compare to field'), 'customer.region')

    const patch = updateNodeConfig.mock.calls.at(-1)?.[1]
    expect(patch.cases[0].value).toBe('{{customer.region}}')
    expect(patch.cases[0].label).toBe('high')
  })

  it('switching back to Value clears the field reference', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'high', operator: '>', value: '{{limit}}' }],
    })

    await user.click(caseCard(1).getByRole('radio', { name: 'Value' }))

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('')
  })
})

describe('SwitchConfig functions', () => {
  it('wraps a picked field in the chosen function', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'eu', operator: '=', value: '' }],
    })

    await user.click(caseCard(1).getByRole('radio', { name: 'Field' }))
    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to function' }), 'lower')
    await user.type(caseCard(1).getByLabelText('Compare to field'), 'customer.region')

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{lower(source.customer.region)}}')
  })

  it('wraps a fixed value in the chosen function', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'eu', operator: '=', value: 'EU' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to function' }), 'lower')

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{lower("EU")}}')
  })

  it('shows a saved function of a field', () => {
    renderConfig({ field: 'lower(source.status)', cases: [{ label: 'x', operator: '=', value: '{{upper(source.code)}}' }] })

    expect(caseCard(1).getByRole('radio', { name: 'Field' })).toBeChecked()
    expect(caseCard(1).getByLabelText('Compare to field')).toHaveValue('code')
    expect(caseCard(1).getByRole('combobox', { name: 'Compare to function' })).toHaveValue('upper')
    expect(screen.getByRole('combobox', { name: 'Switch field' })).toHaveValue('status')
    expect(screen.getByRole('combobox', { name: 'Switch field function' })).toHaveValue('lower')
  })

  it('applies a function to the switch field', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ field: 'status', cases: [] })

    await pick(user, screen.getByRole('combobox', { name: 'Switch field function' }), 'trim')

    expect(updateNodeConfig.mock.calls.at(-1)?.[1]).toEqual({ field: 'trim(source.status)' })
  })

  it('keeps a hand-written value untouched and offers no function for it', () => {
    renderConfig({ field: 'amount', cases: [{ label: 'x', operator: '=', value: '{{concat(source.a, source.b)}}' }] })

    expect(caseCard(1).getByLabelText('Compare to')).toHaveValue('{{concat(source.a, source.b)}}')
    expect(caseCard(1).getByRole('combobox', { name: 'Compare to function' })).toBeDisabled()
  })
})

describe('switch simulation', () => {
  const cases = [
    { label: 'over_limit', operator: '>', value: '{{limit}}' },
    { label: 'exact', operator: '=', value: '7' },
  ]

  it('compares against another field of the record', () => {
    const res = simulateTransformation('switch', { field: 'amount', cases }, { amount: 150, limit: 100 })
    expect(res.metadata.matchedLabel).toBe('over_limit')
  })

  it('honours the case operator', () => {
    const res = simulateTransformation('switch', { field: 'amount', cases: [{ label: 'big', operator: '>', value: '10' }] }, { amount: 50 })
    expect(res.metadata.matchedLabel).toBe('big')
  })

  it('falls through to default', () => {
    const res = simulateTransformation('switch', { field: 'amount', cases }, { amount: 50, limit: 100 })
    expect(res.metadata.matchedLabel).toBe('default')
  })
})
