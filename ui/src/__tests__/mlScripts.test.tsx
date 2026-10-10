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
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Custom training scripts and GPU pools. Both are server options: the Scripts
 * tab, the custom:<script> algorithms and the device picker appear only when
 * the worker status says the server offers them. Saving and deleting a script
 * is an Administrator's; an Editor reads them and trains with them.
 */

const customers = {
  name: 'customers', rows: 120, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'churned', type: 'string' }],
}

const SOURCE = 'def train(df, spec):\n    return None\n\ndef export_onnx(model, spec):\n    return b""\n'

interface Caps { custom_scripts?: boolean; scripts_enabled?: boolean; gpu?: boolean }

function api(caps: Caps = {}) {
  const writes: Array<{ method: string; path: string; body?: any }> = []
  const scripts: any[] = [{ vhost: 'tenant-a', name: 'tabnet', version: 2, sha256: 'ab'.repeat(32), created_by: 'root', created_at: '2026-10-10T00:00:00Z' }]
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({
      configured: true, ready: true,
      capabilities: { custom_scripts: false, scripts_enabled: false, gpu: false, ...caps },
    })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json(customers)),
    http.get('/api/vhosts/:vhost/ml/scripts', () => HttpResponse.json({ data: scripts, total: scripts.length })),
    http.get('/api/vhosts/:vhost/ml/scripts/:name', ({ params }) => HttpResponse.json({
      script: { ...scripts[0], name: params.name, source: SOURCE },
      versions: [scripts[0], { ...scripts[0], version: 1, sha256: 'cd'.repeat(32) }],
    })),
    http.put('/api/vhosts/:vhost/ml/scripts/:name', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'PUT', path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({ vhost: params.vhost, name: params.name, version: 1, sha256: 'ef'.repeat(32), created_at: '2026-10-10T00:00:00Z' })
    }),
    http.delete('/api/vhosts/:vhost/ml/scripts/:name', ({ params }) => {
      writes.push({ method: 'DELETE', path: `${params.vhost}/${params.name}` })
      return new HttpResponse(null, { status: 204 })
    }),
    http.post('/api/vhosts/:vhost/ml/models/:name/train', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ method: 'TRAIN', path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({
        model: { name: params.name, backend: 'hermod-ml', remote_version: '1' },
        version: {
          model: params.name, version: '1', task: 'classification', algorithm: body.algorithm, dataset: 'customers',
          target: 'churned', features: ['age'], metrics: { accuracy: 0.9, score: 0.9 }, rows: { train: 96, test: 24 },
          created_at: '2026-10-10T00:00:00Z',
        },
        live: true, reason: 'the model had no live version',
      })
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

async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

/** The options a Select offers, by their visible text. */
async function optionsOf(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp) {
  const input = within(scope).queryByRole('combobox', { name: label }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  const names = within(list).getAllByRole('option', { hidden: true }).map((o) => o.textContent)
  await user.keyboard('{Escape}')
  return names
}

async function openTrain(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /train a model/i }))
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByRole('textbox', { name: /model name/i }), 'churn')
  await pick(user, dialog, /^dataset/i, 'customers')
  await pick(user, dialog, /column to predict/i, 'churned')
  return dialog
}

