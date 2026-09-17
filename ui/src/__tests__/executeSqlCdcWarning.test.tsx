import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SQLConfig } from '@/components/workflow/Transformation/configs/enrichment/SQLConfig'

// execute_sql is the one node that names a source and is NOT blocked from CDC
// ones, and the reason is that it does something different with it. db_lookup
// and batch_sql read — the rule keeps query load off a database already paying
// for logical replication. execute_sql only writes: it runs ExecContext and can
// return nothing but a row count (pkg/comm/transformer/advanced/execute_sql.go).
//
// Its hazard is a feedback loop, not load: a write into a table that is in the
// publication produces a change event that comes back round the pipeline. That
// is scoped to the table, while use_cdc is scoped to the source, so refusing
// the source outright would break the ordinary case of writing an audit or
// status row to a table nobody streams. Warn, name the real risk, and leave the
// choice with the operator.
describe('execute_sql against a CDC target', () => {
  const nonCDC = { id: 'ops', name: 'ops', type: 'postgres', config: { use_cdc: 'false' } }
  const cdc = { id: 'orders', name: 'orders', type: 'postgres', config: { use_cdc: 'true' } }

  const renderConfig = (config: any) =>
    render(
      <MantineProvider>
        <SQLConfig
          config={config}
          updateNodeConfig={vi.fn()}
          nodeId="n1"
          sources={[nonCDC, cdc]}
          availableFields={[]}
        />
      </MantineProvider>
    )

  it('warns about the feedback loop when the target is a CDC source', () => {
    renderConfig({ sourceId: 'orders' })
    const warning = screen.getByTestId('execute-sql-cdc-warning')
    expect(warning).toHaveTextContent(/orders/)
    expect(warning).toHaveTextContent(/back into the pipeline|feedback/i)
  })

  it('does not warn when the target is a non-CDC source', () => {
    renderConfig({ sourceId: 'ops' })
    expect(screen.queryByTestId('execute-sql-cdc-warning')).toBeNull()
  })

  it('reads the legacy sourceID spelling too', () => {
    renderConfig({ sourceID: 'orders' })
    expect(screen.getByTestId('execute-sql-cdc-warning')).toBeInTheDocument()
  })

  // The distinction from the other two pickers, asserted rather than assumed:
  // a CDC source here stays choosable.
  it('still lets you choose a CDC source', async () => {
    const user = userEvent.setup()
    renderConfig({})
    await user.click(screen.getByRole('combobox', { name: /database source/i }))

    const option = screen.getByText(/orders/).closest('[role="option"]')
    expect(option).not.toBeNull()
    expect(option).not.toHaveAttribute('data-combobox-disabled')
  })
})
