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

/**
 * Analyzing a dataset on the Data tab: every dataset has an Analyze button,
 * a Parquet file uploads like a CSV and is analyzed as soon as it is in, and
 * the analysis says what the data holds, what to fix and how, what each
 * column looks like, and that a big dataset was measured on a sample.
 */

const orders = {
  name: 'orders', rows: 1_200_000, updated_at: '2026-10-10T00:00:00Z',
  columns: [{ name: 'customer_id', type: 'string' }, { name: 'amount', type: 'number' }, { name: 'note', type: 'string' }],
}

const ordersProfile = {
  dataset: 'orders', rows: 1_200_000, column_count: 3, sample_rows: 50_000, sampled: true, max_sample_rows: 50_000,
  duplicate_rows: 0, computed_at: '2026-10-15T00:00:00Z', correlations: [],
  columns: [
    { name: 'customer_id', type: 'string', kind: 'category', missing: 0, missing_share: 0, distinct: 900,
      top: [{ value: 'c1', rows: 300, share: 0.006 }], other_share: 0.994 },
    { name: 'amount', type: 'number', kind: 'number', missing: 0, missing_share: 0, distinct: 4000,
      numbers: { min: 1, max: 900, mean: 40, median: 30, q1: 12, q3: 55, std: 35, skew: 2.4, integer: false, outliers: 12 },
      histogram: [{ from: 1, to: 100, rows: 45_000 }, { from: 100, to: 900, rows: 5_000 }] },
    { name: 'note', type: 'string', kind: 'empty', missing: 50_000, missing_share: 1, distinct: 0 },
  ],
  warnings: [
    { code: 'empty_column', severity: 'high', column: 'note', message: 'note is empty in every row.', fix: 'Leave it out when training, or fill it at the source.' },
    { code: 'skewed', severity: 'low', column: 'amount', message: 'amount has a long tail of large values.', fix: 'Usually fine; a log of it can help a linear model.' },
  ],
}

function api() {
  const writes: Array<{ path: string; body: string }> = []
  let datasets = [orders]
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/jobs', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/vhosts/:vhost/ml/datasets', () => HttpResponse.json({ data: datasets, total: datasets.length })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name', ({ params }) => HttpResponse.json({ ...orders, name: params.name, sample: [] })),
    http.get('/api/vhosts/:vhost/ml/datasets/:name/profile', ({ params }) =>
      HttpResponse.json({ ...ordersProfile, dataset: params.name })),
    http.put('/api/vhosts/:vhost/ml/datasets/:name/file', async ({ params, request }) => {
      writes.push({ path: `${params.name}${new URL(request.url).search}`, body: await request.text() })
      const info = { ...orders, name: String(params.name), rows: 3 }
      datasets = [...datasets, info]
      return HttpResponse.json(info)
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

async function openData(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('tab', { name: 'Data' }))
}

describe('Analyzing a dataset', () => {
  it('says what the data holds, that it was sampled, and what to fix and how', async () => {
    api()
    renderPage()
    const user = userEvent.setup()
    await openData(user)
    await user.click(within(await screen.findByRole('table', { name: 'Datasets' })).getByRole('button', { name: 'Analyze orders' }))
    const dialog = await screen.findByRole('dialog', { name: /check orders/i })
    expect(await within(dialog).findByText(/1,200,000 rows and 3 columns/)).toBeInTheDocument()
    expect(within(dialog).getByText(/measured on 50,000 rows spread evenly over all 1,200,000/i)).toBeInTheDocument()
    const checks = within(dialog).getByRole('list', { name: /things to check/i })
    const items = within(checks).getAllByRole('listitem')
    // Worst first, each with how to fix it.
    expect(items[0]).toHaveTextContent('note is empty in every row.')
    expect(items[0]).toHaveTextContent(/how to fix: leave it out when training/i)
    expect(within(items[0]).getByText('Fix before training')).toBeInTheDocument()
    expect(items[1]).toHaveTextContent(/good to know/i)
  })

  it('shows each column with its numbers and a chart that says what it draws', async () => {
    api()
    renderPage()
    const user = userEvent.setup()
    await openData(user)
    await user.click(within(await screen.findByRole('table', { name: 'Datasets' })).getByRole('button', { name: 'Analyze orders' }))
    const dialog = await screen.findByRole('dialog', { name: /check orders/i })
    await user.click(within(dialog).getByRole('tab', { name: 'Columns' }))
    expect(await within(dialog).findByRole('figure', { name: /amount/i })).toBeInTheDocument()
    expect(within(dialog).getAllByText(/median/i).length).toBeGreaterThan(0)
  })

  it('shows the first rows from the eye button', async () => {
    api()
    renderPage()
    const user = userEvent.setup()
    await openData(user)
    await user.click(await screen.findByRole('button', { name: 'Show rows of orders' }))
    const dialog = await screen.findByRole('dialog', { name: /check orders/i })
    expect(within(dialog).getByRole('tab', { name: 'First rows', selected: true })).toBeInTheDocument()
  })

  it('uploads a Parquet file and analyzes it as soon as it is in', async () => {
    const writes = api()
    renderPage()
    const user = userEvent.setup()
    await openData(user)
    await user.click(await screen.findByRole('button', { name: /upload a file/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /dataset name/i }), 'sales')
    const file = new File(['PAR1-bytes-PAR1'], 'sales.parquet', { type: 'application/vnd.apache.parquet' })
    await user.upload(within(dialog).getByLabelText(/file/i, { selector: 'input[type=file]' }), file)
    await user.click(within(dialog).getByRole('button', { name: /^upload$/i }))
    await waitFor(() => expect(writes).toHaveLength(1))
    expect(writes[0]).toEqual({ path: 'sales?format=parquet', body: 'PAR1-bytes-PAR1' })
    expect(await screen.findByRole('dialog', { name: /check sales/i })).toBeInTheDocument()
  })

  it('refuses a file it cannot read before sending it', async () => {
    const writes = api()
    renderPage()
    const user = userEvent.setup({ applyAccept: false })
    await openData(user)
    await user.click(await screen.findByRole('button', { name: /upload a file/i }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: /dataset name/i }), 'old')
    await user.upload(within(dialog).getByLabelText(/file/i, { selector: 'input[type=file]' }), new File(['x'], 'old.xls'))
    expect(await within(dialog).findByText(/not a file Hermod reads/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /^upload$/i })).toBeDisabled()
    expect(writes).toHaveLength(0)
  })
})
