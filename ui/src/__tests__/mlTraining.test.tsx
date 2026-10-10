import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect, useState } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { MLTrainConfig } from '@/components/workflow/Transformation/configs/ml/MLTrainConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Training: with an ML worker configured, the Models page keeps the vhost's
 * datasets (uploaded CSV/Excel, or read from a database source), trains a
 * model on one, shows how it scored and whether it went live, and puts any
 * version live. The Train Model node does the same from a workflow.
 */

const customers = {
  name: 'customers', rows: 120, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' }],
}

const version = (v: string, score: number) => ({
  model: 'churn', version: v, task: 'classification', algorithm: 'random_forest', dataset: 'customers',
  target: 'churned', features: ['age', 'plan'], metrics: { accuracy: score, score }, rows: { train: 96, test: 24 },
  created_at: '2026-10-10T00:00:00Z',
})

function trainingApi({ ready = true } = {}) {
  const writes: Array<{ method: string; path: string; body?: any; contentType?: string | null }> = []
  const models: any[] = [{ name: 'churn', backend: 'hermod-ml', url: '', remote_version: '1', serving: false, features: ['age', 'plan'] }]
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: ready, ready })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: models.length })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json({ ...customers, sample: [{ age: 30, plan: 'pro', churned: 'no' }] })),
    http.get('/api/sources', () => HttpResponse.json({ data: [{ id: 'crm', name: 'CRM database', type: 'postgres' }, { id: 'hook', name: 'Webhook', type: 'webhook' }], total: 2 })),
    http.put('/api/vhosts/:vhost/ml/datasets/:name/file', async ({ params, request }) => {
      writes.push({ method: 'UPLOAD', path: `${params.vhost}/${params.name}${new URL(request.url).search}`, body: await request.text() })
      return HttpResponse.json({ ...customers, name: params.name })
    }),
    http.post('/api/vhosts/:vhost/ml/datasets/:name/query', async ({ params, request }) => {
      writes.push({ method: 'QUERY', path: `${params.vhost}/${params.name}`, body: await request.json() })
      return HttpResponse.json({ name: params.name, rows: 42 })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'TRAIN', path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({
        model: { name: params.name, backend: 'hermod-ml', remote_version: '1' },
        version: version('2', 0.71), live: false, reason: 'score 0.71 is below the minimum 0.8',
      })
    }),
    http.get('/api/vhosts/:vhost/ml/models/:name/versions', () => HttpResponse.json({ data: [version('2', 0.71), version('1', 0.9)], total: 2 })),
    http.post('/api/vhosts/:vhost/ml/models/:name/versions/:version/promote', ({ params }) => {
      writes.push({ method: 'PROMOTE', path: `${params.vhost}/${params.name}/${params.version}` })
      models[0].remote_version = params.version
      return HttpResponse.json(models[0])
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

/** Picks an option of a Mantine Select or MultiSelect by its accessible name. */
async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  // The option of this input's own list: two pickers may offer the same column.
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

describe('Training on the Models page', () => {
  it('lists the datasets and shows a trained model with its live version', async () => {
    trainingApi()
    renderPage()
    expect(await screen.findByText('customers')).toBeInTheDocument()
    expect(await screen.findByText(/version 1 live/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /train a model/i })).toBeInTheDocument()
  })

  it('says how to turn training on when no worker is configured', async () => {
    trainingApi({ ready: false })
    renderPage()
    expect(await screen.findByText(/HERMOD_ML_WORKER_URL/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /train a model/i })).not.toBeInTheDocument()
  })

  it('uploads a CSV file as a dataset', async () => {
    const writes = trainingApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /upload a file/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /dataset name/i }), 'sales')
    const file = new File(['region,amount\nEU,10\n'], 'sales.csv', { type: 'text/csv' })
    await user.upload(within(dialog).getByLabelText(/file/i, { selector: 'input[type=file]' }), file)
    await user.click(within(dialog).getByRole('button', { name: /^upload$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'UPLOAD')).toBeTruthy())
    const up = writes.find((w) => w.method === 'UPLOAD')!
    expect(up.path).toBe('tenant-a/sales?format=csv')
    expect(up.body).toBe('region,amount\nEU,10\n')
  })

  it('reads a dataset from a database source of the vhost', async () => {
    const writes = trainingApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /from a database/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /dataset name/i }), 'orders')
    await pick(user, dialog, /database source/i, 'CRM database')
    await user.type(within(dialog).getByRole('textbox', { name: /query/i }), 'SELECT * FROM orders')
    await user.click(within(dialog).getByRole('button', { name: /read rows/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'QUERY')).toBeTruthy())
    expect(writes.find((w) => w.method === 'QUERY')!.body).toEqual({ source_id: 'crm', query: 'SELECT * FROM orders', max_rows: 0 })
    expect(await within(dialog).findByText(/42 rows/)).toBeInTheDocument()
  })

  it('trains a model and says why the new version did not go live', async () => {
    const writes = trainingApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /train a model/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'churn2')
    await pick(user, dialog, /^dataset/i, 'customers')
    await pick(user, dialog, /column to predict/i, 'churned')
    await user.click(within(dialog).getByRole('radio', { name: /if it scores/i }))
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    const train = writes.find((w) => w.method === 'TRAIN')!
    expect(train.path).toBe('tenant-a/churn2')
    expect(train.body).toMatchObject({ dataset: 'customers', target: 'churned', task: 'auto', algorithm: 'auto', go_live: { mode: 'if', metric: 'score', min: 0.8 } })
    expect(await within(dialog).findByText(/below the minimum/)).toBeInTheDocument()
    expect(within(dialog).getByText(/version 2/i)).toBeInTheDocument()
  })

  it('puts an earlier version live from the version list', async () => {
    const writes = trainingApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Versions of churn' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(await within(dialog).findByRole('button', { name: 'Put version 2 live' }))
    await waitFor(() => expect(writes.find((w) => w.method === 'PROMOTE')?.path).toBe('tenant-a/churn/2'))
  })
})

