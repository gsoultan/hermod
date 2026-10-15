import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect, useState } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { findEnabledModelAction, openTraining } from '../test/buttons'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { MLTrainConfig } from '@/components/workflow/Transformation/configs/ml/MLTrainConfig'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Guided training: the Train dialog walks someone who knows nothing about
 * machine learning through their data, what to predict, how careful to be,
 * training, and a result in plain words. Every field phase 7 added to the API
 * is optional, so an older server still trains through the same dialog.
 */

const customers = {
  name: 'customers', rows: 1200, updated_at: '2026-10-10T00:00:00Z',
  columns: [
    { name: 'customer_id', type: 'string' }, { name: 'age', type: 'number' },
    { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' },
  ],
}
const sample = [
  { customer_id: 'c-001', age: 30, plan: 'pro', churned: 'no' },
  { customer_id: 'c-002', age: 41, plan: 'basic', churned: 'yes' },
]

const check = {
  task: 'classification', target_kind: 'yes_no', classes: 2, rows: 1200, usable_rows: 1180,
  columns: [
    { name: 'customer_id', type: 'string', role: 'id_like', note: 'Every row has its own value, like an ID.' },
    { name: 'age', type: 'number', role: 'use', note: '' },
    { name: 'plan', type: 'string', role: 'use', note: '' },
    { name: 'churned', type: 'string', role: 'target', note: '' },
  ],
  warnings: [
    { code: 'id_like', message: 'customer_id looks like an ID, so it cannot help predict anything.', column: 'customer_id' },
    { code: 'imbalanced', message: 'Only 1 in 10 rows is "yes".' },
  ],
  ok: true,
}

const classified = {
  model: 'churn', version: '1', task: 'classification', algorithm: 'extra_trees', dataset: 'customers', target: 'churned',
  features: ['age', 'plan'], metrics: { accuracy: 0.87, f1: 0.8, score: 0.87 }, rows: { train: 944, test: 236 },
  created_at: '2026-10-10T00:00:00Z', preset: 'balanced',
  baseline: { accuracy: 0.6, f1: 0.37, score: 0.6 },
  importance: [{ feature: 'plan', weight: 0.7 }, { feature: 'age', weight: 0.3 }],
  leaderboard: [
    { algorithm: 'extra_trees', score: 0.84, metric: 'balanced_accuracy', folds: 3, seconds: 4.2, chosen: true },
    { algorithm: 'linear', score: 0.71, metric: 'balanced_accuracy', folds: 3, seconds: 0.8, chosen: false },
  ],
  warnings: ['Rows with an empty plan were trained on as "missing".'],
}

const regressed = {
  ...classified, task: 'regression', algorithm: 'hist_gradient_boosting', target: 'spend',
  metrics: { rmse: 20.1, mae: 12.4, r2: 0.3, score: 0.3 }, baseline: { rmse: 25, mae: 19.9, r2: 0, score: 0 },
  leaderboard: undefined, importance: undefined, warnings: undefined,
}

/** Only the fields an older server returns for a version. */
const plain = {
  model: 'churn', version: '1', task: 'classification', algorithm: 'random_forest', dataset: 'customers', target: 'churned',
  features: ['age', 'plan'], metrics: { accuracy: 0.9, f1: 0.88, score: 0.9 }, rows: { train: 944, test: 236 },
  created_at: '2026-10-10T00:00:00Z',
}

type Writes = Array<{ method: string; path: string; body?: any }>

function guidedApi({ version = classified as any, trainCheck = check as any, models = [] as any[], live = false } = {}) {
  const writes: Writes = []
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: models.length })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json({ ...customers, sample })),
    http.post('/api/vhosts/:vhost/ml/train-check', async ({ params, request }) => {
      writes.push({ method: 'CHECK', path: `${params.vhost}`, body: await request.json() })
      if (trainCheck === 404) return HttpResponse.json({ error: 'not found' }, { status: 404 })
      return HttpResponse.json(trainCheck)
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      writes.push({ method: 'TRAIN', path: `${params.vhost}/${params.name}`, body: await request.json() })
      return HttpResponse.json({
        model: { name: params.name, backend: 'hermod-ml', url: '', serving: false, features: ['age', 'plan'] },
        version, live, reason: live ? 'trained to go live always' : 'only put live by a person',
      })
    }),
    http.get('/api/vhosts/:vhost/ml/models/:name/versions', () => HttpResponse.json({ data: [plain], total: 1 })),
    http.post('/api/vhosts/:vhost/ml/models/:name/versions/:version/promote', ({ params }) => {
      writes.push({ method: 'PROMOTE', path: `${params.vhost}/${params.name}/${params.version}` })
      return HttpResponse.json({ name: params.name, backend: 'hermod-ml', url: '', serving: false, remote_version: params.version })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/predict', async ({ request }) => {
      writes.push({ method: 'PREDICT', path: '', body: await request.json() })
      return HttpResponse.json({ predictions: [{ label: 'no', probability: 0.8 }] })
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

/**
 * Picks an option of a Mantine Select by its accessible name. Hidden inputs
 * count: jsdom never ends the Advanced section's opening transition, so
 * what it holds stays aria-hidden there although a person sees it.
 */
async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label, hidden: true })
    ?? within(scope).getByRole('textbox', { name: label, hidden: true })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

