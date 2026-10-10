import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { MonitoringModal } from '@/pages/ml/MonitoringModal'
import type { DriftStatus, MLModel } from '@/lib/mlModels'

/**
 * Monitoring: what a model was asked and answered (the prediction log), how
 * far its live inputs moved from its training data (drift), and the setting
 * that turns logging on.
 */

const churn: MLModel = {
  name: 'churn', backend: 'hermod-ml', url: '', remote_version: '3', serving: false,
  monitoring: { log_sample_rate: 0.5, log_mask_fields: ['email'] },
}

function monitoringApi(drift: DriftStatus) {
  const writes: Array<{ path: string; body: any }> = []
  server.use(
    http.get('/api/vhosts/:vhost/ml/models/:name/predictions', ({ params }) => HttpResponse.json({
      data: params.name !== 'churn' ? [] : [{
        vhost: 'tenant-a', model: 'churn', version: '3', timestamp: '2026-10-10T10:00:00Z',
        inputs: { age: 41, email: '****' }, outputs: { label: 'yes' }, latency_ms: 12.5,
        caller_kind: 'workflow', caller_id: 'wf-orders',
      }],
      total: 1,
    })),
    http.get('/api/vhosts/:vhost/ml/models/:name/drift', () => HttpResponse.json(drift)),
    http.put('/api/vhosts/:vhost/ml/models/:name/monitoring', async ({ params, request }) => {
      const body = (await request.json()) as any
      writes.push({ path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json({ ...churn, monitoring: body })
    }),
  )
  return writes
}

function renderModal(role = 'Editor') {
  signInAs(role)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <MonitoringModal vhost="tenant-a" model={churn} onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Model monitoring', () => {
  it('lists the logged predictions with their caller', async () => {
    monitoringApi({ report: null, reason: 'no window has been judged yet' })
    renderModal()

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('wf-orders')).toBeInTheDocument()
    expect(within(dialog).getByText(/"email":"\*\*\*\*"/)).toBeInTheDocument()
    expect(within(dialog).getByText(/"label":"yes"/)).toBeInTheDocument()
  })

  it('shows the drift of each feature against its thresholds', async () => {
    monitoringApi({
      report: {
        vhost: 'tenant-a', model: 'churn', version: '3', rows: 240, warn: 0.1, alert: 0.25, status: 'alert',
        window_start: '2026-10-10T09:55:00Z', window_end: '2026-10-10T10:00:00Z',
        features: [
          { feature: 'age', kind: 'numeric', psi: 1.73, status: 'alert', null_fraction: 0, training_null_fraction: 0 },
          { feature: 'plan', kind: 'categorical', psi: 0.02, status: 'ok', null_fraction: 0, training_null_fraction: 0 },
        ],
      },
    })
    renderModal()
    const user = userEvent.setup()

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: /drift/i }))
    const age = (await within(dialog).findByText('age')).closest('tr') as HTMLElement
    expect(within(age).getByText('1.730')).toBeInTheDocument()
    expect(within(age).getByText(/alert/i)).toBeInTheDocument()
    const plan = within(dialog).getByText('plan').closest('tr') as HTMLElement
    expect(within(plan).getByText(/ok/i)).toBeInTheDocument()
  })

  it('says why there is no drift report yet', async () => {
    monitoringApi({ report: null, reason: 'no window has been judged yet', window: '5m0s', min_rows: 100 })
    renderModal()
    const user = userEvent.setup()

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: /drift/i }))
    expect(await within(dialog).findByText(/no window has been judged yet/)).toBeInTheDocument()
  })

  it('saves the logging setting', async () => {
    const writes = monitoringApi({ report: null, reason: 'x' })
    renderModal()
    const user = userEvent.setup()

    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: /settings/i }))
    const rate = within(dialog).getByRole('textbox', { name: /share of predictions logged/i })
    await user.clear(rate)
    await user.type(rate, '0.25')
    const retention = within(dialog).getByRole('textbox', { name: /keep for/i })
    await user.type(retention, '30d')
    await user.click(within(dialog).getByRole('button', { name: /save monitoring/i }))

    await waitFor(() => expect(writes).toHaveLength(1))
    expect(writes[0].path).toBe('tenant-a/churn')
    expect(writes[0].body).toMatchObject({ log_sample_rate: 0.25, log_mask_fields: ['email'], log_retention: '30d' })
  })

  it('does not offer the setting to a viewer', async () => {
    monitoringApi({ report: null, reason: 'x' })
    renderModal('Viewer')

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('tab', { name: /settings/i })).not.toBeInTheDocument()
  })
})
