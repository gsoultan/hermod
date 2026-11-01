import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { SendToMimirModal } from '@/pages/ml/datasets/SendToMimirModal'

/**
 * Send to mimir-server: the address and key secret of a mimir model already
 * connected are offered, the column to predict comes from the dataset, and
 * the server's verdict on the rows is shown once they arrive. The key is
 * named, never typed.
 */

const columns = [{ name: 'age', type: 'number' as const }, { name: 'plan', type: 'string' as const }, { name: 'churned', type: 'bool' as const }]

function renderModal() {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <SendToMimirModal vhost="tenant-a" name="customers" columns={columns} onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Send a dataset to mimir-server', () => {
  it('offers a connected mimir server, sends the dataset and shows the verdict', async () => {
    let sent: any
    server.use(
      http.get('/api/vhosts/tenant-a/ml/models', () => HttpResponse.json({
        data: [{ name: 'churn', backend: 'mimir', url: 'http://mimir:8080', remote_model: 'm-1', token_secret: 'MIMIR_API_KEY', serving: false }],
        total: 1,
      })),
      http.get('/api/vhosts/tenant-a/secrets', () => HttpResponse.json({ data: [{ name: 'MIMIR_API_KEY' }] })),
      http.post('/api/vhosts/tenant-a/ml/datasets/customers/mimir', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ id: 'ds-1', name: 'customers', rows: 1200, target: 'churned', verdict: 'warn' })
      }),
    )
    renderModal()
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByRole('combobox', { name: /mimir server/i })).toHaveValue('http://mimir:8080')
    expect(within(dialog).getByRole('combobox', { name: /api key secret/i })).toHaveValue('MIMIR_API_KEY')
    await user.click(within(dialog).getByRole('combobox', { name: /column to predict/i }))
    await user.click(await screen.findByRole('option', { name: 'churned', hidden: true }))
    await user.click(within(dialog).getByRole('button', { name: /^send$/i }))

    expect(await within(dialog).findByText(/the dataset is on the server/i)).toBeInTheDocument()
    expect(sent).toEqual({ url: 'http://mimir:8080', token_secret: 'MIMIR_API_KEY', name: 'customers', target: 'churned' })
    expect(within(dialog).getByText('warn')).toBeInTheDocument()
    expect(within(dialog).getByText(/has concerns/i)).toBeInTheDocument()
  })

  it('refuses to send without a server or with a key typed in place of a secret name', async () => {
    server.use(
      http.get('/api/vhosts/tenant-a/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts/tenant-a/secrets', () => HttpResponse.json({ data: [] })),
    )
    renderModal()
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('combobox', { name: /api key secret/i }), 'mimir_abc.def')
    await user.click(within(dialog).getByRole('button', { name: /^send$/i }))
    expect(within(dialog).getByText(/enter the mimir server's address/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/enter the name, not the key/i)).toBeInTheDocument()
  })
})
