import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { activityTabs } from '@/components/layout/nav'
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { modelAction } from '../test/buttons'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to: _to, params: _params, ...props }: any) => <a {...props}>{children}</a>,
  useNavigate: () => () => {},
  useRouterState: () => ({ location: { pathname: '/' }, isLoading: false }),
}))

/**
 * Getting started on the Machine Learning page. People could not find the
 * page (the sidebar said "Models"), did not know training needs the hermod-ml
 * worker, and saw Train a model, Datasets and the neural networks vanish when
 * the worker was missing. The page now walks them through five steps, says
 * exactly what is missing, and never hides an action it can explain instead.
 */

const off = { configured: false, ready: false }
const down = { configured: true, ready: false, error: 'the ML worker does not answer' }
const slim = { configured: true, ready: true, algorithms: ['auto', 'random_forest', 'gradient_boosting', 'linear', 'xgboost'] }
const withDL = { ...slim, algorithms: [...slim.algorithms, 'pytorch_mlp', 'keras_mlp'] }
/** Hermod's built-in engine, in force when no worker is configured (pkg/ml/builtin). */
const builtin = {
  configured: true, ready: true, source: 'builtin',
  algorithms: ['auto', 'random_forest', 'gradient_boosting', 'linear', 'mlp', 'als', 'item_similarity'],
  unavailable: Object.fromEntries(['xgboost', 'extra_trees', 'hist_gradient_boosting', 'pytorch_mlp', 'keras_mlp']
    .map((a) => [a, 'needs the hermod-ml worker'])),
}
/** The built-in engine and a connected worker at once (internal/ml/transport/http WorkerStatus). */
const both = {
  configured: true, ready: true, source: 'saved',
  algorithms: [...builtin.algorithms, 'xgboost', 'extra_trees', 'hist_gradient_boosting'],
  unavailable: { pytorch_mlp: 'the torch package is not installed', keras_mlp: 'TensorFlow is not installed' },
  engines: {
    builtin: { available: true, algorithms: builtin.algorithms },
    worker: { configured: true, ready: true, source: 'saved', algorithms: ['auto', 'linear', 'xgboost'] },
  },
}
/** The same, with the worker down: the built-in engine trains on, and lists what needs the worker. */
const bothWorkerDown = {
  ...both,
  algorithms: builtin.algorithms, unavailable: builtin.unavailable,
  engines: { ...both.engines, worker: { configured: true, ready: false, source: 'saved', error: 'the ML worker does not answer' } },
}

const customers = {
  name: 'customers', rows: 1200, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' }],
}
const trained = { name: 'churn', backend: 'hermod-ml', url: '', serving: false, task: 'classification' }
const live = { ...trained, remote_version: '1' }

const noQuotas = {
  max_datasets: null, max_dataset_rows: null, max_dataset_bytes: null,
  max_models: null, max_concurrent_trainings: null, max_predictions_per_second: null,
}

/** The API one page load needs; `worker` may be a function so a test can change it. */
function pageApi({ worker = off as any, datasets = [] as any[], models = [] as any[] } = {}) {
  const calls = { worker: 0, datasets: 0 }
  server.use(
    http.get('/api/ml/worker', () => {
      calls.worker++
      return HttpResponse.json(typeof worker === 'function' ? worker() : worker)
    }),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: models.length })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => {
      calls.datasets++
      return HttpResponse.json({ data: datasets, total: datasets.length })
    }),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json({ ...customers, sample: [] })),
    http.get('/api/vhosts/:vhost/ml/jobs', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/quotas', () => HttpResponse.json({ quotas: { vhost: 'tenant-a', ...noQuotas }, defaults: noQuotas, effective: noQuotas })),
    http.get('/api/vhosts/:vhost/ml/models/:name/versions', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name/profile', ({ params }) => HttpResponse.json(profileOf(String(params.name)))),
  )
  return calls
}