describe('Custom training scripts on the Models page', () => {
  it('shows no Scripts tab, no custom algorithm and no device when the server offers neither', async () => {
    api()
    renderPage()
    const user = userEvent.setup()
    const dialog = await openTrain(user)
    expect(screen.queryByRole('tab', { name: /scripts/i })).not.toBeInTheDocument()
    expect((await optionsOf(user, dialog, /^algorithm/i)).join('|')).not.toMatch(/custom/i)
    expect(within(dialog).queryByRole('textbox', { name: /^device/i })).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('combobox', { name: /^device/i })).not.toBeInTheDocument()
  })

  it('lets an Administrator save a new script and delete one', async () => {
    const writes = api({ custom_scripts: true, scripts_enabled: true })
    renderPage('Administrator')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: /scripts/i }))
    expect(await screen.findByText('tabnet')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /new script/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /script name/i }), 'lgbm')
    const editor = within(dialog).getByRole('textbox', { name: /^source/i })
    await user.clear(editor)
    await user.click(editor)
    await user.paste(SOURCE)
    await user.click(within(dialog).getByRole('button', { name: /^save/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    expect(writes.find((w) => w.method === 'PUT')).toMatchObject({ path: 'tenant-a/lgbm', body: { source: SOURCE } })

    await user.click(await screen.findByRole('button', { name: 'Delete script tabnet' }))
    await user.click(await screen.findByRole('button', { name: /delete script$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'DELETE')?.path).toBe('tenant-a/tabnet'))
  })

  it('shows an Editor a script and its versions, read-only', async () => {
    api({ custom_scripts: true, scripts_enabled: true })
    renderPage('Editor')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: /scripts/i }))
    expect(screen.queryByRole('button', { name: /new script/i })).not.toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Open script tabnet' }))
    const dialog = await screen.findByRole('dialog')
    const editor = await within(dialog).findByRole('textbox', { name: /^source/i })
    await waitFor(() => expect(editor).toHaveValue(SOURCE))
    expect(editor).toHaveAttribute('readonly')
    expect(within(dialog).getByText(/version 1/i)).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: /^save/i })).not.toBeInTheDocument()
  })

  it('trains with a custom script when the server offers it', async () => {
    const writes = api({ custom_scripts: true, scripts_enabled: true })
    renderPage()
    const user = userEvent.setup()
    const dialog = await openTrain(user)
    await pick(user, dialog, /^algorithm/i, 'Script: tabnet')
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    expect(writes.find((w) => w.method === 'TRAIN')!.body).toMatchObject({ algorithm: 'custom:tabnet' })
    expect(writes.find((w) => w.method === 'TRAIN')!.body.device).toBeUndefined()
  })

  it('says scripts are saved but cannot train until the pool exists', async () => {
    api({ custom_scripts: false, scripts_enabled: true })
    renderPage('Administrator')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('tab', { name: /scripts/i }))
    expect(await screen.findByText(/HERMOD_ML_CUSTOM_WORKER_URL/)).toBeInTheDocument()
  })
})

describe('GPU pool', () => {
  it('sends device gpu when one is configured and chosen', async () => {
    const writes = api({ gpu: true })
    renderPage()
    const user = userEvent.setup()
    const dialog = await openTrain(user)
    await pick(user, dialog, /^device/i, 'GPU')
    await user.click(within(dialog).getByRole('button', { name: /^train$/i }))
    await waitFor(() => expect(writes.find((w) => w.method === 'TRAIN')).toBeTruthy())
    expect(writes.find((w) => w.method === 'TRAIN')!.body).toMatchObject({ device: 'gpu', algorithm: 'auto' })
  })
})

function NodeHarness({ onConfig }: { onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>({ model: 'churn', dataset: 'customers', target: 'churned' })
  return (
    <MLTrainConfig nodeId="n1" config={config} sources={[]}
      updateNodeConfig={(_id: string, patch: any) => {
        setConfig((c) => {
          const next = { ...c, ...patch }
          onConfig(next)
          return next
        })
      }} />
  )
}

describe('Train Model node', () => {
  it('offers the vhost scripts and the GPU when the server does, and saves them', async () => {
    api({ custom_scripts: true, scripts_enabled: true, gpu: true })
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
    // The device picker waits for the worker status.
    await screen.findByText(/^Device/)
    await pick(user, document.body, /^device/i, 'GPU')
    await waitFor(() => expect(last).toMatchObject({ device: 'gpu' }))
    // A script runs on its own pool, so picking one drops the device.
    await pick(user, document.body, /^algorithm/i, 'Script: tabnet')
    await waitFor(() => expect(last).toMatchObject({ algorithm: 'custom:tabnet', device: '' }))
    expect(screen.queryByRole('textbox', { name: /^device/i })).not.toBeInTheDocument()
  })

  it('offers neither when the server does not', async () => {
    api()
    signInAs('Editor')
    useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={qc}>
          <NodeHarness onConfig={() => {}} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    const user = userEvent.setup()
    await screen.findByText(/^Algorithm/)
    expect((await optionsOf(user, document.body, /^algorithm/i)).join('|')).not.toMatch(/script/i)
    expect(screen.queryByRole('textbox', { name: /^device/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /^device/i })).not.toBeInTheDocument()
  })
})
