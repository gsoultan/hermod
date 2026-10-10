import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ProposalsPanel } from '@/pages/workflows/proposals/ProposalsPanel'

const WF = 'wf-1'

function proposal(overrides: Record<string, unknown> = {}) {
  return {
    id: 'p-1',
    workflow_id: WF,
    kind: 'retry_policy',
    node_id: 'snk',
    title: 'Retry the warehouse sink longer',
    reason: 'The sink timed out 14 times in the last hour.',
    patch: [
      { op: 'replace', path: '/max_retries', before: 3, after: 6 },
      { op: 'replace', path: '/retry_interval', before: '100ms', after: '2s' },
    ],
    status: 'pending',
    occurrences: 14,
    created_at: '2026-10-09T10:00:00Z',
    last_seen_at: '2026-10-09T11:00:00Z',
    ...overrides,
  }
}

function renderPanel(canDecide = true) {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>
          <ProposalsPanel workflowId={WF} canDecide={canDecide} />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Self-healing proposals', () => {
  it('shows a pending proposal with its reason and a before/after diff', async () => {
    server.use(http.get(`/api/workflows/${WF}/proposals`, () => HttpResponse.json({ data: [proposal()] })))
    renderPanel()

    expect(await screen.findByText('Retry the warehouse sink longer')).toBeInTheDocument()
    expect(screen.getByText(/timed out 14 times/)).toBeInTheDocument()
    const diff = screen.getByRole('table', { name: /changes proposed by retry the warehouse sink longer/i })
    const retries = within(diff).getByText('/max_retries').closest('tr') as HTMLElement
    expect(within(retries).getByText('3')).toBeInTheDocument()
    expect(within(retries).getByText('6')).toBeInTheDocument()
    const interval = within(diff).getByText('/retry_interval').closest('tr') as HTMLElement
    expect(within(interval).getByText('"100ms"')).toBeInTheDocument()
    expect(within(interval).getByText('"2s"')).toBeInTheDocument()
  })

  it('approves a proposal and shows it applied', async () => {
    let status = 'pending'
    let approvals = 0
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () =>
        HttpResponse.json({ data: [proposal(status === 'applied' ? { status, previous_version: 4, applied_version: 5, decided_by: 'ana' } : {})] }),
      ),
      http.post(`/api/workflows/${WF}/proposals/p-1/approve`, () => {
        approvals += 1
        status = 'applied'
        return HttpResponse.json(proposal({ status: 'applied', previous_version: 4, applied_version: 5 }))
      }),
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: /^approve/i }))
    await user.click(await screen.findByRole('button', { name: 'Apply fix' }))
    await waitFor(() => expect(approvals).toBe(1))
    expect(await screen.findByText(/applied as version 5/i)).toBeInTheDocument()
  })

  it('rejects a proposal', async () => {
    let rejected = 0
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () => HttpResponse.json({ data: [proposal(rejected ? { status: 'rejected' } : {})] })),
      http.post(`/api/workflows/${WF}/proposals/p-1/reject`, () => {
        rejected += 1
        return HttpResponse.json(proposal({ status: 'rejected' }))
      }),
    )
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: /^reject/i }))
    await waitFor(() => expect(rejected).toBe(1))
    expect(await screen.findByText('Rejected')).toBeInTheDocument()
  })

  it('explains a proposal that went stale (409)', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () => HttpResponse.json({ data: [proposal()] })),
      http.post(`/api/workflows/${WF}/proposals/p-1/approve`, () =>
        HttpResponse.json({ error: 'the workflow changed since this fix was proposed; it was not applied' }, { status: 409 }),
      ),
    )
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: /^approve/i }))
    await user.click(await screen.findByRole('button', { name: 'Apply fix' }))
    expect(await screen.findByText(/no longer be applied/i)).toBeInTheDocument()
    expect(screen.getByText(/workflow changed since this fix was proposed/)).toBeInTheDocument()
  })

  it('explains a fix the workflow validator refused (422)', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () => HttpResponse.json({ data: [proposal()] })),
      http.post(`/api/workflows/${WF}/proposals/p-1/approve`, () =>
        HttpResponse.json({ error: 'invalid workflow: retry interval too long' }, { status: 422 }),
      ),
    )
    const user = userEvent.setup()
    renderPanel()
    await user.click(await screen.findByRole('button', { name: /^approve/i }))
    await user.click(await screen.findByRole('button', { name: 'Apply fix' }))
    expect(await screen.findByText(/validation rejected this fix/i)).toBeInTheDocument()
    expect(screen.getByText(/retry interval too long/)).toBeInTheDocument()
  })

  it('offers no decisions to a Viewer', async () => {
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () =>
        HttpResponse.json({ data: [proposal(), proposal({ id: 'p-2', status: 'applied', previous_version: 2, applied_version: 3 })] }),
      ),
    )
    renderPanel(false)
    await screen.findAllByText('Retry the warehouse sink longer')
    expect(screen.queryByRole('button', { name: /^approve/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /^reject/i })).toBeNull()
    expect(screen.queryByRole('button', { name: /roll back/i })).toBeNull()
  })

  it('rolls an applied fix back to the version before it, after confirmation', async () => {
    const rollbacks: string[] = []
    server.use(
      http.get(`/api/workflows/${WF}/proposals`, () =>
        HttpResponse.json({ data: [proposal({ status: 'applied', previous_version: 4, applied_version: 5 })] }),
      ),
      http.post(`/api/workflows/${WF}/rollback/:version`, ({ params }) => {
        rollbacks.push(String(params.version))
        return HttpResponse.json({ ok: true })
      }),
    )
    const user = userEvent.setup()
    renderPanel()

    await user.click(await screen.findByRole('button', { name: /roll back/i }))
    expect(await screen.findByText(/version 4/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await new Promise((r) => setTimeout(r, 30))
    expect(rollbacks).toEqual([])

    await user.click(screen.getByRole('button', { name: /roll back/i }))
    await user.click(await screen.findByRole('button', { name: 'Roll back' }))
    await waitFor(() => expect(rollbacks).toEqual(['4']))
  })

  it('says when there is nothing to review', async () => {
    server.use(http.get(`/api/workflows/${WF}/proposals`, () => HttpResponse.json({ data: [] })))
    renderPanel()
    expect(await screen.findByText(/no self-healing proposals/i)).toBeInTheDocument()
  })
})