/** What the server says of a customers table (pkg/ml/profile). */
const profileOf = (dataset: string) => ({
  dataset, rows: 1200, column_count: 3, sample_rows: 1200, sampled: false, max_sample_rows: 50000, duplicate_rows: 0,
  columns: [
    { name: 'age', type: 'number', kind: 'number', missing: 0, missing_share: 0, distinct: 60 },
    { name: 'plan', type: 'string', kind: 'category', missing: 0, missing_share: 0, distinct: 3 },
    { name: 'churned', type: 'string', kind: 'yes_no', missing: 0, missing_share: 0, distinct: 2 },
  ],
  warnings: [], correlations: [], computed_at: '2026-10-15T00:00:00Z',
})

function SelectVHost({ vhost }: { vhost: string }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts([vhost])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(username = 'tester', role = 'Editor') {
  signInAs(role, username)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
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

const guide = () => screen.findByRole('region', { name: /getting started/i })
const step = async (name: RegExp) => within(await guide()).findByRole('listitem', { name })

/** The "Train a new model" choice behind Add model. */
async function trainChoice(user: ReturnType<typeof userEvent.setup>) {
  await user.click((await screen.findAllByRole('button', { name: /^add model$/i }))[0])
  const dialog = await screen.findByRole('dialog', { name: /add a model/i })
  return within(dialog).getByRole('button', { name: /train a new model/i })
}

afterEach(() => localStorage.clear())

describe('Machine Learning in the sidebar', () => {
  // Machine learning is a tab of Activity now, not a sidebar link, and the
  // navigation is data: this is where its name lives.
  it('is called Machine learning, not Models, and only for those who may train', () => {
    const labels = (role: string) => activityTabs(role).map((t) => t.label)
    expect(labels('Editor')).toContain('Machine learning')
    expect(labels('Administrator')).toContain('Machine learning')
    expect(labels('Viewer')).not.toContain('Machine learning')
    expect(labels('Editor')).not.toContain('Models')
  })

  it('titles the tab Machine learning', async () => {
    pageApi()
    renderPage()
    expect(await screen.findByRole('heading', { level: 2, name: 'Machine learning' })).toBeInTheDocument()
  })
})

describe('Getting started: the training worker', () => {
  it('says the worker is not set up, and keeps the actions visible but disabled with the reason', async () => {
    const calls = pageApi({ worker: off })
    renderPage()
    const user = userEvent.setup()
    const worker = await step(/connect the training worker/i)
    expect(await within(worker).findByText(/not set up yet/i)).toBeInTheDocument()
    expect(within(worker).getByText('To do')).toBeInTheDocument()
    expect(within(worker).getByRole('button', { name: /set up the worker/i })).toBeEnabled()

    const train = await trainChoice(user)
    expect(train).toBeDisabled()
    expect(train).toHaveAccessibleDescription(/hermod-ml worker, which is not set up yet/i)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    // Datasets are on their tab even without a worker, with their buttons off.
    await user.click(screen.getByRole('tab', { name: 'Data' }))
    expect(await screen.findByRole('heading', { name: 'Datasets' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /upload a file/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /from a database/i })).toBeDisabled()
    expect(calls.datasets).toBe(0)

    // Steps that need the worker say so instead of offering a dead button.
    const data = await step(/get your data/i)
    expect(within(data).getByRole('button', { name: /go to datasets/i })).toBeDisabled()
    const model = await step(/train a model/i)
    expect(within(model).getByRole('button', { name: /start training/i })).toBeDisabled()
  })

  it('says the worker is set up but not answering, with its own troubleshooting', async () => {
    pageApi({ worker: down })
    renderPage()
    const user = userEvent.setup()
    const worker = await step(/connect the training worker/i)
    expect(await within(worker).findByText(/set up, but it does not answer/i)).toBeInTheDocument()
    expect(await trainChoice(user)).toHaveAccessibleDescription(/set up but not answering/i)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await user.click(within(worker).getByRole('button', { name: /fix the connection/i }))
    const dialog = await screen.findByRole('dialog', { name: /set up the training worker/i })
    expect(within(dialog).getByText(/hermod has a worker address but the worker does not answer/i)).toBeInTheDocument()
    // An Editor cannot connect the worker, and is told who can.
    expect(within(dialog).getByText(/an administrator of this hermod opens machine/i)).toBeInTheDocument()
  })

  it('says the worker is connected without deep learning', async () => {
    pageApi({ worker: slim })
    renderPage()
    const user = userEvent.setup()
    const worker = await step(/connect the training worker/i)
    expect(await within(worker).findByText(/^connected/i)).toBeInTheDocument()
    expect(within(worker).getByText('Done')).toBeInTheDocument()
    expect(within(worker).getByText(/deep learning \(pytorch, tensorflow\) is not available/i)).toBeInTheDocument()
    expect(within(worker).getByRole('button', { name: /how to add deep learning/i })).toBeEnabled()
    expect(await trainChoice(user)).toBeEnabled()
  })

  it('says the worker is connected with deep learning', async () => {
    pageApi({ worker: withDL })
    renderPage()
    const worker = await step(/connect the training worker/i)
    expect(await within(worker).findByText(/deep learning \(pytorch, tensorflow\) is available/i)).toBeInTheDocument()
    expect(within(worker).queryByRole('button', { name: /how to add deep learning/i })).not.toBeInTheDocument()
  })
})

describe('Getting started: the setup guide', () => {
  it('explains hermod-ml, gives Docker, Compose and Helm commands, and checks again', async () => {
    let status: any = off
    const calls = pageApi({ worker: () => status })
    server.use(
      http.get('/api/ml/worker/connection', () => HttpResponse.json({ source: 'none', url: '', token_set: false, editable: true })),
      http.get('/api/version', () => HttpResponse.json({ version: 'dev (Enterprise Edition)' })),
    )
    renderPage('tester', 'Administrator')
    const user = userEvent.setup()
    const worker = await step(/connect the training worker/i)
    await user.click(await within(worker).findByRole('button', { name: /set up the worker/i }))
    const dialog = await screen.findByRole('dialog', { name: /set up the training worker/i })

    expect(within(dialog).getByText(/what is hermod-ml\?/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/a separate helper program that trains models/i)).toBeInTheDocument()

    // Docker is the first tab: one command to run the worker.
    expect(await within(dialog).findByText(/docker run -d --name hermod-ml --restart unless-stopped -p 8090:8090 -e HERMOD_ML_TOKEN=change-me/)).toBeInTheDocument()
    expect(within(dialog).getByText(/ghcr\.io\/gsoultan\/hermod-ml:latest$/m)).toBeInTheDocument()
    expect(within(dialog).getAllByRole('button', { name: /^copy/i }).length).toBeGreaterThanOrEqual(1)

    await user.click(within(dialog).getByRole('tab', { name: /helm/i }))
    expect(await within(dialog).findByText(/--set mlWorker\.enabled=true --set mlWorker\.token=change-me/)).toBeInTheDocument()

    // Once the worker is running, Check again asks Hermod and shows the answer.
    status = withDL
    const before = calls.worker
    await user.click(within(dialog).getByRole('button', { name: /check again/i }))
    await waitFor(() => expect(calls.worker).toBeGreaterThan(before))
    expect(await within(dialog).findByText(/the worker is connected/i)).toBeInTheDocument()
    expect(await within(worker).findByText('Done')).toBeInTheDocument()
  })
})

describe('Getting started: the built-in engine', () => {
  it('makes connecting the worker optional, and lets training start without it', async () => {
    pageApi({ worker: builtin, datasets: [customers] })
    renderPage()
    const user = userEvent.setup()
    const worker = await step(/optional: connect the hermod-ml worker for tensorflow\/pytorch/i)
    expect(within(worker).getByText('Optional')).toBeInTheDocument()
    expect(within(worker).getByText(/built-in engine/i)).toBeInTheDocument()
    expect(within(worker).getByRole('button', { name: /set up the worker/i })).toBeEnabled()
    const data = await step(/get your data/i)
    expect(within(data).getByRole('button', { name: /go to datasets/i })).toBeEnabled()
    const model = await step(/train a model/i)
    expect(within(model).getByRole('button', { name: /start training/i })).toBeEnabled()
    expect(await trainChoice(user)).toBeEnabled()
  })

  it('can be hidden once the other three steps are done', async () => {
    pageApi({ worker: builtin, datasets: [customers], models: [live] })
    renderPage()
    const use = await step(/use it/i)
    expect(await within(use).findByText('Done')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /hide the guide/i })).toBeInTheDocument()
  })
})

