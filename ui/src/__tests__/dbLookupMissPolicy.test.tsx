import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DBLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/DBLookupConfig'

// The backend has supported three miss policies since onMiss was introduced
// (pkg/comm/transformer/lookup/onmiss.go) and the whole point of them is that a
// lookup that finds no row is an explicit, auditable choice rather than a silent
// passthrough. The editor never offered the choice, so every workflow ran on
// whichever policy was inferred from whether Default Value happened to be set.
//
// The control has to mirror resolveMissPolicy exactly, including its inference,
// or it shows a policy the pipeline is not running.
describe('db_lookup miss policy', () => {
  const renderAdvanced = async (config: any, updateNodeConfig = () => {}) => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <DBLookupConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          availableFields={[]}
          sources={[{ id: 's1', name: 'db', type: 'postgres' }]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    await user.click(screen.getByRole('tab', { name: /advanced/i }))
    return user
  }

  // The label is associated with the combobox and with its listbox, so the role
  // is what disambiguates.
  const policyInput = () =>
    screen.getByRole('combobox', { name: /when no row matches/i }) as HTMLInputElement

  it('shows passthrough when neither onMiss nor a default value is set', async () => {
    await renderAdvanced({ sourceId: 's1' })
    expect(policyInput().value).toMatch(/pass the message through/i)
  })

  it('infers the default-value policy the same way the backend does', async () => {
    // resolveMissPolicy: an unset onMiss with a defaultValue means the author
    // wanted misses filled in.
    await renderAdvanced({ sourceId: 's1', defaultValue: 'unknown' })
    expect(policyInput().value).toMatch(/default value/i)
  })

  it('writes the chosen policy to onMiss', async () => {
    const updateNodeConfig = vi.fn()
    const user = await renderAdvanced({ sourceId: 's1' }, updateNodeConfig)

    await user.click(policyInput())
    expect(policyInput()).toHaveAttribute('aria-expanded', 'true')

    // By text, not by role: jsdom has no layout, so floating-ui's hide()
    // middleware reports the reference as hidden and Mantine puts the open
    // dropdown at display:none, which takes its options out of the
    // accessibility tree. Asserting aria-expanded above is what actually
    // establishes that the dropdown opened.
    await user.click(screen.getByText('Fail the message'))

    expect(updateNodeConfig).toHaveBeenCalledWith('n1', { onMiss: 'fail' })
  })

  it('warns that the default policy writes nothing without a default value', async () => {
    // applyMissPolicy only writes when defaultValue is non-empty, so this
    // combination behaves exactly like passthrough while claiming not to.
    await renderAdvanced({ sourceId: 's1', onMiss: 'default', defaultValue: '' })
    expect(screen.getByTestId('lookup-miss-warning')).toHaveTextContent(/nothing will be written/i)
  })

  it('does not warn once a default value is set', async () => {
    await renderAdvanced({ sourceId: 's1', onMiss: 'default', defaultValue: 'unknown' })
    expect(screen.queryByTestId('lookup-miss-warning')).toBeNull()
  })
})
