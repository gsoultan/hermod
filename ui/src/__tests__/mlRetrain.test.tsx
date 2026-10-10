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
import { MLDatasetSinkConfig } from '@/components/workflow/Sink/MLDatasetSinkConfig'
import { SINK_TYPES, configComponents } from '@/components/forms/SinkForm'
import { missingConnectionFields } from '@/lib/connectorRequirements'
import { describeRetrain } from '@/lib/mlModels'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

/**
 * Retraining and collecting: a trained model can train again by itself, on a
 * schedule or once its dataset has grown, and the page shows how the last
 * retraining went. The Collect Dataset sink is what grows the dataset.
 */

const customers = {
  name: 'customers', rows: 120, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'age', type: 'number' }, { name: 'plan', type: 'string' }, { name: 'churned', type: 'string' }],
}

const policy = {
  schedule: '@daily', new_rows: 500,
  spec: { dataset: 'customers', target: 'churned', features: ['age'], task: 'classification', algorithm: 'auto' },
  go_live: { mode: 'if', metric: 'score', min: 0.8 },
  updated_by: 'ada', updated_at: '2026-10-09T00:00:00Z',
}

function retrainApi(models: any[]) {
  const writes: Array<{ method: string; path: string; body?: any }> = []
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: models.length })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: [customers], total: 1 })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', () => HttpResponse.json(customers)),
    http.put('/api/vhosts/:vhost/ml/models/:name/retrain', async ({ params, request }) => {
      const body = await request.json()
      writes.push({ method: 'PUT', path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({ ...models[0], retrain: body })
    }),
    http.delete('/api/vhosts/:vhost/ml/models/:name/retrain', ({ params }) => {
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

/** Picks an option of a Mantine Select by its accessible name. */
async function pick(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: RegExp, option: string) {
  const input = within(scope).queryByRole('combobox', { name: label }) ?? within(scope).getByRole('textbox', { name: label })
  await user.click(input)
  await waitFor(() => expect(document.getElementById(input.getAttribute('aria-controls') ?? '')).toBeTruthy())
  const list = document.getElementById(input.getAttribute('aria-controls') as string) as HTMLElement
  await user.click(await within(list).findByRole('option', { name: option, hidden: true }))
}

const trained = (extra: Record<string, unknown> = {}) => ({
  name: 'churn', backend: 'hermod-ml', url: '', remote_version: '2', serving: false, ...extra,
})

describe('Retraining on the Models page', () => {
  it('says when a model retrains and how the last retraining went', async () => {
    retrainApi([trained({
      retrain: policy,
      retrain_status: { at: '2026-10-10T03:00:00Z', trigger: 'schedule', version: '3', live: true, reason: 'score 0.91 reaches the minimum 0.8', dataset_rows: 620, trained_at: '2026-10-10T03:00:00Z' },
    })])
    renderPage()
    expect(await screen.findByText(/retrains @daily or after 500 new rows/i)).toBeInTheDocument()
    expect(screen.getByText(/last retrain: version 3, live/i)).toBeInTheDocument()
  })

  it('shows a failed retraining with its error', async () => {
    retrainApi([trained({
      retrain: policy,
      retrain_status: { at: '2026-10-10T03:00:00Z', trigger: 'new_rows', live: false, error: 'dataset "customers" has no column "churned"', dataset_rows: 0, trained_at: '0001-01-01T00:00:00Z' },
    })])
    renderPage()
    expect(await screen.findByText(/last retrain failed: dataset "customers" has no column "churned"/i)).toBeInTheDocument()
  })

  it('sets a retrain policy on a trained model', async () => {
    const writes = retrainApi([trained()])
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /retrain churn automatically/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /schedule/i }), '0 3 * * *')
    await user.type(within(dialog).getByRole('textbox', { name: /new rows/i }), '1000')
    await pick(user, dialog, /dataset/i, 'customers')
    await pick(user, dialog, /column to predict/i, 'churned')
    await user.click(within(dialog).getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(writes.find((w) => w.method === 'PUT')).toBeTruthy())
    const put = writes.find((w) => w.method === 'PUT')!
    expect(put.path).toBe('tenant-a/churn')
    expect(put.body).toMatchObject({
      schedule: '0 3 * * *', new_rows: 1000,
      spec: { dataset: 'customers', target: 'churned', task: 'auto', algorithm: 'auto' },
      go_live: { mode: 'never' },
    })
  })

  it('asks for a schedule or a row count before saving', async () => {
    const writes = retrainApi([trained()])
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /retrain churn automatically/i }))
    const dialog = await screen.findByRole('dialog')
    await pick(user, dialog, /dataset/i, 'customers')
    await pick(user, dialog, /column to predict/i, 'churned')
    expect(within(dialog).getByRole('button', { name: /^save$/i })).toBeDisabled()
    expect(writes).toEqual([])
  })

  it('opens with the current policy and can stop retraining', async () => {
    const writes = retrainApi([trained({ retrain: policy })])
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /retrain churn automatically/i }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('textbox', { name: /schedule/i })).toHaveValue('@daily')
    expect(within(dialog).getByRole('textbox', { name: /new rows/i })).toHaveValue('500')
    await user.click(within(dialog).getByRole('button', { name: /stop retraining/i }))
    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', path: 'tenant-a/churn' }]))
  })

  it('is not offered for a model served elsewhere', async () => {
    retrainApi([{ name: 'remote', backend: 'oip', url: 'http://kserve', serving: false }])
    renderPage()
    expect(await screen.findByText('remote')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retrain remote automatically/i })).not.toBeInTheDocument()
  })

  it('describes a policy in words', () => {
    expect(describeRetrain({ ...policy, new_rows: 0 } as any)).toBe('Retrains @daily')
    expect(describeRetrain({ ...policy, schedule: '' } as any)).toBe('Retrains after 500 new rows')
  })
})

