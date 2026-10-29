import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ApprovalsPage } from '@/pages/workflows/ApprovalsPage'

const approvals = [
  { id: 'ap-1aaaaaaa', workflow_id: 'wf1', node_id: 'check', message_id: 'm1', status: 'pending', created_at: '2026-10-29T08:00:00Z' },
  { id: 'ap-2bbbbbbb', workflow_id: 'wf2', node_id: 'form', message_id: 'm2', status: 'pending', created_at: '2026-10-29T08:10:00Z', form_definition: { fields: [{ name: 'reason', type: 'text', label: 'Reason', required: true }] } },
  { id: 'ap-3ccccccc', workflow_id: 'wf1', node_id: 'check', message_id: 'm3', status: 'approved', created_at: '2026-10-28T08:00:00Z', processed_by: 'gembit' },
]
let requested: string[] = []
let decided: { id: string; verb: string; body: any }[] = []

function renderPage() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ApprovalsPage />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  requested = []
  decided = []
  signInAs('Editor')
  server.use(
    http.get('/api/approvals', ({ request }) => {
      const q = new URL(request.url).searchParams
      requested.push(q.toString())
      const status = q.get('status')
      const data = status ? approvals.filter((a) => a.status === status) : approvals
      return HttpResponse.json({ data, total: data.length })
    }),
    http.get('/api/workflows', () => HttpResponse.json({ data: [{ id: 'wf1', name: 'Billing sync' }, { id: 'wf2', name: 'Inventory alerts' }], total: 2 })),
    http.post('/api/approvals/:id/:verb', async ({ params, request }) => {
      decided.push({ id: String(params.id), verb: String(params.verb), body: await request.json() })
      return HttpResponse.json({})
    }),
  )
})

describe('approvals', () => {
  it('lists what is waiting by flow name, with Reject and Approve on the row', async () => {
    renderPage()
    const list = await screen.findByRole('list', { name: 'Approvals' })
    await within(list).findByText('Billing sync')
    const rows = within(list).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(requested[0]).toContain('status=pending')
    await userEvent.click(within(rows[0]).getByRole('button', { name: 'Approve' }))
    await vi.waitFor(() => expect(decided).toEqual([{ id: 'ap-1aaaaaaa', verb: 'approve', body: { notes: '', form_data: {} } }]))
  })

  it('sends an approval that needs answers to its review instead of approving blind', async () => {
    renderPage()
    const list = await screen.findByRole('list', { name: 'Approvals' })
    const row = (await within(list).findByText('Inventory alerts')).closest('li')!
    expect(within(row).queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button', { name: 'Review' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText(/Reason/)).toBeInTheDocument()
  })

  it('shows decided ones with who decided, under All', async () => {
    renderPage()
    await within(await screen.findByRole('list', { name: 'Approvals' })).findByText('Billing sync')
    await userEvent.click(screen.getByText('All'))
    await vi.waitFor(() => expect(requested.at(-1)).not.toContain('status='))
    const list = screen.getByRole('list', { name: 'Approvals' })
    await vi.waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(3))
    expect(within(list).getByText('Approved')).toBeInTheDocument()
  })

  it('says so when nothing is waiting', async () => {
    server.use(http.get('/api/approvals', () => HttpResponse.json({ data: [], total: 0 })))
    renderPage()
    expect(await screen.findByText('Nothing is waiting for approval')).toBeInTheDocument()
  })
})
