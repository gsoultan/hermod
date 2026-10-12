import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { openTraining } from '../test/buttons'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { servingCurl, servingCurlAsync } from '@/pages/ml/nextSteps'
import { listJobs, startTraining } from '@/lib/mlModels'

/**
 * Asynchronous jobs: the Train step sends the training as a job and follows
 * it with a progress bar, the Models page lists the vhost's jobs, and the
 * "Copy API call" snippets come sync and async.
 */

const customers = {
  name: 'customers', rows: 120, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' }],
}

const trained = {
  model: { name: 'churn2', backend: 'hermod-ml', remote_version: '1' },
  version: {
    model: 'churn2', version: '3', task: 'classification', algorithm: 'random_forest', dataset: 'customers',
    target: 'churned', features: ['age', 'plan'], metrics: { accuracy: 0.9, score: 0.9 }, rows: { train: 96, test: 24 },
    created_at: '2026-10-10T00:00:00Z',
  },
  live: false, reason: 'only put live by a person',
}

const job = (over: Record<string, unknown>) => ({
  id: 'job_1', vhost: 'tenant-a', model: 'churn2', kind: 'train', status: 'queued', progress: 0,
  created_at: '2026-10-10T00:00:00Z', expires_at: '2026-10-17T00:00:00Z', status_url: '/api/vhosts/tenant-a/ml/jobs/job_1',
  ...over,
})

const listed = [
  job({ id: 'job_a', status: 'running', progress: 0.5, progress_message: '2,500 of 5,000 rows', kind: 'predict', model: 'double' }),
  job({ id: 'job_b', status: 'failed', error: 'the model server did not answer in 30s', kind: 'train' }),
  job({ id: 'job_c', status: 'succeeded', progress: 1, kind: 'recommend', model: 'recs', finished_at: '2026-10-10T00:01:00Z' }),
]

function jobsApi({ jobs = [] as any[], states = [] as any[] } = {}) {
  const calls: Array<{ method: string; path: string; body?: any; prefer?: string | null }> = []
  let read = 0
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json({ ...customers, sample: [{ age: 30, plan: 'pro', churned: 'no' }] })),
    http.get('/api/vhosts/:vhost/ml/models/:name/versions', () => HttpResponse.json({ data: [trained.version], total: 1 })),
    http.post('/api/vhosts/:vhost/ml/train-check', () => HttpResponse.json({ error: 'not found' }, { status: 404 })),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      calls.push({ method: 'TRAIN', path: `${params.vhost}/${params.name}`, body: await request.json(), prefer: request.headers.get('Prefer') })
      return HttpResponse.json({ job_id: 'job_1', status: 'queued', status_url: '/api/vhosts/tenant-a/ml/jobs/job_1' }, { status: 202 })
    }),
    http.get('/api/vhosts/:vhost/ml/jobs/:id', ({ params }) => {
      calls.push({ method: 'GET', path: `${params.vhost}/${params.id}` })
      const s = states[Math.min(read++, states.length - 1)]
      return HttpResponse.json(s)
    }),
    http.get('/api/vhosts/:vhost/ml/jobs', ({ request }) => {
      const url = new URL(request.url)
      calls.push({ method: 'LIST', path: url.search })
      const status = url.searchParams.get('status')
      const data = jobs.filter((j) => !status || j.status === status)
      return HttpResponse.json({ data, total: data.length })
    }),
    http.post('/api/vhosts/:vhost/ml/jobs/:id/cancel', ({ params }) => {
      calls.push({ method: 'CANCEL', path: `${params.vhost}/${params.id}` })
      return HttpResponse.json(job({ id: params.id, status: 'cancelled' }))
    }),
  )
  return calls
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

async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label, hidden: true })
    ?? within(scope).getByRole('textbox', { name: label, hidden: true })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

/** Walks the Train dialog for model churn2 to its Train button and clicks it. */
async function train(user: ReturnType<typeof userEvent.setup>) {
  await openTraining(user)
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'churn2')
  await pick(user, dialog, /^dataset/i, 'customers')
  await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
  await pick(user, dialog, /column to predict/i, 'churned')
  await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
  await user.click(within(dialog).getByRole('button', { name: /^next$/i }))
  await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
  return dialog
}