function SinkHarness({ initial, onConfig }: { initial: Record<string, string>; onConfig: (c: Record<string, string>) => void }) {
  const [config, setConfig] = useState(initial)
  return (
    <MLDatasetSinkConfig
      vhost="tenant-a"
      config={config}
      updateConfig={(key: string, value: string) => {
        setConfig((c) => {
          const next = { ...c, [key]: value }
          onConfig(next)
          return next
        })
      }}
    />
  )
}

describe('Collect Dataset sink', () => {
  it('is offered as a sink, in the palette and the sink form, with its own editor', () => {
    const subTypes = NODE_CATEGORIES.flatMap((c) => c.items).filter((i: any) => i.type === 'sink').map((i: any) => i.subType)
    expect(subTypes).toContain('ml_dataset')
    expect(SINK_TYPES.map((t) => t.value)).toContain('ml_dataset')
    expect(configComponents.ml_dataset).toBeDefined()
  })

  it('needs a dataset before its connection step can be left', () => {
    expect(missingConnectionFields('sink', 'ml_dataset', {})).toEqual(['Dataset'])
    expect(missingConnectionFields('sink', 'ml_dataset', { dataset: 'customers' })).toEqual([])
  })

  it('writes the dataset, column mappings and masked fields the sink reads', async () => {
    signInAs('Editor')
    retrainApi([])
    let last: Record<string, string> = {}
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <MantineProvider>
        <QueryClientProvider client={qc}>
          <SinkHarness initial={{}} onConfig={(c) => { last = c }} />
        </QueryClientProvider>
      </MantineProvider>,
    )
    const user = userEvent.setup()
    const body = document.body
    await pick(user, body, /^dataset/i, 'customers')
    expect(last.dataset).toBe('customers')

    await user.click(screen.getByRole('button', { name: /add column/i }))
    await user.type(screen.getByRole('textbox', { name: /field 1/i }), 'customer.age')
    await user.type(screen.getByRole('textbox', { name: /column 1/i }), 'age')
    expect(JSON.parse(last.column_mappings)).toEqual([{ source_field: 'customer.age', target_column: 'age' }])

    await user.type(screen.getByRole('textbox', { name: /mask these columns/i }), 'email')
    expect(last.mask_fields).toBe('email')
  })
})
