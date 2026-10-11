import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState, type ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { TrainModal } from '@/pages/ml/TrainModal'
import { ModelTestModal } from '@/pages/ml/ModelTestModal'
import { MonitoringModal } from '@/pages/ml/MonitoringModal'
import { MLPredictConfig } from '@/components/workflow/Transformation/configs/ml/MLPredictConfig'
import { describeRecommendation, type MLModel } from '@/lib/mlModels'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Recommendations: a model trained on who-had-what rows (an orders table)
 * that answers a person's top items, or the items like one item. Training
 * says in words how often the top 10 held what someone really picked next,
 * against recommending everyone the most popular items.
 */

const orders = {
  name: 'orders', rows: 5000, updated_at: '2026-10-10T00:00:00Z',
  columns: [
    { name: 'customer', type: 'string' }, { name: 'product', type: 'string' },
    { name: 'qty', type: 'number' }, { name: 'ordered_at', type: 'string' },
  ],
}

const recVersion = {
  model: 'recs', version: '1', task: 'recommendation', algorithm: 'item_similarity', dataset: 'orders', target: '', features: [],
  metrics: { 'hit_rate@10': 0.34, 'precision@10': 0.04, 'recall@10': 0.3, 'ndcg@10': 0.21, coverage: 0.45,
    'baseline_hit_rate@10': 0.12, 'baseline_ndcg@10': 0.07, baseline_coverage: 0.02, score: 0.21 },
  rows: { train: 4800, test: 200 }, created_at: '2026-10-10T00:00:00Z',
  recommend: { user: 'customer', item: 'product', strength: 'qty', time: 'ordered_at' },
  catalog: { users: 200, items: 60 },
  samples: [{ user: 'c-1', recommended: ['p-2', 'p-9'], picked: ['p-9'] }],
}

const recModel: MLModel = { name: 'recs', backend: 'hermod-ml', url: '', remote_version: '1', serving: false, task: 'recommendation' }

function api() {
  const calls: Array<{ path: string; body: any }> = []
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [orders], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json(orders)),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [recModel], total: 1 })),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      calls.push({ path: `train ${params.vhost}/${params.name}`, body: await request.json() })
      return HttpResponse.json({ model: { ...recModel, name: params.name }, version: recVersion, live: true, reason: 'go-live rule is always' })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/predict', async ({ params, request }) => {
      const body = (await request.json()) as any
      calls.push({ path: `predict ${params.vhost}/${params.name}`, body })
      return HttpResponse.json({
        model: params.name,
        items: [{ item: 'p-9', score: 0.8 }, { item: 'p-1', score: 41, reason: 'popular' }],
      })
    }),
  )
  return calls
}

function wrap(children: ReactNode) {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </MantineProvider>,
  )
}

async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

describe('describeRecommendation', () => {
  it('says the hit rate per 100 people against the popular baseline', () => {
    const d = describeRecommendation(recVersion.metrics)
    expect(d.hits).toBe('For 34 of 100 people, one of their top 10 was something they really picked next.')
    expect(d.baseline).toMatch(/most popular items would find it for 12 of 100: this model is better than that/)
    expect(d.coverage).toBe('Its top-10 lists used 45% of the items.')
    expect(describeRecommendation({ 'hit_rate@10': 0.1, 'baseline_hit_rate@10': 0.2 }).baseline).toMatch(/worse than/)
  })
})

describe('Training a recommender', () => {
  it('sends the who, what, how-strong and when columns and explains the result', async () => {
    const calls = api()
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    expect(within(dialog).queryByRole('textbox', { name: /column to predict/i })).not.toBeInTheDocument()

    await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'recs')
    await pick(user, dialog, /^dataset/i, 'orders')
    await pick(user, dialog, /^who/i, 'customer')
    await pick(user, dialog, /^what they picked/i, 'product')
    await pick(user, dialog, /^how much/i, 'qty')
    await pick(user, dialog, /^when/i, 'ordered_at')
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))

    await waitFor(() => expect(calls.find((c) => c.path.startsWith('train'))).toBeTruthy())
    const body = calls.find((c) => c.path.startsWith('train'))!.body
    expect(body).toMatchObject({
      dataset: 'orders', target: '', task: 'recommendation', algorithm: 'auto',
      recommend: { user: 'customer', item: 'product', strength: 'qty', time: 'ordered_at' },
    })
    expect(body.features).toBeUndefined()
    expect(await within(dialog).findByText(/For 34 of 100 people/)).toBeInTheDocument()
    expect(within(dialog).getByText(/for 12 of 100/)).toBeInTheDocument()
    expect(within(dialog).getByText(/200 people, 60 items/)).toBeInTheDocument()
  })

  it('trains a recommender again as a recommender', async () => {
    api()
    wrap(<TrainModal vhost="tenant-a" modelName="recs" task="recommendation" onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('radio', { name: /predict a column/i })).not.toBeInTheDocument()
    expect(within(dialog).getByText(/^who/i)).toBeInTheDocument()
    expect(within(dialog).queryByText(/column to predict/i)).not.toBeInTheDocument()
  })

  it('needs a who and a what column before it trains', async () => {
    api()
    wrap(<TrainModal vhost="tenant-a" onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('radio', { name: /recommend items to people/i }))
    await pick(user, dialog, /^dataset/i, 'orders')
    expect(within(dialog).getByRole('button', { name: /^train$/i })).toBeDisabled()
    await pick(user, dialog, /^who/i, 'customer')
    expect(within(dialog).getByRole('button', { name: /^train$/i })).toBeDisabled()
    await pick(user, dialog, /^what they picked/i, 'product')
    expect(within(dialog).getByRole('button', { name: /^train$/i })).toBeEnabled()
  })
})

