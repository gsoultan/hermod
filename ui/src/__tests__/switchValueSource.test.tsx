import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { useState } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { SwitchConfig } from '@/components/workflow/Transformation/configs/logic/SwitchConfig'
import { SecretNamesContext } from '@/components/shared/TemplateField'
import { simulateTransformation } from '@/utils/transformationUtils'

// A case compares the switch field against either a fixed value or another
// field of the same record. A field is stored as a {{path}} reference, the
// form the engine's condition evaluator already resolves, so saved configs
// keep their shape and nothing on the backend has to learn a new key.
// Stateful like the real editor, so typed input accumulates.
const renderConfig = (initial: any, secretNames: string[] = []) => {
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
      <SecretNamesContext.Provider value={secretNames}>
        <Harness />
      </SecretNamesContext.Provider>
    </MantineProvider>,
  )
  return updateNodeConfig
}

// Choose an option from one combobox; each Select keeps its own list mounted.
const pick = async (user: ReturnType<typeof userEvent.setup>, input: HTMLElement, name: string | RegExp) => {
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
  it('shows a static value as text', () => {
    renderConfig({ field: 'amount', cases: [{ label: 'high', operator: '>', value: '100' }] })

    expect(caseCard(1).getByRole('combobox', { name: 'Compare to type' })).toHaveValue('Text')
    expect(caseCard(1).getByRole('textbox', { name: 'Compare to text' })).toHaveValue('100')
  })

  it('shows a saved field reference as a field with its name', () => {
    renderConfig({ field: 'amount', cases: [{ label: 'high', operator: '>', value: '{{limit}}' }] })

    expect(caseCard(1).getByRole('combobox', { name: 'Compare to type' })).toHaveValue('Field')
    expect(caseCard(1).getByRole('combobox', { name: 'Compare to field' })).toHaveValue('limit')
  })

  it('saves a picked field as a {{path}} reference', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'high', operator: '>', value: '' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to type' }), 'Field')
    await user.type(caseCard(1).getByRole('combobox', { name: 'Compare to field' }), 'customer.region')

    const patch = updateNodeConfig.mock.calls.at(-1)?.[1]
    expect(patch.cases[0].value).toBe('{{customer.region}}')
    expect(patch.cases[0].label).toBe('high')
  })

  it('switching back to text clears the field reference', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'high', operator: '>', value: '{{limit}}' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to type' }), 'Text')

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('')
  })

  it('compares against a secret of the vhost', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'vip', operator: '=', value: '' }],
    }, ['VIP_CODE'])

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to type' }), 'Secret')
    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to secret' }), 'VIP_CODE')

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{secret("VIP_CODE")}}')
  })
})

describe('SwitchConfig functions', () => {
  it('wraps a picked field in the chosen function', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'eu', operator: '=', value: '{{customer.region}}' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to type' }), 'Function')
    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to function' }), /^lower/)

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{lower(source.customer.region)}}')
  })

  it('wraps a fixed value in the chosen function', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'eu', operator: '=', value: 'EU' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to type' }), 'Function')
    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to function' }), /^lower/)

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{lower("EU")}}')
  })

  it('shows a saved function of a field on both sides', () => {
    renderConfig({ field: 'lower(source.status)', cases: [{ label: 'x', operator: '=', value: '{{upper(source.code)}}' }] })

    expect(caseCard(1).getByRole('combobox', { name: 'Compare to function' })).toHaveValue('upper')
    expect(caseCard(1).getByRole('combobox', { name: 'Compare to › text field' })).toHaveValue('code')
    expect(screen.getByRole('combobox', { name: 'Switch field function' })).toHaveValue('lower')
    expect(screen.getByRole('combobox', { name: 'Switch field › text field' })).toHaveValue('status')
  })

  it('applies a function to the switch field', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ field: 'status', cases: [] })

    await pick(user, screen.getByRole('combobox', { name: 'Switch field type' }), 'Function')
    await pick(user, screen.getByRole('combobox', { name: 'Switch field function' }), /^trim/)

    expect(updateNodeConfig.mock.calls.at(-1)?.[1]).toEqual({ field: 'trim(source.status)' })
  })

  it('nests one function inside another', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: [{ label: 'x', operator: '=', value: '{{upper(source.code)}}' }],
    })

    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to › text type' }), 'Function')
    await pick(user, caseCard(1).getByRole('combobox', { name: 'Compare to › text function' }), /^trim/)

    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('{{upper(trim(source.code))}}')
  })

  it('keeps text mixed with tokens editable as typed', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({ field: 'amount', cases: [{ label: 'x', operator: '=', value: 'id-{{limit}}' }] })

    const box = caseCard(1).getByRole('textbox', { name: 'Compare to text' })
    expect(box).toHaveValue('id-{{limit}}')
    await user.type(box, '!')
    expect(updateNodeConfig.mock.calls.at(-1)?.[1].cases[0].value).toBe('id-{{limit}}!')
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
