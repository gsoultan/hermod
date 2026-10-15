import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { DeadLettersPage } from '@/pages/monitoring/DeadLettersPage'
import type { DeadLetter } from '@/lib/deadLetters'

/**
 * Dead letters: every message a workflow parked, whatever its dead-letter
 * sink. The page lists them, shows one with its payload, and replays (as it
 * was or edited) or discards them, one at a time or in bulk.
 */

const b64 = (s: string) => btoa(s)

const orders: DeadLetter = {
  id: 'dl-1', vhost: 'tenant-a', workflow_id: 'wf-orders', workflow_name: 'Orders sync', message_id: 'ord-88412',
  node_id: 'normalize', error: 'field total is not a number: got "12,50"', failed_at: '2026-10-14T04:52:57Z',
  payload_size: 40, status: 'queued',
}
const invoices: DeadLetter = {
  id: 'dl-2', vhost: 'tenant-a', workflow_id: 'wf-inv', workflow_name: 'Invoices to SAP', message_id: 'inv-9',
  sink_id: 'snk-sap', error: 'connection refused', failed_at: '2026-10-14T04:40:00Z', payload_size: 2, status: 'queued',
}

function deadLetterApi(rows: DeadLetter[] = [orders, invoices]) {
  const calls: Array<{ path: string; body?: any }> = []
  server.use(
    http.get('/api/workflows', () => HttpResponse.json({ data: [
      { id: 'wf-orders', name: 'Orders sync', nodes: [{ id: 'normalize', type: 'transformation', config: { label: 'Normalize order' } }] },
      { id: 'wf-inv', name: 'Invoices to SAP', nodes: [] },
    ], total: 2 })),
    http.get('/api/dead-letters/stats', () => HttpResponse.json({
      queued: 1284, last_24h: 312, top_error: 'field total is not a number', top_error_count: 218, replayed_this_week: 940,
    })),
    http.get('/api/dead-letters', ({ request }) => {
      calls.push({ path: 'list?' + new URL(request.url).searchParams.toString() })
      return HttpResponse.json({ data: rows, total: rows.length })
    }),
    http.get('/api/dead-letters/:id', ({ params }) => {
      const d = rows.find((r) => r.id === params.id)!
      return HttpResponse.json({
        ...d,
        payload: b64('{"id":"ord-88412","total":"12,50"}'),
        metadata: { table: 'orders' },
        origin: { operation: 'update', table: 'orders', before: b64('{"id":"ord-88412","total":"12.00"}') },
        history: [{ at: d.failed_at, action: 'failed', detail: 'normalize' }],
      })
    }),
    http.post('/api/dead-letters/discard', async ({ request }) => {
      const body = await request.json() as any
      calls.push({ path: 'discard', body })
      return HttpResponse.json({ results: body.ids.map((id: string) => ({ id, status: 'discarded' })) })
    }),
    http.post('/api/dead-letters/replay', async ({ request }) => {
      const body = await request.json() as any
      calls.push({ path: 'replay', body })
      return HttpResponse.json({ results: body.ids.map((id: string) => ({ id, status: 'replayed' })) })
    }),
    http.post('/api/dead-letters/:id/replay', async ({ params, request }) => {
      const body = await request.json() as any
      calls.push({ path: `${params.id}/replay`, body })
      if (body.test) {
        return HttpResponse.json({ steps: [
          { node_id: 'normalize', node_type: 'transformation', payload: { total: '12.50' } },
          { node_id: 'out', node_type: 'sink', payload: { total: '12.50' } },
        ] })
      }
      return HttpResponse.json({ id: params.id, status: 'replayed' })
    }),
  )
  return calls
}

