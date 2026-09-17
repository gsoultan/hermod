import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { BatchSQLSourceConfig } from '@/components/workflow/Source/BatchSQLSourceConfig'

// A batch_sql source has no inbound message, so a {{ }} token other than
// {{.last_value}} can only come from the source's own parameters. The backend
// now binds them (pkg/comm/source/batchsql/batchsql.go prepareQuery) and fails
// the run on an undefined token, so the editor has to be able to define them.
describe('batch_sql query parameters', () => {
  const renderConfig = (config: any, updateConfig = () => {}) =>
    render(
      <MantineProvider>
        <BatchSQLSourceConfig config={config} updateConfig={updateConfig} allSources={[]} />
      </MantineProvider>
    )

  const parametersInput = () => screen.getByRole('textbox', { name: /query parameters/i })

  it('offers a parameters editor', () => {
    renderConfig({})
    expect(parametersInput()).toBeInTheDocument()
  })

  it('writes valid JSON to the parameters config key', async () => {
    const updateConfig = vi.fn()
    const user = userEvent.setup()
    renderConfig({}, updateConfig)
    await user.click(parametersInput())
    await user.paste('{"ids":["u1","u2"]}')
    expect(updateConfig).toHaveBeenCalledWith('parameters', '{"ids":["u1","u2"]}')
  })

  it('flags malformed JSON, which the backend rejects rather than ignores', async () => {
    renderConfig({ parameters: '{"ids":' })
    expect(await screen.findByText(/must be a valid JSON object/i)).toBeInTheDocument()
  })

  it('flags a JSON array, which is not an object of named parameters', async () => {
    renderConfig({ parameters: '["u1"]' })
    expect(await screen.findByText(/must be a valid JSON object/i)).toBeInTheDocument()
  })

  it('accepts a well-formed object', () => {
    renderConfig({ parameters: '{"ids":["u1"]}' })
    expect(screen.queryByText(/must be a valid JSON object/i)).not.toBeInTheDocument()
  })
})
