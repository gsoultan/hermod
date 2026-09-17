import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { BatchSQLSourceConfig } from '@/components/workflow/Source/BatchSQLSourceConfig'
import type { Source } from '@/types'

// A batch_sql source holds no connection of its own: it names another source in
// `source_id` and runs whole queries against that source's database on a cron.
// The engine refuses to build one whose delegate is a CDC source
// (Registry.requireNonCDCDelegate) -- a scheduled full-table query does not
// belong on a database already serving logical replication, and if the delegate
// is a source node too, every row arrives twice.
//
// The picker listed every database source regardless, so the rule was only
// discoverable by starting the workflow and reading the failure. It has to
// read use_cdc the way the backend does: opt-out, so a source with no key is a
// CDC source, with SQL Server the one exception.
describe('batch_sql delegate picker', () => {
  const src = (over: Partial<Source>): Source =>
    ({ id: 'x', name: 'x', type: 'postgres', vhost: 'default', config: {}, ...over }) as Source

  const nonCDC = src({ id: 'reporting', name: 'reporting', config: { use_cdc: 'false' } })
  const cdc = src({ id: 'orders', name: 'orders', config: { use_cdc: 'true' } })
  const unset = src({ id: 'legacy', name: 'legacy', type: 'mysql', config: {} })
  const sqlServer = src({ id: 'erp', name: 'erp', type: 'mssql', config: { use_cdc: 'true' } })

  const allSources = [nonCDC, cdc, unset, sqlServer]

  const renderConfig = (config: Record<string, any> = {}, updateConfig = vi.fn()) => {
    render(
      <MantineProvider>
        <BatchSQLSourceConfig config={config} updateConfig={updateConfig} allSources={allSources} />
      </MantineProvider>
    )
    return updateConfig
  }

  const openPicker = async (config: Record<string, any> = {}, updateConfig = vi.fn()) => {
    const user = userEvent.setup()
    renderConfig(config, updateConfig)
    await user.click(screen.getByRole('combobox', { name: /database source/i }))
    return { user, updateConfig }
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

  it('offers a delegate with CDC switched off', async () => {
    await openPicker()
    expect(option(/reporting/)).not.toHaveAttribute('data-combobox-disabled')
  })

  it('does not let you pick a CDC delegate', async () => {
    const { user, updateConfig } = await openPicker()
    const cdcOption = option(/orders/)
    expect(cdcOption).toHaveAttribute('data-combobox-disabled')
    await user.click(cdcOption)
    expect(updateConfig).not.toHaveBeenCalled()
  })

  it('treats a delegate with no use_cdc key as CDC, the same reading the factory uses', async () => {
    await openPicker()
    expect(option(/legacy/)).toHaveAttribute('data-combobox-disabled')
  })

  it('offers SQL Server even with CDC on, the one documented exception', async () => {
    await openPicker()
    expect(option(/erp/)).not.toHaveAttribute('data-combobox-disabled')
  })

  it('says why a CDC delegate cannot be used instead of hiding it', async () => {
    await openPicker()
    expect(option(/orders/)).toHaveTextContent(/cdc/i)
  })

  // Filtering the entry out would blank the field and read as lost
  // configuration. The source keeps showing what it is set to, with the reason
  // it will not start.
  it('flags a source already pointing at a CDC delegate without blanking it', () => {
    renderConfig({ source_id: 'orders' })

    const input = screen.getByRole('combobox', { name: /database source/i }) as HTMLInputElement
    expect(input.value).toMatch(/orders/)
    expect(screen.getByTestId('batch-sql-source-cdc-error')).toHaveTextContent(/non-cdc/i)
  })

  it('does not flag a source pointing at a non-CDC delegate', () => {
    renderConfig({ source_id: 'reporting' })
    expect(screen.queryByTestId('batch-sql-source-cdc-error')).toBeNull()
  })
})