function renderPage(role = 'Editor') {
  signInAs(role)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <ConfirmProvider>
            <DeadLettersPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Dead letters page', () => {
  it('leads with the summary and lists what was parked, where and why', async () => {
    deadLetterApi()
    renderPage()

    expect(await screen.findByText('1,284')).toBeInTheDocument()
    expect(screen.getByText('312')).toBeInTheDocument()
    expect(screen.getByText('940')).toBeInTheDocument()
    const table = screen.getByRole('table')
    expect(await within(table).findByText('Orders sync')).toBeInTheDocument()
    expect(within(table).getByText('Node: Normalize order')).toBeInTheDocument()
    expect(within(table).getByText('Sink: snk-sap')).toBeInTheDocument()
    expect(within(table).getByText('connection refused')).toBeInTheDocument()
  })

  it('says which workflows land here and that the sink keeps its own copy', async () => {
    deadLetterApi()
    renderPage()

    expect(await screen.findByText(/workflows with a dead letter sink/i)).toBeInTheDocument()
    expect(screen.getByText(/does not remove the copy in the workflow's dead letter sink/i)).toBeInTheDocument()
  })

  it('searches, and asks only for waiting messages by default', async () => {
    const calls = deadLetterApi()
    renderPage()
    const user = userEvent.setup()
    await screen.findByText('Invoices to SAP', { selector: 'td *, td' })
    expect(calls[0].path).toContain('status=queued')

    await user.type(screen.getByRole('textbox', { name: /search/i }), 'refused')
    await waitFor(() => expect(calls.some((c) => c.path.includes('search=refused'))).toBe(true))
  })

  it('opens a message with its payload, and discards it after confirming', async () => {
    const calls = deadLetterApi()
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /open message ord-88412/i }))
    const panel = await screen.findByRole('dialog', { name: /message ord-88412/i })
    expect((await within(panel).findAllByText(/"total": "12,50"/))[0]).toBeInTheDocument()

    await user.click(within(panel).getByRole('tab', { name: /before \/ after/i }))
    expect(await within(panel).findByText(/"total": "12.00"/)).toBeInTheDocument()

    await user.click(within(panel).getByRole('button', { name: /^discard$/i }))
    const confirm = await screen.findByRole('dialog', { name: /discard/i })
    await user.click(within(confirm).getByRole('button', { name: /discard/i }))
    await waitFor(() => expect(calls).toContainEqual({ path: 'discard', body: { ids: ['dl-1'] } }))
  })

  it('replays the selected messages together', async () => {
    const calls = deadLetterApi()
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('checkbox', { name: /select ord-88412/i }))
    await user.click(screen.getByRole('checkbox', { name: /select inv-9/i }))
    expect(screen.getByText('2 selected')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /replay 2/i }))
    await waitFor(() => expect(calls).toContainEqual({ path: 'replay', body: { ids: ['dl-1', 'dl-2'] } }))
  })

  it('edits a payload, shows what changed, tests it and replays it', async () => {
    const calls = deadLetterApi()
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /open message ord-88412/i }))
    const panel = await screen.findByRole('dialog', { name: /message ord-88412/i })
    await within(panel).findAllByText(/"total": "12,50"/)
    await user.click(within(panel).getByRole('button', { name: /edit and replay/i }))

    const dialog = await screen.findByRole('dialog', { name: /edit and replay ord-88412/i })
    const editor = within(dialog).getByRole('textbox', { name: /payload/i })
    await user.clear(editor)
    await user.click(editor)
    await user.paste('{"id":"ord-88412","total"')
    expect(within(dialog).getByText(/not valid json/i)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /test and replay/i })).toBeDisabled()

    await user.paste(':"12.50"}')
    expect(within(dialog).getByText(/1 field changed/i)).toHaveTextContent('total "12,50" → "12.50"')

    await user.click(within(dialog).getByRole('button', { name: /test and replay/i }))
    await waitFor(() => expect(calls.find((c) => c.path === 'dl-1/replay')?.body).toEqual({
      payload: '{"id":"ord-88412","total":"12.50"}', from_start: false, test: true,
    }))
    expect(await within(dialog).findByText(/normalize/)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: /replay now/i }))
    await waitFor(() => expect(calls.filter((c) => c.path === 'dl-1/replay').at(-1)?.body).toEqual({
      payload: '{"id":"ord-88412","total":"12.50"}', from_start: false, test: false,
    }))
  })

  it('opens on the workflow a mail linked to', async () => {
    const calls = deadLetterApi()
    window.history.pushState({}, '', '/dead-letters?workflow_id=wf-orders')
    try {
      renderPage()
      await waitFor(() => expect(calls.some((c) => c.path.includes('workflow_id=wf-orders'))).toBe(true))
    } finally {
      window.history.pushState({}, '', '/')
    }
  })

  it('offers a viewer nothing that changes the queue', async () => {
    deadLetterApi()
    renderPage('Viewer')

    await user_open()
    const panel = await screen.findByRole('dialog', { name: /message ord-88412/i })
    expect(within(panel).queryByRole('button', { name: /replay/i })).not.toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: /discard/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: /select ord-88412/i })).not.toBeInTheDocument()
  })
})

async function user_open() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: /open message ord-88412/i }))
}