const next = (user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement) =>
  user.click(within(dialog).getByRole('button', { name: /^next$/i }))

/** Opens the dialog for a new model and walks it to "What to predict" with churned picked. */
async function toTarget(user: ReturnType<typeof userEvent.setup>) {
  await openTraining(user)
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'churn')
  await pick(user, dialog, /^dataset/i, 'customers')
  await next(user, dialog)
  await pick(user, dialog, /column to predict/i, 'churned')
  return dialog
}

/** From "What to predict", through "How careful", to training; returns the dialog on the Result step. */
async function trainFromTarget(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, writes: Writes) {
  await next(user, dialog)
  await next(user, dialog)
  await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
  await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
}

describe('Guided training', () => {
  it('walks from the data to a trained model, one step at a time', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    await openTraining(user)
    const dialog = await screen.findByRole('dialog')

    // 1. Your data: nothing to go on until a dataset is picked, then its first rows.
    expect(within(dialog).getByRole('button', { name: /^next$/i })).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'churn')
    await pick(user, dialog, /^dataset/i, 'customers')
    expect(await within(dialog).findByText('c-002')).toBeInTheDocument()
    expect(within(dialog).getByText(/1,200 rows/)).toBeInTheDocument()
    await next(user, dialog)

    // 2. What to do: predicting a column is the default, next to recommending items.
    expect(within(dialog).getByText('What do you want to do?')).toBeInTheDocument()
    expect(within(dialog).getByRole('radio', { name: /predict a column/i })).toBeChecked()
    expect(within(dialog).getByRole('radio', { name: /recommend items to people/i })).not.toBeChecked()
    // The check runs and says what the target is in words.
    await pick(user, dialog, /column to predict/i, 'churned')
    expect(await within(dialog).findByText(/yes\/no question/i)).toBeInTheDocument()
    await waitFor(() => expect(writes.find((w) => w.method === 'CHECK')?.body).toMatchObject({ dataset: 'customers', target: 'churned' }))
    await next(user, dialog)

    // 3. How careful: Balanced is the default, the expert controls are closed away.
    expect(within(dialog).getByRole('radio', { name: /balanced/i })).toBeChecked()
    expect(within(dialog).queryByLabelText(/^algorithm/i)).not.toBeInTheDocument()
    await next(user, dialog)

    // 4. Train: one button.
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    const train = writes.find((w) => w.method === 'TRAIN')!
    expect(train.path).toBe('tenant-a/churn')
    expect(train.body).toMatchObject({ dataset: 'customers', target: 'churned', algorithm: 'auto', preset: 'balanced', features: ['age', 'plan'] })

    // 5. Result.
    expect(await within(dialog).findByText(/right 87 of 100 times on rows it never saw/i)).toBeInTheDocument()
  })

  it('leaves out the columns the check flags, and puts one back in a click', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)

    expect(await within(dialog).findByText(/customer_id looks like an ID/)).toBeInTheDocument()
    expect(within(dialog).getByText(/1 in 10 rows/)).toBeInTheDocument()
    await waitFor(() => expect(within(dialog).getByRole('checkbox', { name: /customer_id/ })).not.toBeChecked())
    expect(within(dialog).getByRole('checkbox', { name: /^age/ })).toBeChecked()

    await user.click(within(dialog).getByRole('button', { name: /use customer_id anyway/i }))
    expect(within(dialog).getByRole('checkbox', { name: /customer_id/ })).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: /leave out customer_id/i }))
    expect(within(dialog).getByRole('checkbox', { name: /customer_id/ })).not.toBeChecked()

    await trainFromTarget(user, dialog, writes)
    expect(writes.find((w) => w.method === 'TRAIN')!.body.features).toEqual(['age', 'plan'])
  })

  it('stops at "What to predict" when the check says training would be refused', async () => {
    guidedApi({
      trainCheck: {
        ...check, ok: false,
        warnings: [{ code: 'few_rows', message: 'There are only 12 usable rows; training needs at least 20.' }],
      },
    })
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    expect(await within(dialog).findByText(/only 12 usable rows/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /^next$/i })).toBeDisabled()
    await user.click(within(dialog).getByRole('button', { name: /choose another dataset/i }))
    expect(await within(dialog).findByRole('combobox', { name: /^dataset/i })).toBeInTheDocument()
  })

  it('sends the quality the person picked', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await next(user, dialog)
    await user.click(within(dialog).getByRole('radio', { name: /quick/i }))
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    expect(writes.find((w) => w.method === 'TRAIN')!.body).toMatchObject({ algorithm: 'auto', preset: 'quick' })
  })

  it('sends no quality with an algorithm picked under Advanced', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /advanced/i }))
    await within(dialog).findByLabelText(/^algorithm/i)
    await pick(user, dialog, /^algorithm/i, 'Extra trees')
    await next(user, dialog)
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    const body = writes.find((w) => w.method === 'TRAIN')!.body
    expect(body.algorithm).toBe('extra_trees')
    expect(body).not.toHaveProperty('preset')
  })

  it('reads a classifier in plain words: right N of 100, against the baseline', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await trainFromTarget(user, dialog, writes)

    expect(await within(dialog).findByText(/right 87 of 100 times on rows it never saw/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/^good$/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/always guessing the most common answer is right 60 of 100 times/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/what mattered most/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('progressbar', { name: /plan: 70%/i })).toBeInTheDocument()
    const tried = within(dialog).getByRole('table', { name: /what auto tried/i })
    expect(within(tried).getByText('Extra trees')).toBeInTheDocument()
    expect(within(tried).getByText(/kept/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/empty plan were trained on/)).toBeInTheDocument()

    // Raw numbers sit behind Details.
    expect(within(dialog).queryByText('f1')).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: /details/i }))
    expect(await within(dialog).findByText('f1')).toBeInTheDocument()
  })

  it('reads a number model in plain words: typically off by', async () => {
    const writes = guidedApi({ version: regressed })
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await trainFromTarget(user, dialog, writes)
    expect(await within(dialog).findByText(/typically off by 12\.4/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/^fair$/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/always guessing the average is off by 19\.9/i)).toBeInTheDocument()
  })

  it('works against a server without the check or the new result fields', async () => {
    const writes = guidedApi({ version: plain, trainCheck: 404 })
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await waitFor(() => expect(writes.find((w) => w.method === 'CHECK')).toBeTruthy())
    // Every column is learned from, as before the check existed.
    expect(within(dialog).getByRole('checkbox', { name: /customer_id/ })).toBeChecked()
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument()
    await trainFromTarget(user, dialog, writes)
    expect(writes.find((w) => w.method === 'TRAIN')!.body).not.toHaveProperty('features')

    expect(await within(dialog).findByText(/right 90 of 100 times/i)).toBeInTheDocument()
    expect(within(dialog).queryByText(/^(good|fair|weak)$/i)).not.toBeInTheDocument()
    expect(within(dialog).queryByText(/what mattered most/i)).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('table', { name: /what auto tried/i })).not.toBeInTheDocument()
  })

  it('puts the new version live, and tries it with a row from the data', async () => {
    const writes = guidedApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await toTarget(user)
    await trainFromTarget(user, dialog, writes)
    await user.click(await within(dialog).findByRole('button', { name: /make it live/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'PROMOTE')?.path).toBe('tenant-a/churn/1'))
    expect(await within(dialog).findByText(/^live$/i)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: /try it/i }))
    const test = await screen.findByRole('dialog', { name: /test churn/i })
    expect((within(test).getByRole('textbox', { name: /row/i }) as HTMLTextAreaElement).value).toContain('"plan": "pro"')
  })

  it('trains an existing model again from its last version', async () => {
    const writes = guidedApi({ models: [{ name: 'churn', backend: 'hermod-ml', url: '', remote_version: '1', serving: false }] })
    renderPage()
    const user = userEvent.setup()
    await user.click(await findEnabledModelAction(user, 'churn', 'Train again'))
    const dialog = await screen.findByRole('dialog')
    // It opens on "What to predict", with the last version's choices.
    await waitFor(() => expect(within(dialog).getByRole('combobox', { name: /column to predict/i })).toHaveValue('churned'))
    await trainFromTarget(user, dialog, writes)
    expect(writes.find((w) => w.method === 'TRAIN')!).toMatchObject({ path: 'tenant-a/churn', body: { dataset: 'customers', target: 'churned' } })
  })
})

