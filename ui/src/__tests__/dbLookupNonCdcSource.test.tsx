import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { DBLookupConfig } from '@/components/workflow/Transformation/configs/enrichment/DBLookupConfig'

// A db_lookup runs a query per message against the source it names, so the
// backend refuses to run one against a database that is also serving change
// data capture (requireNonCDCSource in pkg/comm/transformer/lookup/db_lookup.go).
//
// The editor offered every database source in the dropdown regardless, so the
// only way to find out you had picked a CDC source was to start the workflow
// and read the error off a message that failed. The picker has to apply the
// same rule the pipeline does, including its reading of the flag: use_cdc is
// opt-out (internal/factory/factory.go), so a source with no key is a CDC
// source, and SQL Server is the documented exception.
describe('db_lookup source picker', () => {
  const nonCDC = { id: 'customers', name: 'customers', type: 'postgres', config: { use_cdc: 'false' } }
  const cdc = { id: 'orders', name: 'orders', type: 'postgres', config: { use_cdc: 'true' } }
  const unset = { id: 'legacy', name: 'legacy', type: 'mysql', config: {} }
  const sqlServer = { id: 'erp', name: 'erp', type: 'mssql', config: { use_cdc: 'true' } }

  const renderPicker = async (config: any = {}, updateNodeConfig = vi.fn()) => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <DBLookupConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          availableFields={[]}
          sources={[nonCDC, cdc, unset, sqlServer]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    await user.click(screen.getByRole('combobox', { name: /database source/i }))
    return { user, updateNodeConfig }
  }

  // By text, not by role: jsdom has no layout, so floating-ui's hide()
  // middleware reports the reference as hidden and Mantine puts the open
  // dropdown at display:none, which takes its options out of the accessibility
  // tree. dbLookupMissPolicy.test.tsx hits the same thing.
  const option = (name: RegExp) => {
    const el = screen.getByText(name).closest('[role="option"]')
    if (!el) throw new Error(`no option matching ${name}`)
    return el
  }

  it('offers a source with CDC switched off', async () => {
    await renderPicker()
    expect(option(/customers/)).not.toHaveAttribute('data-combobox-disabled')
  })

  it('does not let you pick a CDC source', async () => {
    const { user, updateNodeConfig } = await renderPicker()
    const cdcOption = option(/orders/)
    expect(cdcOption).toHaveAttribute('data-combobox-disabled')
    await user.click(cdcOption)
    expect(updateNodeConfig).not.toHaveBeenCalled()
  })

  it('treats a source with no use_cdc key as CDC, the same reading the factory uses', async () => {
    await renderPicker()
    expect(option(/legacy/)).toHaveAttribute('data-combobox-disabled')
  })

  it('offers SQL Server even with CDC on, the one documented exception', async () => {
    await renderPicker()
    expect(option(/erp/)).not.toHaveAttribute('data-combobox-disabled')
  })

  it('says why a CDC source cannot be used instead of hiding it', async () => {
    await renderPicker()
    expect(option(/orders/)).toHaveTextContent(/cdc/i)
  })

  // Filtering the entry out would blank the field and read as lost
  // configuration. The node keeps showing what it is actually set to, with the
  // reason it will not run.
  it('flags a node already pointing at a CDC source without blanking it', async () => {
    render(
      <MantineProvider>
        <DBLookupConfig
          config={{ sourceId: 'orders' }}
          updateNodeConfig={vi.fn()}
          nodeId="n1"
          availableFields={[]}
          sources={[nonCDC, cdc, unset, sqlServer]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )

    const input = screen.getByRole('combobox', { name: /database source/i }) as HTMLInputElement
    expect(input.value).toMatch(/orders/)
    expect(screen.getByTestId('lookup-source-cdc-error')).toHaveTextContent(/non-cdc/i)
  })

  it('does not flag a node pointing at a non-CDC source', () => {
    render(
      <MantineProvider>
        <DBLookupConfig
          config={{ sourceId: 'customers' }}
          updateNodeConfig={vi.fn()}
          nodeId="n1"
          availableFields={[]}
          sources={[nonCDC, cdc, unset, sqlServer]}
          onTest={() => {}}
          testing={false}
        />
      </MantineProvider>
    )
    expect(screen.queryByTestId('lookup-source-cdc-error')).toBeNull()
  })
})