describe('Getting started: the built-in engine beside a worker', () => {
  it('marks the optional worker step done, and says both engines train', async () => {
    pageApi({ worker: both, datasets: [customers] })
    renderPage()
    const worker = await step(/optional: connect the hermod-ml worker for tensorflow\/pytorch/i)
    expect(await within(worker).findByText('Done')).toBeInTheDocument()
    expect(within(worker).getByText(/both engines/i)).toBeInTheDocument()
    const model = await step(/train a model/i)
    expect(within(model).getByRole('button', { name: /start training/i })).toBeEnabled()
  })

  it('says a worker that is down is down, while the built-in engine keeps training', async () => {
    pageApi({ worker: bothWorkerDown, datasets: [customers] })
    renderPage()
    const user = userEvent.setup()
    const worker = await step(/optional: connect the hermod-ml worker for tensorflow\/pytorch/i)
    expect(await within(worker).findByText(/does not answer/i)).toBeInTheDocument()
    expect(within(worker).getByText(/built-in engine keeps training/i)).toBeInTheDocument()
    expect(within(worker).getByRole('button', { name: /fix the connection/i })).toBeEnabled()
    const model = await step(/train a model/i)
    expect(within(model).getByRole('button', { name: /start training/i })).toBeEnabled()
    expect(await trainChoice(user)).toBeEnabled()
  })
})

