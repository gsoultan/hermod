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
// One applicant's email, refused by the mail gateway every 20 seconds for an
// hour and forty minutes: 263 attempts, one row.
const resent: DeadLetter = {
  id: 'dl-3', vhost: 'tenant-a', workflow_id: 'wf-docs', workflow_name: 'Supporting Document Send', message_id: 'read-263',
  sink_id: 'snk-panmail', error: 'panmail: the api key was not accepted', failed_at: '2026-10-30T19:22:48Z',
  first_failed_at: '2026-10-30T17:41:34Z', failure_count: 263, record_key: 'payload:6c3cfb84', payload_size: 1800, status: 'queued',
}

function deadLetterApi(rows: DeadLetter[] = [orders, invoices], sinkDown = false) {
  const calls: Array<{ path: string; body?: any }> = []
  server.use(
    http.post('/api/dead-letters/:id/check', ({ params }) => {
      calls.push({ path: `${params.id}/check` })
      const d = rows.find((r) => r.id === params.id)!
      if (!d.sink_id) return HttpResponse.json({ checked: false })
      return HttpResponse.json(sinkDown
        ? { checked: true, sink_id: d.sink_id, sink_name: 'Panmail', reachable: false, error: 'dial tcp: connection refused' }
        : { checked: true, sink_id: d.sink_id, sink_name: 'Panmail', reachable: true })
    }),
    http.get('/api/workflows', () => HttpResponse.json({ data: [
      { id: 'wf-orders', name: 'Orders sync', nodes: [{ id: 'normalize', type: 'transformation', config: { label: 'Normalize order' } }] },
      { id: 'wf-inv', name: 'Invoices to SAP', nodes: [] },
      { id: 'wf-docs', name: 'Supporting Document Send', nodes: [] },
    ], total: 3 })),
    http.get('/api/dead-letters/stats', () => HttpResponse.json({
      queued: 1284, queued_attempts: 1784, last_24h: 312, top_error: 'field total is not a number', top_error_count: 218, replayed_this_week: 940,
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
        history: d.failure_count && d.failure_count > 1
          ? [{ at: d.first_failed_at, action: 'failed', detail: 'snk-panmail' },
            { at: '2026-10-30T17:41:54Z', action: 'failed_again', count: d.failure_count - 1, until: d.failed_at, detail: d.error }]
          : [{ at: d.failed_at, action: 'failed', detail: 'normalize' }],
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

  it('says which workflows land here, with no mention of a dead letter sink', async () => {
    deadLetterApi()
    renderPage()

    expect(await screen.findByText(/kept here when the flow is set to keep them/i)).toBeInTheDocument()
    expect(screen.queryByText(/dead.letter sink/i)).not.toBeInTheDocument()
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

    // The confirm counts messages that go out, and checks the sink first.
    const confirm = await screen.findByRole('dialog', { name: /replay 2 records/i })
    expect(await within(confirm).findByText(/sends 2 messages/i)).toBeInTheDocument()
    expect(await within(confirm).findByText(/panmail answers now/i)).toBeInTheDocument()
    expect(calls.filter((c) => c.path.endsWith('/check')).map((c) => c.path)).toEqual(['dl-2/check'])
    await user.click(within(confirm).getByRole('button', { name: /send 2 messages/i }))
    await waitFor(() => expect(calls).toContainEqual({ path: 'replay', body: { ids: ['dl-1', 'dl-2'] } }))
  })

  it('shows a record the source kept re-sending as one row with its count and span', async () => {
    deadLetterApi([resent, orders])
    renderPage()

    expect(await screen.findByText('1,284')).toBeInTheDocument()
    expect(screen.getByText(/1,784 failed attempts/)).toBeInTheDocument()
    const table = screen.getByRole('table')
    const row = (await within(table).findByText('read-263')).closest('tr')!
    expect(within(row).getByText('×263')).toBeInTheDocument()
    expect(within(row).getByText(/over 1 h 41 min/)).toBeInTheDocument()
    expect(within(row).getByText(/^first /i)).toBeInTheDocument()
    expect(within(table).getByText('Latest retry')).toBeInTheDocument()
  })

  it('warns before replaying a record whose sink still refuses, and folds its history', async () => {
    const calls = deadLetterApi([resent], true)
    renderPage()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /open message read-263/i }))
    const panel = await screen.findByRole('dialog', { name: /message read-263/i })
    expect(within(panel).getByText(/263 failed attempts/i)).toBeInTheDocument()
    await user.click(within(panel).getByRole('tab', { name: /history/i }))
    expect(await within(panel).findByText(/failed again ×262/)).toBeInTheDocument()

    await user.click(within(panel).getByRole('button', { name: /^replay$/i }))
    const confirm = await screen.findByRole('dialog', { name: /replay 1 record/i })
    expect(await within(confirm).findByText(/panmail does not answer/i)).toBeInTheDocument()
    expect(within(confirm).getByText(/connection refused/)).toBeInTheDocument()
    expect(within(confirm).getByText(/covers 263 failed attempts/i)).toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: /send 1 message anyway/i }))
    await waitFor(() => expect(calls).toContainEqual({ path: 'replay', body: { ids: ['dl-3'] } }))
  })

  it('offers the delivered status, for records the source sent again and got through', async () => {
    const calls = deadLetterApi()
    renderPage()
    const user = userEvent.setup()
    await screen.findByText('Invoices to SAP', { selector: 'td *, td' })

    await user.click(screen.getByRole('combobox', { name: /^status$/i }))
    await user.click(await screen.findByRole('option', { name: /delivered later/i, hidden: true }))
    await waitFor(() => expect(calls.some((c) => c.path.includes('status=delivered'))).toBe(true))
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