describe('Testing a recommender', () => {
  it("shows a person's top 10, marking the popular fallback, then items like one item", async () => {
    const calls = api()
    wrap(<ModelTestModal vhost="tenant-a" model={recModel} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('textbox', { name: /^row/i })).not.toBeInTheDocument()

    await user.type(within(dialog).getByRole('textbox', { name: /person/i }), 'c-1')
    await user.click(within(dialog).getByRole('button', { name: /recommend/i }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toEqual({ path: 'predict tenant-a/recs', body: { user: 'c-1', k: 10, exclude_seen: true } })
    expect(await within(dialog).findByText('p-9')).toBeInTheDocument()
    expect(within(dialog).getByText(/popular/i)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('radio', { name: /similar items/i }))
    await user.type(within(dialog).getByRole('textbox', { name: /^item/i }), 'p-2')
    await user.click(within(dialog).getByRole('button', { name: /find similar/i }))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(calls[1].body).toEqual({ item: 'p-2', k: 10 })
  })
})

describe('Monitoring a recommender', () => {
  it('shows coverage and the popular-fallback share instead of drift', async () => {
    server.use(
      http.get('/api/vhosts/:vhost/ml/models/:name/predictions', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts/:vhost/ml/models/:name/drift', () => HttpResponse.json({
        report: null, reason: 'a recommendation model has no features to drift', window: '5m0s', min_rows: 100,
        recommendation: {
          vhost: 'tenant-a', model: 'recs', version: '1', window_start: '2026-10-10T09:55:00Z', window_end: '2026-10-10T10:00:00Z',
          calls: 400, fallbacks: 100, popular_share: 0.25, recommended: 27, catalog: 60, coverage: 0.45,
        },
      })),
    )
    wrap(<MonitoringModal vhost="tenant-a" model={recModel} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('tab', { name: /drift/i })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('tab', { name: /coverage/i }))
    expect(await within(dialog).findByText(/27 of 60 items \(45%\)/)).toBeInTheDocument()
    expect(within(dialog).getByText(/100 of 400 calls \(25%\)/)).toBeInTheDocument()
  })

  it('says when no window has been judged yet', async () => {
    server.use(
      http.get('/api/vhosts/:vhost/ml/models/:name/predictions', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/vhosts/:vhost/ml/models/:name/drift', () => HttpResponse.json({
        report: null, reason: 'a recommendation model has no features to drift', window: '5m0s', min_rows: 100,
      })),
    )
    wrap(<MonitoringModal vhost="tenant-a" model={recModel} onClose={() => {}} />)
    const user = userEvent.setup()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: /coverage/i }))
    expect(await within(dialog).findByText(/at least 100 calls/)).toBeInTheDocument()
  })
})

function Harness({ onConfig }: { onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>({ model: 'recs' })
  return (
    <MLPredictConfig nodeId="n1" config={config} fieldPaths={['customer.id', 'sku']}
      updateNodeConfig={(_id: string, patch: any) => setConfig((c) => {
        const next = { ...c, ...patch }
        onConfig(next)
        return next
      })} />
  )
}

describe('Predict node in Recommend mode', () => {
  it('reads the id from a record field and names where the items go', async () => {
    api()
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    wrap(<Harness onConfig={(c) => { last = c }} />)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('radio', { name: /^recommend/i }))
    expect(last.mode).toBe('recommend')
    expect(screen.queryByText(/^inputs$/i)).not.toBeInTheDocument()

    const idField = screen.queryByRole('combobox', { name: /field with the id/i }) ?? screen.getByRole('textbox', { name: /field with the id/i })
    await user.type(idField, 'customer.id')
    await user.type(screen.getByRole('textbox', { name: /how many/i }), '5')
    await user.type(screen.getByRole('textbox', { name: /output field/i }), 'next')
    expect(last).toMatchObject({ mode: 'recommend', idField: 'customer.id', k: '5', outputField: 'next' })

    await user.click(screen.getByRole('radio', { name: /items like an item/i }))
    expect(last.recommendFor).toBe('item')
    expect(last.excludeSeen).toBeUndefined()
  })

  it('switches a recommender to Recommend mode by itself', async () => {
    api()
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    let last: any = {}
    wrap(<Harness onConfig={(c) => { last = c }} />)
    await waitFor(() => expect(last.mode).toBe('recommend'))
  })
})
