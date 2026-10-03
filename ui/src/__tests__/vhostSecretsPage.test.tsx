import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { SecretsPage } from '@/pages/secrets/SecretsPage'

/**
 * A vhost keeps its own secrets, and this page is where they are saved. There
 * was nowhere in Hermod to save one: a new API key meant changing the server's
 * environment. The page lists names and never a value -- the API does not
 * return one, and the value box is never filled in for you.
 */

type Stored = { name: string; updated_by: string; updated_at: string }

/** An in-memory secrets API for one test, recording every write. */
function secretsApi(initial: Record<string, Stored[]>) {
  const state = structuredClone(initial)
  const writes: Array<{ method: string; vhost: string; name: string; body?: unknown }> = []
  server.use(
    http.get('/api/vhosts/:vhost/secrets', ({ params }) => {
      const list = state[params.vhost as string] ?? []
      return HttpResponse.json({ data: list, total: list.length })
    }),
    http.put('/api/vhosts/:vhost/secrets/:name', async ({ params, request }) => {
      const vhost = params.vhost as string
      const name = params.name as string
      const body = await request.json()
      writes.push({ method: 'PUT', vhost, name, body })
      const list = (state[vhost] ??= [])
      const entry = { name, updated_by: 'tester', updated_at: '2026-10-02T10:00:00Z' }
      const at = list.findIndex((s) => s.name === name)
      if (at >= 0) list[at] = entry
      else list.push(entry)
      return new HttpResponse(null, { status: 204 })
    }),
    http.delete('/api/vhosts/:vhost/secrets/:name', ({ params }) => {
      const vhost = params.vhost as string
      const name = params.name as string
      writes.push({ method: 'DELETE', vhost, name })
      state[vhost] = (state[vhost] ?? []).filter((s) => s.name !== name)
      return new HttpResponse(null, { status: 204 })
    }),
  )
  return writes
}

function SelectVHost({ vhost, available }: { vhost: string; available: string[] }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts(available)
    // Set once: the page under test is what changes them afterwards.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(vhost = 'tenant-a', available = ['tenant-a', 'tenant-b']) {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <ConfirmProvider>
            <SelectVHost vhost={vhost} available={available} />
            <SecretsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

const apiKey: Stored = { name: 'API_KEY', updated_by: 'ada', updated_at: '2026-10-01T08:00:00Z' }

describe('Secrets page', () => {
  it('lists the selected vhost’s secrets by name, with who changed them', async () => {
    secretsApi({ 'tenant-a': [apiKey], 'tenant-b': [{ ...apiKey, name: 'OTHER' }] })
    renderPage('tenant-a')

    const row = (await screen.findByText('API_KEY')).closest('tr')!
    expect(within(row).getByText('ada')).toBeInTheDocument()
    // Where a secret lives is the point of the page, so it says which vhost this is.
    expect(screen.getByLabelText('VHost shown')).toHaveTextContent('tenant-a')
    expect(within(row).getByText('secret("API_KEY")')).toBeInTheDocument()
    expect(screen.queryByText('OTHER')).toBeNull()
  })

  it('says how to start when the vhost has no secrets', async () => {
    secretsApi({})
    renderPage('tenant-a')

    expect(await screen.findByText(/no secrets in tenant-a yet/i)).toBeInTheDocument()
  })

  it('saves a new secret and shows it in the list', async () => {
    const writes = secretsApi({})
    const user = userEvent.setup()
    renderPage('tenant-a')

    await user.click(await screen.findByRole('button', { name: /add secret/i }))
    const dialog = await screen.findByRole('dialog', { name: /add secret/i })
    await user.type(within(dialog).getByLabelText(/^name/i), 'API_KEY')
    await user.type(within(dialog).getByLabelText(/^value/i), 's3cret')
    await user.click(within(dialog).getByRole('button', { name: /^save secret$/i }))

    await waitFor(() =>
      expect(writes).toEqual([{ method: 'PUT', vhost: 'tenant-a', name: 'API_KEY', body: { value: 's3cret' } }]),
    )
    expect(await screen.findByText('secret("API_KEY")')).toBeInTheDocument()
  })

  it('refuses a name an expression could not write, before sending anything', async () => {
    const writes = secretsApi({})
    const user = userEvent.setup()
    renderPage('tenant-a')

    await user.click(await screen.findByRole('button', { name: /add secret/i }))
    const dialog = await screen.findByRole('dialog', { name: /add secret/i })
    await user.type(within(dialog).getByLabelText(/^name/i), 'api-key')
    await user.type(within(dialog).getByLabelText(/^value/i), 's3cret')
    await user.click(within(dialog).getByRole('button', { name: /^save secret$/i }))

    expect(await within(dialog).findByText(/"api-key" is not a valid name/i)).toBeInTheDocument()
    expect(writes).toEqual([])
  })

  it('rotates a secret without ever showing the old value', async () => {
    const writes = secretsApi({ 'tenant-a': [apiKey] })
    const user = userEvent.setup()
    renderPage('tenant-a')

    await user.click(await screen.findByRole('button', { name: /rotate API_KEY/i }))
    const dialog = await screen.findByRole('dialog', { name: /rotate API_KEY/i })
    const value = within(dialog).getByLabelText(/^new value/i)
    expect(value).toHaveValue('')
    await user.type(value, 'next')
    await user.click(within(dialog).getByRole('button', { name: /^save secret$/i }))

    await waitFor(() =>
      expect(writes).toEqual([{ method: 'PUT', vhost: 'tenant-a', name: 'API_KEY', body: { value: 'next' } }]),
    )
  })

  it('deletes a secret only after the deletion is confirmed', async () => {
    const writes = secretsApi({ 'tenant-a': [apiKey] })
    const user = userEvent.setup()
    renderPage('tenant-a')

    await user.click(await screen.findByRole('button', { name: /delete API_KEY/i }))
    expect(writes).toEqual([])
    const confirm = await screen.findByRole('dialog', { name: /delete secret/i })
    await user.click(within(confirm).getByRole('button', { name: /^delete secret$/i }))

    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', vhost: 'tenant-a', name: 'API_KEY' }]))
    await waitFor(() => expect(screen.queryByText('secret("API_KEY")')).toBeNull())
  })

  it('asks which vhost when every vhost is selected', async () => {
    secretsApi({ 'tenant-b': [{ ...apiKey, name: 'B_KEY' }] })
    const user = userEvent.setup()
    renderPage('all', ['tenant-a', 'tenant-b'])

    expect(await screen.findByText(/choose a vhost/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /add secret/i })).toBeNull()

    await user.click(screen.getByPlaceholderText('Select one'))
    await user.click(await screen.findByRole('option', { name: 'tenant-b', hidden: true }))

    expect(await screen.findByText('B_KEY')).toBeInTheDocument()
  })

  it('shows what the server said when a save is refused', async () => {
    secretsApi({})
    server.use(
      http.put('/api/vhosts/:vhost/secrets/:name', () =>
        HttpResponse.json({ error: 'Forbidden: you do not have access to this vhost' }, { status: 403 }),
      ),
    )
    const user = userEvent.setup()
    renderPage('tenant-a')

    await user.click(await screen.findByRole('button', { name: /add secret/i }))
    const dialog = await screen.findByRole('dialog', { name: /add secret/i })
    await user.type(within(dialog).getByLabelText(/^name/i), 'API_KEY')
    await user.type(within(dialog).getByLabelText(/^value/i), 's3cret')
    await user.click(within(dialog).getByRole('button', { name: /^save secret$/i }))

    expect(await within(dialog).findByText(/do not have access to this vhost/i)).toBeInTheDocument()
  })
})
