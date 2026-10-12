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
import { modelAction } from '../test/buttons'

/**
 * Models: where a vhost registers the models its workflows and applications
 * call. A model is an address on a model server, not a file; the page saves
 * it, tests it with one row, and makes the serving key an application uses.
 */

type Model = { name: string; backend: string; url: string; serving: boolean; remote_model?: string; mcp_exposed?: boolean; [k: string]: unknown }

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

/** The names of the items in the menu `item` belongs to, in order. */
const menuLabels = (item: HTMLElement) =>
  within(item.closest('[role="menu"]') as HTMLElement).getAllByRole('menuitem', { hidden: true }).map((i) => i.getAttribute('aria-label'))

describe('Models page', () => {
  it('has one primary action, Add model, which first asks whether to train or connect', async () => {
    modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    expect(await screen.findByText('fraud')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^add model$/i })).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /^train a model$/i })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /^add model$/i }))
    const dialog = await screen.findByRole('dialog', { name: /add a model/i })
    expect(within(dialog).getByRole('button', { name: /train a new model from my data/i })).toHaveAccessibleDescription(/learns/i)
    const connect = within(dialog).getByRole('button', { name: /connect a model that's already running somewhere/i })
    expect(connect).toHaveAccessibleDescription(/kserve|mlflow/i)
    await user.click(connect)
    expect(await screen.findByRole('dialog', { name: /connect a model/i })).toBeInTheDocument()
  })

  it('connects a model on an Open Inference Protocol server in two short steps', async () => {
    const writes = modelsApi([])
    renderPage()
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /^add model$/i }))[0])
    await user.click(await screen.findByRole('button', { name: /connect a model/i }))
    const dialog = await screen.findByRole('dialog', { name: /connect a model/i })
    expect(within(dialog).getByRole('radio', { name: /open inference/i })).toBeChecked()
    await user.type(within(dialog).getByRole('textbox', { name: /^name in hermod/i }), 'churn')
    await user.type(within(dialog).getByRole('textbox', { name: /^server address/i }), 'http://ml:8080')
    await user.type(within(dialog).getByRole('textbox', { name: /^model name on the server/i }), 'churn')
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))

    await user.type(within(dialog).getByRole('textbox', { name: /^inputs/i }), 'age, plan')
    await user.click(within(dialog).getByRole('button', { name: /save model/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    const put = writes.find((w) => w.method === 'PUT')!
    expect(put.path).toBe('tenant-a/churn')
    expect(put.body).toEqual({
      backend: 'oip', url: 'http://ml:8080', remote_model: 'churn', remote_version: '', token_secret: '',
      input_name: '', features: ['age', 'plan'], timeout_ms: 0, description: '',
    })
    expect(await screen.findByText('churn')).toBeInTheDocument()
  })

  it('says how to fix each field before moving on', async () => {
    const writes = modelsApi([])
    renderPage()
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /^add model$/i }))[0])
    await user.click(await screen.findByRole('button', { name: /connect a model/i }))
    const dialog = await screen.findByRole('dialog', { name: /connect a model/i })
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))

    expect(within(dialog).getByRole('textbox', { name: /^name in hermod/i })).toHaveAccessibleDescription(/give the model a name, such as churn/i)
    expect(within(dialog).getByRole('textbox', { name: /^server address/i })).toHaveAccessibleDescription(/enter the server's address/i)
    expect(within(dialog).getByRole('textbox', { name: /^model name on the server/i })).toHaveAccessibleDescription(/enter the model's name on the server/i)

    await user.type(within(dialog).getByRole('textbox', { name: /^name in hermod/i }), '1st model')
    await user.type(within(dialog).getByRole('textbox', { name: /^server address/i }), 'ml:8080')
    await user.type(within(dialog).getByRole('textbox', { name: /^model name on the server/i }), 'team/churn')
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
    expect(within(dialog).getByRole('textbox', { name: /^name in hermod/i })).toHaveAccessibleDescription(/start with a letter/i)
    expect(within(dialog).getByRole('textbox', { name: /^server address/i })).toHaveAccessibleDescription(/start the address with http:\/\/ or https:\/\//i)
    expect(within(dialog).getByRole('textbox', { name: /^model name on the server/i })).toHaveAccessibleDescription(/leave out slashes/i)
    expect(within(dialog).queryByRole('textbox', { name: /^inputs/i })).not.toBeInTheDocument()
    expect(writes).toHaveLength(0)
  })

  it('asks only for an address for an MLflow server, and keeps the rarely needed options folded away', async () => {
    const writes = modelsApi([])
    renderPage()
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /^add model$/i }))[0])
    await user.click(await screen.findByRole('button', { name: /connect a model/i }))
    const dialog = await screen.findByRole('dialog', { name: /connect a model/i })
    await user.click(within(dialog).getByRole('radio', { name: /mlflow/i }))
    expect(within(dialog).queryByRole('textbox', { name: /^model name on the server/i })).not.toBeInTheDocument()
    await user.type(within(dialog).getByRole('textbox', { name: /^name in hermod/i }), 'fraud')
    await user.type(within(dialog).getByRole('textbox', { name: /^server address/i }), 'https://mlflow:5000')
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))

    expect(within(dialog).queryByRole('textbox', { name: /token/i })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: /advanced options/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^access token secret/i }), 'ML_TOKEN')
    await user.type(within(dialog).getByRole('textbox', { name: /^timeout/i }), '5000')
    await user.click(within(dialog).getByRole('button', { name: /^back$/i }))
    expect(within(dialog).getByRole('textbox', { name: /^server address/i })).toHaveValue('https://mlflow:5000')
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
    await user.click(within(dialog).getByRole('button', { name: /save model/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    expect(writes.find((w) => w.method === 'PUT')!.body).toMatchObject({
      backend: 'mlflow', url: 'https://mlflow:5000', remote_model: '', token_secret: 'ML_TOKEN', timeout_ms: 5000,
    })
  })

  it('edits a connected model from its menu, keeping what is already set', async () => {
    const writes = modelsApi([{
      name: 'fraud', backend: 'oip', url: 'http://kserve', remote_model: 'fraud-v', serving: false,
      token_secret: 'ML_TOKEN', input_name: 'input', features: ['amount', 'country'], timeout_ms: 2000, description: 'Card fraud',
    } as Model])
    renderPage()
    const user = userEvent.setup()

    await user.click(await modelAction(user, 'fraud', /edit connection/i))
    const dialog = await screen.findByRole('dialog', { name: /edit fraud/i })
    expect(within(dialog).queryByRole('textbox', { name: /^name in hermod/i })).not.toBeInTheDocument()
    await user.clear(within(dialog).getByRole('textbox', { name: /^server address/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^server address/i }), 'http://kserve:9000')
    await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
    await user.click(within(dialog).getByRole('button', { name: /save model/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    expect(writes.find((w) => w.method === 'PUT')).toEqual({
      method: 'PUT', path: 'tenant-a/fraud', body: {
        backend: 'oip', url: 'http://kserve:9000', remote_model: 'fraud-v', remote_version: '', token_secret: 'ML_TOKEN',
        input_name: 'input', features: ['amount', 'country'], timeout_ms: 2000, description: 'Card fraud',
      },
    })
  })

  it('explains what a model is when there is none, and points at Add model', async () => {
    modelsApi([])
    renderPage()

    expect(await screen.findByText(/no models in tenant-a yet/i)).toBeInTheDocument()
    expect(screen.getByText(/a model learns from past examples/i)).toBeInTheDocument()
    expect(screen.getByText(/press add model/i)).toBeInTheDocument()
  })

  it('shows each model with a plain status and its other actions in one menu', async () => {
    modelsApi([
      { name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: true },
      { name: 'churn', backend: 'hermod-ml', url: '', serving: false, remote_version: '2' } as Model,
      { name: 'upsell', backend: 'hermod-ml', url: '', serving: false } as Model,
    ])
    renderPage()
    const user = userEvent.setup()

    const table = await screen.findByRole('table', { name: /models/i })
    const row = (name: string) => within(table).getByText(name).closest('tr') as HTMLElement
    await within(table).findByText('upsell')
    expect(within(row('fraud')).getByText(/^connected$/i)).toBeInTheDocument()
    expect(within(row('churn')).getByText(/^version 2 live$/i)).toBeInTheDocument()
    expect(within(row('upsell')).getByText(/^not live yet$/i)).toBeInTheDocument()
    expect(within(row('fraud')).getAllByRole('button')).toHaveLength(1)

    expect(menuLabels(await modelAction(user, 'churn', /try it with one row/i))).toEqual([
      'Try it with one row', 'Versions and going live', 'Serving key for apps', 'Monitoring', 'Where it is scored',
      'Train again', 'Retrain automatically', 'Delete model',
    ])
  })

  it('offers a model served elsewhere its own menu: no versions or training, but editing its connection', async () => {
    modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: true }])
    renderPage()
    const user = userEvent.setup()

    expect(menuLabels(await modelAction(user, 'fraud', /try it with one row/i))).toEqual([
      'Try it with one row', 'Serving key for apps', 'Monitoring', 'Edit connection', 'Delete model',
    ])
  })

  it('puts datasets, jobs and limits on their own tabs, each saying what it is for', async () => {
    modelsApi([])
    renderPage()
    const user = userEvent.setup()

    const tabs = await screen.findByRole('tablist')
    expect(within(tabs).getAllByRole('tab').map((t) => t.textContent)).toEqual(['Models', 'Data', 'Training jobs', 'Limits'])
    expect(screen.queryByRole('region', { name: 'ML quotas' })).not.toBeInTheDocument()
    await user.click(within(tabs).getByRole('tab', { name: 'Limits' }))
    expect(await screen.findByRole('region', { name: 'ML quotas' })).toBeInTheDocument()
    for (const name of ['Models', 'Data', 'Training jobs', 'Limits']) {
      expect(within(tabs).getByRole('tab', { name })).toHaveAccessibleDescription(/\w{3,}/)
    }
  })

  it('deletes a model after asking', async () => {
    const writes = modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    await user.click(await modelAction(user, 'fraud', /delete model/i))
    const confirm = await screen.findByRole('dialog', { name: /delete model/i })
    await user.click(within(confirm).getByRole('button', { name: /delete model/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'DELETE')).toEqual({ method: 'DELETE', path: 'tenant-a/fraud' }))
  })

  it('tests a model with one row and shows its answer', async () => {
    const writes = modelsApi([{ name: 'fraud', backend: 'mlflow', url: 'http://ml', serving: false }])
    renderPage()
    const user = userEvent.setup()

    await user.click(await modelAction(user, 'fraud', 'Try it with one row'))
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

    await user.click(await modelAction(user, 'fraud', 'Serving key for apps'))
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
    const user = userEvent.setup()

    await user.click(await screen.findByRole('tab', { name: 'Limits' }))
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

    await user.click(await screen.findByRole('tab', { name: 'Limits' }))
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

    await user.click(await modelAction(user, 'fraud', 'Monitoring'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('tab', { name: /prediction log/i })).toBeInTheDocument()
    expect(await within(dialog).findByText(/logging is off/i)).toBeInTheDocument()
  })
})