describe('Training as a job', () => {
  it('sends the training async, shows its progress, then the result', async () => {
    const calls = jobsApi({
      states: [
        job({ status: 'running', progress: 0.4, progress_message: 'Training on the ML worker' }),
        job({ status: 'running', progress: 0.4, progress_message: 'Training on the ML worker' }),
        job({ status: 'succeeded', progress: 1, result: trained, finished_at: '2026-10-10T00:05:00Z' }),
      ],
    })
    renderPage()
    const user = userEvent.setup()
    const dialog = await train(user)

    await waitFor(() => expect(calls.find((c) => c.method === 'TRAIN')).toBeTruthy())
    const sent = calls.find((c) => c.method === 'TRAIN')!
    expect(sent.body).toMatchObject({ dataset: 'customers', target: 'churned', respond: 'async' })
    expect(sent.prefer).toBe('respond-async')

    expect(await within(dialog).findByText(/training on the ml worker \(40%\)/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/you can close this window/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('progressbar', { name: /training progress/i })).toBeInTheDocument()

    // The job is read again until it ends, and its result is the step after.
    expect(await within(dialog).findByText(/only put live by a person/i, {}, { timeout: 5000 })).toBeInTheDocument()
    expect(within(dialog).getByText(/version 3/i)).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'GET').length).toBeGreaterThanOrEqual(3)
    expect(within(dialog).queryByRole('progressbar', { name: /training progress/i })).not.toBeInTheDocument()
  }, 20000)

  it('says in words why a training job failed', async () => {
    jobsApi({ states: [job({ status: 'failed', progress: 0.05, error: 'dataset "customers" has no column "churned"' })] })
    renderPage()
    const user = userEvent.setup()
    const dialog = await train(user)
    expect(await within(dialog).findByText(/the model was not trained/i)).toBeInTheDocument()
    expect(within(dialog).getByText('dataset "customers" has no column "churned"')).toBeInTheDocument()
  }, 20000)

  it('stops a running training', async () => {
    const calls = jobsApi({ states: [job({ status: 'running', progress: 0.2, progress_message: 'Training on the ML worker' })] })
    renderPage()
    const user = userEvent.setup()
    const dialog = await train(user)
    await user.click(await within(dialog).findByRole('button', { name: /stop training/i }))
    await waitFor(() => expect(calls.find((c) => c.method === 'CANCEL')).toMatchObject({ path: 'tenant-a/job_1' }))
  }, 20000)
})

describe('Jobs list on the Models page', () => {
  it('lists jobs with status, progress and why one failed, and filters by status', async () => {
    const calls = jobsApi({ jobs: listed })
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: 'Training jobs' }))
    const table = await screen.findByRole('table', { name: /jobs/i })
    expect(within(table).getByText('2,500 of 5,000 rows')).toBeInTheDocument()
    expect(within(table).getByText('the model server did not answer in 30s')).toBeInTheDocument()
    expect(within(table).getByText('Prediction')).toBeInTheDocument()
    expect(within(table).getByText('Recommendation')).toBeInTheDocument()
    expect(within(table).getByText('Failed')).toBeInTheDocument()
    expect(within(table).getByRole('progressbar', { name: /progress of double/i })).toBeInTheDocument()

    await pick(user, document.body, /^status/i, 'Failed')
    await waitFor(() => expect(calls.some((c) => c.method === 'LIST' && c.path.includes('status=failed'))).toBe(true))
    await waitFor(() => expect(screen.queryByText('2,500 of 5,000 rows')).not.toBeInTheDocument())
  })

  it('lets an Editor stop an unfinished job, and a Viewer only look', async () => {
    const calls = jobsApi({ jobs: listed })
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: 'Training jobs' }))
    await screen.findByRole('table', { name: /jobs/i })
    expect(screen.queryByRole('button', { name: /stop job job_b/i })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /stop job job_a/i }))
    await waitFor(() => expect(calls.find((c) => c.method === 'CANCEL')).toMatchObject({ path: 'tenant-a/job_a' }))
  })

  it('shows no Stop button to a Viewer', async () => {
    jobsApi({ jobs: listed })
    renderPage('Viewer')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: 'Training jobs' }))
    await screen.findByRole('table', { name: /jobs/i })
    expect(screen.queryByRole('button', { name: /stop job/i })).not.toBeInTheDocument()
  })
})

describe('API helpers', () => {
  it('gives the sync and the async call of a model', () => {
    const sync = servingCurl('tenant-a', 'churn', { instances: [{ age: 30 }] })
    const async = servingCurlAsync('tenant-a', 'churn', { instances: [{ age: 30 }] })
    expect(sync).toContain(`-d '{"instances":[{"age":30}]}'`)
    expect(sync).not.toContain('respond')
    expect(async).toContain(`-d '{"instances":[{"age":30}],"respond":"async"}'`)
    expect(async).toContain('/api/ml/serve/tenant-a/jobs/<job_id>')
  })

  it('takes the result at once from a server that trains while the request waits', async () => {
    jobsApi()
    server.use(http.post('/api/vhosts/:vhost/ml/models/:name/train', () => HttpResponse.json(trained)))
    const got = await startTraining('tenant-a', 'churn2', { dataset: 'customers', target: 'churned', go_live: { mode: 'never' } })
    expect(got.job).toBeUndefined()
    expect(got.result?.version.version).toBe('3')
  })

  it('lists jobs with the filters it is given', async () => {
    const calls = jobsApi({ jobs: listed })
    const got = await listJobs('tenant-a', { model: 'double', status: 'running', limit: 20, offset: 40 })
    expect(calls.at(-1)?.path).toBe('?model=double&status=running&limit=20&offset=40')
    expect(got.total).toBe(1)
  })
})
