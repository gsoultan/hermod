import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ModelsPage } from '@/pages/ml/ModelsPage'

/**
 * Models: where a vhost registers the models its workflows and applications
 * call. A model is an address on a model server, not a file; the page saves
 * it, tests it with one row, and makes the serving key an application uses.
 */

type Model = { name: string; backend: string; url: string; serving: boolean; remote_model?: string; mcp_exposed?: boolean }

const noQuotas = {
  max_datasets: null, max_dataset_rows: null, max_dataset_bytes: null,
  max_models: null, max_concurrent_trainings: null, max_predictions_per_second: null,
}

function modelsApi(initial: Model[], quotas: Record<string, unknown> = { ...noQuotas, max_models: 5 }) {
  const state = structuredClone(initial)
  const writes: Array<{ method: string; path: string; body?: any }> = []
  let own: Record<string, unknown> = { vhost: 'tenant-a', ...quotas }
  const effective = () => {
    const defaults: Record<string, number> = { max_predictions_per_second: 50 }
    return Object.fromEntries(Object.keys(noQuotas).map((k) => [k, (own[k] as number | null) ?? defaults[k] ?? 0]))
  }
  const quotaBody = () => ({ quotas: own, defaults: { ...noQuotas, max_predictions_per_second: 50 }, effective: effective() })
  server.use(
    http.get('/api/vhosts/:vhost/ml/quotas', () => HttpResponse.json(quotaBody())),
    http.put('/api/vhosts/:vhost/ml/quotas', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'QUOTAS', path: `${params.vhost}`, body })
      own = { vhost: params.vhost, ...body }
      return HttpResponse.json(quotaBody())
    }),
    http.put('/api/vhosts/:vhost/ml/models/:name/mcp', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'MCP', path: `${params.vhost}/${params.name}`, body })
      const m = state.find((s) => s.name === params.name)!
      m.mcp_exposed = body.exposed
      return HttpResponse.json(m)
    }),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: state, total: state.length })),
    http.put('/api/vhosts/:vhost/ml/models/:name', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'PUT', path: `${params.vhost}/${params.name}`, body })
      const m = { name: params.name as string, serving: false, ...body }
      const at = state.findIndex((s) => s.name === m.name)
      if (at >= 0) state[at] = m
      else state.push(m)
      return HttpResponse.json(m)
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/predict', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'PREDICT', path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({ model: params.name, predictions: [{ prediction: 0.42 }] })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/serving-key', ({ params }) => {
      writes.push({ method: 'KEY', path: `${params.vhost}/${params.name}` })
      const m = state.find((s) => s.name === params.name)
      if (m) m.serving = true
      return HttpResponse.json({ key: 'hml_new-serving-key' })
    }),
    http.delete('/api/vhosts/:vhost/ml/models/:name', ({ params }) => {
      writes.push({ method: 'DELETE', path: `${params.vhost}/${params.name}` })
      return new HttpResponse(null, { status: 204 })
    }),
  )
  return writes
}

function SelectVHost({ vhost }: { vhost: string }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts([vhost])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(role = 'Editor') {
  signInAs(role)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <ConfirmProvider>
            <SelectVHost vhost="tenant-a" />
            <ModelsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Models page', () => {
  it('registers a model on an Open Inference Protocol server', async () => {
    const writes = modelsApi([])
    renderPage()
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /add model/i }))[0])
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /^name/i }), 'churn')
    await user.type(within(dialog).getByRole('textbox', { name: /server url/i }), 'http://ml:8080')
    await user.type(within(dialog).getByRole('textbox', { name: /model on the server/i }), 'churn')
    await user.click(within(dialog).getByRole('button', { name: /save model/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    const put = writes.find((w) => w.method === 'PUT')!
    expect(put.path).toBe('tenant-a/churn')
    expect(put.body).toMatchObject({ backend: 'oip', url: 'http://ml:8080', remote_model: 'churn' })
    expect(await screen.findByText('churn')).toBeInTheDocument()
  })

  it('tests a model with one row and shows its answer', async () => {
    const writes = modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Test fraud' }))
    const dialog = await screen.findByRole('dialog')
    const input = within(dialog).getByRole('textbox', { name: /row/i })
    await user.clear(input)
    await user.click(input)
    await user.paste('{"amount": 120}')
    await user.click(within(dialog).getByRole('button', { name: /run/i }))

    expect(await within(dialog).findByText(/0\.42/)).toBeInTheDocument()
    expect(writes.find((w) => w.method === 'PREDICT')?.body).toEqual({ instances: [{ amount: 120 }] })
  })

  it('makes a serving key and shows it once', async () => {
    modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Serving key for fraud' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: /make a key/i }))
    expect(await within(dialog).findByText('hml_new-serving-key')).toBeInTheDocument()
    expect(within(dialog).getByText(/\/api\/ml\/serve\/tenant-a\/fraud/)).toBeInTheDocument()
  })

  it('exposes a model to MCP clients as a predict tool', async () => {
    const writes = modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    const toggle = await screen.findByRole('switch', { name: 'Expose fraud to MCP' })
    expect(toggle).not.toBeChecked()
    await user.click(toggle)

    await waitFor(() => expect(writes.find((w) => w.method === 'MCP')).toBeTruthy())
    expect(writes.find((w) => w.method === 'MCP')).toEqual({ method: 'MCP', path: 'tenant-a/fraud', body: { exposed: true } })
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Expose fraud to MCP' })).toBeChecked())
  })

  it('shows the vhost its ML quotas without letting a non-administrator change them', async () => {
    modelsApi([])
    renderPage('Viewer')

    const quotas = await screen.findByRole('region', { name: 'ML quotas' })
    expect(await within(quotas).findByText('5')).toBeInTheDocument()
    expect(within(quotas).getByText('50 per second')).toBeInTheDocument()
    expect(within(quotas).getAllByText('No limit').length).toBeGreaterThan(0)
    expect(within(quotas).queryByRole('button', { name: /save quotas/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('switch', { name: /expose .* to MCP/i })).not.toBeInTheDocument()
  })

  it('lets an administrator set the quotas, leaving a blank one to the server default', async () => {
    const writes = modelsApi([])
    renderPage('Administrator')
    const user = userEvent.setup()

    const quotas = await screen.findByRole('region', { name: 'ML quotas' })
    const models = await within(quotas).findByRole('textbox', { name: 'Models' })
    await waitFor(() => expect(models).toHaveValue('5'))
    await user.clear(models)
    await user.type(models, '10')
    await user.type(within(quotas).getByRole('textbox', { name: 'Concurrent trainings' }), '2')
    await user.click(within(quotas).getByRole('button', { name: /save quotas/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'QUOTAS')).toBeTruthy())
    expect(writes.find((w) => w.method === 'QUOTAS')!.body).toEqual({ ...noQuotas, max_models: 10, max_concurrent_trainings: 2 })
  })

  it('opens a model\'s monitoring', async () => {
    modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    server.use(http.get('/api/vhosts/:vhost/ml/models/:name/predictions', () => HttpResponse.json({ data: [], total: 0 })))
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: 'Monitoring of fraud' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('tab', { name: /prediction log/i })).toBeInTheDocument()
    expect(await within(dialog).findByText(/logging is off/i)).toBeInTheDocument()
  })
})