describe('Getting started: data, training and use', () => {
  it('marks Get your data done once the vhost has a dataset, and goes to Datasets', async () => {
    pageApi({ worker: slim, datasets: [customers] })
    renderPage()
    const user = userEvent.setup()
    const data = await step(/get your data/i)
    expect(await within(data).findByText('Done')).toBeInTheDocument()
    await user.click(within(data).getByRole('button', { name: /go to datasets/i }))
    expect(screen.getByRole('heading', { name: 'Datasets' })).toHaveFocus()
  })

  it('keeps Get your data to do while there is no dataset, and training waits for one', async () => {
    pageApi({ worker: slim })
    renderPage()
    const data = await step(/get your data/i)
    expect(await within(data).findByText('To do')).toBeInTheDocument()
    const model = await step(/train a model/i)
    expect(within(model).getByRole('button', { name: /start training/i })).toBeDisabled()
    expect(within(model).getByText(/add a dataset first/i)).toBeInTheDocument()
  })

  it('keeps Check your data to do until a dataset is analyzed, and analyzes the newest one', async () => {
    const orders = { ...customers, name: 'orders', updated_at: '2026-10-12T00:00:00Z' }
    pageApi({ worker: slim, datasets: [customers, orders] })
    renderPage()
    const user = userEvent.setup()
    const check = await step(/check your data/i)
    expect(within(check).getByText('To do')).toBeInTheDocument()
    await user.click(await within(check).findByRole('button', { name: /analyze orders/i }))
    const dialog = await screen.findByRole('dialog', { name: /check orders/i })
    expect(await within(dialog).findByText(/1,200 rows and 3 columns/)).toBeInTheDocument()
    expect(within(check).getByText('Done')).toBeInTheDocument()
  })

  it('starts training from the analysis with the dataset and column it suggested', async () => {
    pageApi({ worker: slim, datasets: [customers] })
    renderPage()
    const user = userEvent.setup()
    await user.click(await within(await step(/check your data/i)).findByRole('button', { name: /analyze customers/i }))
    const dialog = await screen.findByRole('dialog', { name: /check customers/i })
    await user.click(await within(dialog).findByRole('button', { name: /train: .*\(churned\)/i }))
    const train = await screen.findByRole('dialog', { name: /train a model/i })
    expect(within(train).getByRole('combobox', { name: /^dataset/i })).toHaveValue('customers')
  })

  it('opens the Train dialog from the guide once there is data', async () => {
    pageApi({ worker: slim, datasets: [customers] })
    renderPage()
    const user = userEvent.setup()
    const model = await step(/train a model/i)
    await waitFor(() => expect(within(model).getByRole('button', { name: /start training/i })).toBeEnabled())
    expect(within(model).getByText('To do')).toBeInTheDocument()
    await user.click(within(model).getByRole('button', { name: /start training/i }))
    expect(await screen.findByRole('dialog', { name: /train a model/i })).toBeInTheDocument()
  })

  it('marks Train a model done once a model is trained, and offers to make it live', async () => {
    pageApi({ worker: slim, datasets: [customers], models: [trained] })
    renderPage()
    const user = userEvent.setup()
    const model = await step(/train a model/i)
    expect(await within(model).findByText('Done')).toBeInTheDocument()
    const use = await step(/use it/i)
    expect(within(use).getByText('To do')).toBeInTheDocument()
    await user.click(within(use).getByRole('button', { name: /make it live/i }))
    expect(await screen.findByRole('dialog', { name: /versions of churn/i })).toBeInTheDocument()
  })

  it('marks Use it done once a model is live, and offers to try it', async () => {
    pageApi({ worker: slim, datasets: [customers], models: [live] })
    renderPage()
    const user = userEvent.setup()
    const use = await step(/use it/i)
    expect(await within(use).findByText('Done')).toBeInTheDocument()
    await user.click(within(use).getByRole('button', { name: /try your model/i }))
    expect(await screen.findByRole('dialog', { name: /test churn/i })).toBeInTheDocument()
  })

  it('keeps Use it waiting until a model is trained', async () => {
    pageApi({ worker: slim, datasets: [customers] })
    renderPage()
    const use = await step(/use it/i)
    expect(within(use).getByRole('button', { name: /try your model/i })).toBeDisabled()
    expect(within(use).getByText(/train a model first/i)).toBeInTheDocument()
  })

  it('disables Train again and Retrain on a model while the worker is missing, saying why', async () => {
    pageApi({ worker: off, models: [live] })
    renderPage()
    const user = userEvent.setup()
    const again = await modelAction(user, 'churn', 'Train again')
    await waitFor(() => expect(again).toHaveAccessibleDescription(/not set up yet/i))
    expect(again).toBeDisabled()
    const retrain = screen.getByRole('menuitem', { name: 'Retrain automatically', hidden: true })
    expect(retrain).toBeDisabled()
    expect(retrain).toHaveAccessibleDescription(/not set up yet/i)
  })
})

describe('Getting started: hiding the guide', () => {
  it('can be hidden only once every step is done, and stays hidden for that person', async () => {
    pageApi({ worker: withDL, datasets: [customers], models: [live] })
    const first = renderPage('ana')
    const user = userEvent.setup()
    const use = await step(/use it/i)
    expect(await within(use).findByText('Done')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /hide the guide/i }))
    await waitFor(() => expect(screen.queryByRole('listitem', { name: /use it/i })).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: /show the guide/i })).toBeInTheDocument()
    first.unmount()

    renderPage('ana')
    expect(await screen.findByRole('button', { name: /show the guide/i })).toBeInTheDocument()
    expect(screen.queryByRole('listitem', { name: /use it/i })).not.toBeInTheDocument()
  })

  it('cannot be hidden while a step is left', async () => {
    pageApi({ worker: slim, datasets: [customers] })
    renderPage()
    await step(/get your data/i)
    expect(screen.queryByRole('button', { name: /hide the guide/i })).not.toBeInTheDocument()
  })
})
