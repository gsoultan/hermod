import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { PostgresSinkConfig } from '@/components/workflow/Sink/PostgresSinkConfig'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => () => {},
  Link: (props: any) => <button {...props} />,
}))

/** Mirrors SinkWizard: the config object lives above this component. */
function Harness() {
  const [config, setConfig] = useState<Record<string, any>>({
    host: 'db', port: '5432', use_existing_table: 'true', table: '',
  })
  return (
    <PostgresSinkConfig
      type="postgres"
      config={config}
      updateConfig={(k: string, v: any) => setConfig((c) => ({ ...c, [k]: v }))}
      tables={[]}
      discoveredDatabases={[]}
      isFetchingDBs={false}
      loadingTables={false}
      tablesError={null}
      fetchDatabases={() => {}}
      discoverTables={() => {}}
      availableFields={[]}
    />
  )
}

/**
 * `config.table` is bound to an Autocomplete the user types into, and an effect
 * fired column discovery on every change of it. Typing "orders" meant six POSTs
 * to /api/sinks/discover/columns — six live queries against the sink's database,
 * five of them for table names that do not exist.
 */
describe('Postgres sink column discovery', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/sinks/discover/tables', () => HttpResponse.json([])),
      http.post('/api/sinks/discover/tables', () => HttpResponse.json([]))
    )
  })

  it('does not query the database on every keystroke of the table name', async () => {
    // The realistic shape: a partial name is not a table, so discovery fails
    // and the "already have mappings" guard never engages. Only the complete
    // name resolves.
    const attempted: string[] = []
    server.use(
      http.post('/api/sinks/discover/columns', async ({ request }) => {
        const body = (await request.json()) as any
        attempted.push(body.table)
        if (body.table !== 'orders') return new HttpResponse(null, { status: 404 })
        return HttpResponse.json([{ name: 'id' }, { name: 'total' }])
      })
    )

    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <Harness />
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    const table = await screen.findByPlaceholderText('Select or type table name')
    await user.type(table, 'orders')

    // Settle past any debounce.
    await new Promise((r) => setTimeout(r, 900))

    // Typing "orders" must not mean six live queries against the sink.
    expect(attempted).toEqual(['orders'])
  }, 20000)

  it('still discovers columns once the table name settles', async () => {
    let discoveries = 0
    server.use(
      http.post('/api/sinks/discover/columns', () => {
        discoveries += 1
        return HttpResponse.json([{ name: 'id' }])
      })
    )

    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <Harness />
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    const table = await screen.findByPlaceholderText('Select or type table name')
    await user.type(table, 'orders')

    await waitFor(() => expect(discoveries).toBe(1), { timeout: 5000 })
  }, 20000)
})