describe('Models page for a first-time user', () => {
  // The steps moved from the empty model list into Getting started, at the
  // top of the page, where they also show which are done.
  it('walks through adding a dataset, training and trying', async () => {
    guidedApi()
    renderPage()
    const guide = await screen.findByRole('region', { name: /getting started/i })
    expect(within(guide).getByRole('listitem', { name: /get your data/i })).toBeInTheDocument()
    expect(within(guide).getByRole('listitem', { name: /use it/i })).toBeInTheDocument()
    expect(await within(guide).findByRole('button', { name: /start training/i })).toBeInTheDocument()
  })
})

function NodeHarness({ onConfig }: { onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>({})
  return (
    <MLTrainConfig nodeId="n1" config={config} sources={[]}
      updateNodeConfig={(_id: string, patch: any) => {
        setConfig((c) => {
          const nextConfig = { ...c, ...patch }
          onConfig(nextConfig)
          return nextConfig
        })
      }} />
  )
}

describe('Train Model node quality', () => {
  it('saves the quality picked, with the expert fields closed away', async () => {
    guidedApi()
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
    expect(screen.getByRole('radio', { name: /balanced/i })).toBeChecked()
    expect(screen.queryByLabelText(/^algorithm/i)).not.toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: /best quality/i }))
    await waitFor(() => expect(last).toMatchObject({ preset: 'best' }))
    await user.click(screen.getByRole('button', { name: /advanced/i }))
    expect((await screen.findAllByLabelText(/^algorithm/i)).length).toBeGreaterThan(0)
  })
})
