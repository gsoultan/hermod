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

type Model = { name: string; backend: string; url: string; serving: boolean; remote_model?: string }

function modelsApi(initial: Model[]) {
  const state = structuredClone(initial)
  const writes: Array<{ method: string; path: string; body?: any }> = []
  server.use(
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

function renderPage() {
  signInAs('Editor')
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
