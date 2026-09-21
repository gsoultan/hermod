import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect, vi } from 'vitest'
import { SwitchConfig } from '@/components/workflow/Transformation/configs/logic/SwitchConfig'

// `cases` reaches this component in either shape. SwitchConfig writes a plain
// array, but the string form is what the engine's own tests, the workflow
// bundles and every other reader in the UI use -- MiscNodes.tsx and
// transformationUtils.ts both accept either. Reading only the array form meant
// a workflow created through the API rendered with no cases at all, and the
// first edit in the editor would have saved that emptiness back.
const renderConfig = (config: any) => {
  const updateNodeConfig = vi.fn()
  render(
    <MantineProvider>
      <SwitchConfig
        config={config}
        nodeId="n1"
        updateNodeConfig={updateNodeConfig}
        availableFields={['amount']}
      />
    </MantineProvider>,
  )
  return updateNodeConfig
}

const savedCases = [
  { label: 'high', operator: '>', value: '100' },
  { label: 'exact', operator: '=', value: '100' },
]

describe('SwitchConfig cases shape', () => {
  it.each([
    ['array', savedCases as any],
    ['JSON string', JSON.stringify(savedCases)],
  ])('renders every saved case when cases is a %s', (_name, cases) => {
    renderConfig({ field: 'amount', cases })

    expect(screen.getAllByLabelText('Branch Label')).toHaveLength(2)
    expect(screen.getByDisplayValue('high')).toBeInTheDocument()
    expect(screen.getByDisplayValue('exact')).toBeInTheDocument()
  })

  it('appends to a string-shaped list instead of discarding it', async () => {
    const user = userEvent.setup()
    const updateNodeConfig = renderConfig({
      field: 'amount',
      cases: JSON.stringify(savedCases),
    })

    await user.click(screen.getByRole('button', { name: /add case/i }))

    const patch = updateNodeConfig.mock.calls.at(-1)?.[1]
    expect(patch.cases).toHaveLength(3)
    expect(patch.cases.map((c: any) => c.label)).toEqual(['high', 'exact', 'case_3'])
  })
})