function NodeHarness({ onConfig }: { onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>({})
  return (
    <MLTrainConfig
      nodeId="n1"
      config={config}
      sources={[{ id: 'crm', name: 'CRM database', type: 'postgres', vhost: 'tenant-a' }]}
      updateNodeConfig={(_id: string, patch: any) => {
        setConfig((c) => {
          const next = { ...c, ...patch }
          onConfig(next)
          return next
        })
      }}
    />
  )
}

describe('Train Model node', () => {
  it('is offered in the palette under Machine Learning, with an editor', () => {
    const ml = NODE_CATEGORIES.find((c) => c.title === 'Machine Learning')
    expect(ml?.items.map((i: any) => i.subType)).toContain('ml_train')
    expect(TRANSFORM_CONFIGS.ml_train).toBe(MLTrainConfig)
  })

  // Text is pasted, not typed: each keystroke re-renders the whole node form,
  // and this test is about what the form saves, not about typing. The longer
  // timeout is for a loaded machine, as in functionPicker.test.tsx.
  it('saves the model, dataset, target and go-live rule the engine reads', async () => {
    trainingApi()
    signInAs('Editor')
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={qc}>
          <NodeHarness onConfig={(c) => { last = c }} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    const user = userEvent.setup()
    const form = document.body
    await user.click(screen.getByRole('textbox', { name: /model name/i }))
    await user.paste('churn')
    await screen.findByRole('combobox', { name: /^dataset/i })
    await pick(user, form, /^dataset/i, 'customers')
    await pick(user, form, /column to predict/i, 'churned')
    await user.click(screen.getByRole('radio', { name: /if it scores/i }))
    await pick(user, form, /database source/i, 'CRM database')
    await user.click(screen.getByRole('textbox', { name: /query/i }))
    await user.paste('SELECT * FROM customers')

    await waitFor(() => expect(last).toMatchObject({
      model: 'churn', dataset: 'customers', target: 'churned', goLive: 'if', goLiveMetric: 'score', goLiveMin: '0.8',
      sourceId: 'crm', query: 'SELECT * FROM customers',
    }))
  }, 20000)
})
