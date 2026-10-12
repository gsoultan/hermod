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
import { ModelsPage } from '@/pages/ml/ModelsPage'
import { workerImageTag } from '@/lib/mlModels'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to: _to, params: _params, ...props }: any) => <a {...props}>{children}</a>,
  useNavigate: () => () => {},
  useRouterState: () => ({ location: { pathname: '/' }, isLoading: false }),
}))

/**
 * Connecting the hermod-ml worker from the console. It used to take two
 * environment variables on Hermod, a token that had to match the worker's,
 * and a restart; people got one of the three wrong and could not tell which.
 * The setup guide is now three steps: run the worker (one command, with a
 * token generated for you), paste its address and token, test and save. A
 * save takes effect at once.
 */

const off = { configured: false, ready: false, source: 'none' }
const ready = { configured: true, ready: true, source: 'saved', algorithms: ['auto', 'random_forest'] }

const noQuotas = {
  max_datasets: null, max_dataset_rows: null, max_dataset_bytes: null,
  max_models: null, max_concurrent_trainings: null, max_predictions_per_second: null,
}

interface Api {
  worker?: () => unknown
  connection?: () => unknown
  probe?: (body: any) => unknown
}

function pageApi({ worker = () => off, connection = () => ({ source: 'none', url: '', token_set: false, editable: true }), probe }: Api = {}) {
  const calls = { worker: 0, connection: 0, tests: [] as any[], saves: [] as any[] }
  server.use(
    http.get('/api/version', () => HttpResponse.json({ version: 'v1.18.0 (Enterprise Edition)' })),
    http.get('/api/ml/worker', () => {
      calls.worker++
      return HttpResponse.json(worker() as any)
    }),
    http.get('/api/ml/worker/connection', () => {
      calls.connection++
      return HttpResponse.json(connection() as any)
    }),
    http.post('/api/ml/worker/connection/test', async ({ request }) => {
      const body = await request.json()
      calls.tests.push(body)
      return HttpResponse.json((probe ?? (() => ({ result: 'ok', message: 'Connected.', deep_learning: false })))(body) as any)
    }),
    http.put('/api/ml/worker/connection', async ({ request }) => {
      const body = (await request.json()) as any
      calls.saves.push(body)
      return HttpResponse.json({ source: 'saved', url: body.url, token_set: true, editable: true })
    }),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/jobs', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/quotas', () => HttpResponse.json({ quotas: { vhost: 'tenant-a', ...noQuotas }, defaults: noQuotas, effective: noQuotas })),
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

function renderPage(role = 'Administrator') {
  signInAs(role)
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

const workerStep = async () => {
  const guide = await screen.findByRole('region', { name: /getting started/i })
  return within(guide).findByRole('listitem', { name: /connect the training worker/i })
}

async function openSetup(user: ReturnType<typeof userEvent.setup>) {
  const step = await workerStep()
  await user.click(await within(step).findByRole('button', { name: /set up the worker|fix the connection/i }))
  return screen.findByRole('dialog', { name: /set up the training worker/i })
}

afterEach(() => localStorage.clear())

describe('Worker setup: three steps', () => {
  it('numbers the steps, and one generated token fills the command and the form', async () => {
    pageApi()
    renderPage()
    const user = userEvent.setup()
    const dialog = await openSetup(user)

    expect(within(dialog).getByRole('heading', { name: /1\. run the worker/i })).toBeInTheDocument()
    expect(within(dialog).getByRole('heading', { name: /2\. paste the address and token/i })).toBeInTheDocument()
    expect(within(dialog).getByRole('heading', { name: /3\. test and save/i })).toBeInTheDocument()

    // The image matches Hermod's release, and keeps running after a reboot.
    const command = await within(dialog).findByText(/docker run -d --name hermod-ml --restart unless-stopped -p 8090:8090/)
    await waitFor(() => expect(command.textContent).toMatch(/ghcr\.io\/gsoultan\/hermod-ml:1\.18\.0$/m))

    await user.click(within(dialog).getByRole('button', { name: /generate token/i }))
    const token = (within(dialog).getByLabelText(/^token/i) as HTMLInputElement).value
    expect(token).toMatch(/^[0-9a-f]{64}$/)
    expect(command.textContent).toContain(`HERMOD_ML_TOKEN=${token}`)

    // Deep learning picks the -dl image.
    await user.click(within(dialog).getByRole('switch', { name: /deep learning/i }))
    expect(command.textContent).toMatch(/hermod-ml:1\.18\.0-dl$/m)

    // Docker Compose and Helm carry the same token.
    await user.click(within(dialog).getByRole('tab', { name: /docker compose/i }))
    expect((await within(dialog).findAllByText(new RegExp(`HERMOD_ML_TOKEN=${token}`))).length).toBeGreaterThan(0)
    await user.click(within(dialog).getByRole('tab', { name: /helm/i }))
    expect(await within(dialog).findByText(new RegExp(`--set mlWorker\\.token=${token}`))).toBeInTheDocument()
    expect(within(dialog).getByText(/mlWorker\.image\.variant=dl/)).toBeInTheDocument()

    // Inside Docker, localhost is Hermod's own container.
    expect(within(dialog).getByText(/localhost.*means the hermod container/i)).toBeInTheDocument()
  })

  it('says in plain words what is wrong with each failed test', async () => {
    let answer: any = { result: 'unauthorized', message: 'The worker refused the token.' }
    const calls = pageApi({ probe: () => answer })
    renderPage()
    const user = userEvent.setup()
    const dialog = await openSetup(user)

    await user.type(within(dialog).getByLabelText(/^address/i), 'http://hermod-ml:8090')
    await user.type(within(dialog).getByLabelText(/^token/i), 'wrong')
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    expect(await within(dialog).findByText(/refused the token/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/exact token the worker was started with/i)).toBeInTheDocument()
    expect(calls.tests[0]).toEqual({ url: 'http://hermod-ml:8090', token: 'wrong' })

    answer = { result: 'unreachable', message: 'Nothing answered at this address: connection refused' }
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    expect((await within(dialog).findAllByText(/nothing answered at this address/i)).length).toBeGreaterThan(0)
    expect(within(dialog).getByText(/container name on a\s+network both share/i)).toBeInTheDocument()

    answer = { result: 'not_a_worker', message: 'Something answered at this address, but it is not a hermod-ml worker' }
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    expect(await within(dialog).findByText(/not hermod-ml/i)).toBeInTheDocument()

    answer = { result: 'bad_address', message: 'the address must start with http://' }
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    expect(await within(dialog).findByText(/not an address hermod can call/i)).toBeInTheDocument()

    // A failed test still lets the person save, saying so.
    expect(within(dialog).getByRole('button', { name: /save anyway/i })).toBeEnabled()
  })

  it('saves after a good test, and Getting started turns done without a reload', async () => {
    let status: any = off
    const calls = pageApi({
      worker: () => status,
      probe: () => ({ result: 'ok', message: 'Connected.', deep_learning: true, frameworks: ['PyTorch', 'TensorFlow'] }),
    })
    renderPage()
    const user = userEvent.setup()
    const dialog = await openSetup(user)

    // Nothing to save before a test.
    expect(within(dialog).getByRole('button', { name: /^save/i })).toBeDisabled()

    await user.type(within(dialog).getByLabelText(/^address/i), 'http://hermod-ml:8090')
    await user.type(within(dialog).getByLabelText(/^token/i), 's3cret')
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    expect(await within(dialog).findByText(/deep learning \(pytorch, tensorflow\)/i)).toBeInTheDocument()

    status = ready
    const before = calls.worker
    await user.click(within(dialog).getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(calls.saves).toEqual([{ url: 'http://hermod-ml:8090', token: 's3cret' }]))
    await waitFor(() => expect(calls.worker).toBeGreaterThan(before))
    expect(await within(dialog).findByText(/saved\. hermod uses this worker now/i)).toBeInTheDocument()

    const step = await workerStep()
    expect(await within(step).findByText('Done')).toBeInTheDocument()
  })

  it('keeps a saved token when only the address changes', async () => {
    const calls = pageApi({ connection: () => ({ source: 'saved', url: 'http://old:8090', token_set: true, editable: true }) })
    renderPage()
    const user = userEvent.setup()
    const dialog = await openSetup(user)

    const address = within(dialog).getByLabelText(/^address/i) as HTMLInputElement
    await waitFor(() => expect(address.value).toBe('http://old:8090'))
    expect(within(dialog).getByLabelText(/^token/i)).toHaveAttribute('placeholder', expect.stringMatching(/saved/i))
    await user.clear(address)
    await user.type(address, 'http://hermod-ml:8090')
    await user.click(within(dialog).getByRole('button', { name: /test connection/i }))
    await user.click(await within(dialog).findByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(calls.saves).toEqual([{ url: 'http://hermod-ml:8090' }]))
    expect(calls.tests[0]).toEqual({ url: 'http://hermod-ml:8090' })
  })
})

describe('Worker setup: who may connect it', () => {
  it('shows a worker set by the environment read-only', async () => {
    const calls = pageApi({
      worker: () => ({ ...ready, source: 'environment' }),
      connection: () => ({ source: 'environment', url: 'http://env-worker:8090', token_set: true, editable: false }),
    })
    renderPage()
    const user = userEvent.setup()
    const step = await workerStep()
    await user.click(await within(step).findByRole('button', { name: /how to add deep learning/i }))
    const dialog = await screen.findByRole('dialog', { name: /set up the training worker/i })

    expect(await within(dialog).findByText(/set by the server's environment/i)).toBeInTheDocument()
    expect(within(dialog).getByText('http://env-worker:8090')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /^save/i })).not.toBeInTheDocument()
    expect(within(dialog).queryByLabelText(/^token/i)).not.toBeInTheDocument()
    expect(calls.connection).toBeGreaterThan(0)
  })

  it('tells someone who is not an administrator whom to ask, with no form', async () => {
    const calls = pageApi()
    renderPage('Editor')
    const user = userEvent.setup()
    const dialog = await openSetup(user)

    expect(within(dialog).getByText(/ask an administrator/i)).toBeInTheDocument()
    expect(within(dialog).queryByLabelText(/^address/i)).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /test connection/i })).not.toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /check again/i })).toBeInTheDocument()
    expect(calls.connection).toBe(0)
  })
})

describe('workerImageTag', () => {
  it.each([
    ['v1.18.0 (Enterprise Edition)', false, '1.18.0'],
    ['v1.18.0 (Enterprise Edition)', true, '1.18.0-dl'],
    ['v1.19.0-rc.1 (Enterprise Edition)', false, '1.19.0-rc.1'],
    ['v1.2.5-0.20260730072614-816c03f6a612+dirty (Enterprise Edition)', false, 'latest'],
    ['dev (Enterprise Edition)', true, 'latest-dl'],
    [undefined, false, 'latest'],
  ])('%s, deep learning %s → %s', (version, deep, tag) => {
    expect(workerImageTag(version, deep)).toBe(tag)
  })
})
