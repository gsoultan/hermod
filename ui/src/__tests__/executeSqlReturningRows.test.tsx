import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SQLConfig } from '@/components/workflow/Transformation/configs/enrichment/SQLConfig'

// `INSERT ... RETURNING id` wrote the row and the id went nowhere: the node had
// no field to put returned rows in, so Run Preview showed the message exactly
// as it arrived. The transformer now keeps them under `resultField`
// (pkg/comm/transformer/advanced/execute_sql.go). That is opt-in, so the editor
// has to both offer the field and say when a statement is returning rows that
// nothing is keeping — otherwise the preview is as empty as it was before.
describe('execute_sql returned rows', () => {
  const ops = { id: 'ops', name: 'ops', type: 'postgres', config: { use_cdc: 'false' } }

  const renderConfig = (config: any, updateNodeConfig = vi.fn()) =>
    render(
      <MantineProvider>
        <SQLConfig
          config={config}
          updateNodeConfig={updateNodeConfig}
          nodeId="n1"
          sources={[ops]}
          availableFields={[]}
        />
      </MantineProvider>
    )

  it('offers a field to keep the rows a statement returns', async () => {
    const updateNodeConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({ sourceId: 'ops' }, updateNodeConfig)

    await user.type(screen.getByLabelText(/returned rows field/i), 'i')

    expect(updateNodeConfig).toHaveBeenCalledWith('n1', { resultField: 'i' })
  })

  it.each([
    ['RETURNING', 'INSERT INTO t (a) VALUES ({{.a}}) RETURNING id'],
    ['lower-case returning', 'update t set a = 1 where id = {{.id}} returning *'],
    ['SQL Server OUTPUT', 'INSERT INTO t (a) OUTPUT inserted.id VALUES ({{.a}})'],
  ])('says the rows are being discarded when a %s statement has no field', (_name, queryTemplate) => {
    renderConfig({ sourceId: 'ops', queryTemplate })

    expect(screen.getByTestId('execute-sql-returning-hint')).toHaveTextContent(/returned rows field/i)
  })

  it('stops saying so once a field is named', () => {
    renderConfig({
      sourceId: 'ops',
      queryTemplate: 'INSERT INTO t (a) VALUES (1) RETURNING id',
      resultField: 'inserted',
    })

    expect(screen.queryByTestId('execute-sql-returning-hint')).toBeNull()
  })

  it('says nothing about a statement that returns no rows', () => {
    renderConfig({ sourceId: 'ops', queryTemplate: "INSERT INTO t (note) VALUES ('returning soon')" })

    expect(screen.queryByTestId('execute-sql-returning-hint')).toBeNull()
  })

  // One row is the common case and reads as `inserted.id`. A statement that
  // returns several needs a list — chosen, not inferred from how many rows
  // happened to come back for the message in the preview.
  it('lets the node keep every returned row instead of the first', async () => {
    const updateNodeConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({ sourceId: 'ops', resultField: 'inserted' }, updateNodeConfig)

    const input = screen.getByRole('combobox', { name: /rows to keep/i }) as HTMLInputElement
    expect(input.value).toMatch(/first row/i)

    await user.click(input)
    await user.click(screen.getByText(/every row/i))

    expect(updateNodeConfig).toHaveBeenCalledWith('n1', { resultRows: 'all' })
  })

  it('does not ask how many rows to keep before there is a field to keep them in', () => {
    renderConfig({ sourceId: 'ops' })

    expect(screen.queryByRole('combobox', { name: /rows to keep/i })).toBeNull()
  })
})
