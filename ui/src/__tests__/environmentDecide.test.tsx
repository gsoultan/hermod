import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { encodeOrigin } from '@/lib/environments'
import { EnvironmentsPage } from '@/pages/environments/EnvironmentsPage'
import { EnvironmentsTab } from '@/pages/environments/EnvironmentsTab'
import { AcceptLinkPage } from '@/pages/environments/AcceptLinkPage'

let routeParams: Record<string, string> = {}
vi.mock('@tanstack/react-router', () => ({
  useParams: () => routeParams,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

beforeAll(() => { vi.spyOn(window, 'close').mockImplementation(() => {}) })

function wrap(ui: ReactNode) {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>{ui}</ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

const link = (decide: boolean) => ({
  id: 'l-1', name: 'Production', url: 'https://prod.example.com', role: 'production', created_by: 'root',
  created_at: '2026-10-27T10:00:00Z', reachable: true, vhost: 'hr', target_vhost: 'hr', decide,
})

const sent = (overrides: Record<string, unknown> = {}) => ({
  id: 'pr-1', peer_id: 'l-1', from_env: 'Production', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
  from_version: 3, sender: 'ada', start: true, auto_rollback: false, status: 'pending', created_at: '2026-10-27T10:00:00Z',
  ...overrides,
})

function staging(decide: boolean, log: unknown[]) {
  server.use(
    http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'development' })),
    http.get('/api/environments/ready', () => HttpResponse.json([])),
    http.get('/api/environments/links', () => HttpResponse.json([link(decide)])),
    http.get('/api/environments/sent', () => HttpResponse.json(log)),
  )
}

describe('deciding from the sending side', () => {
  it('approves a waiting release in production from here', async () => {
    signInAs('Administrator', 'rina')
    let status = 'pending'
    const approved: string[] = []
    staging(true, [])
    server.use(
      http.get('/api/environments/sent', () => HttpResponse.json([
        sent({ id: 'pr-2', release_id: 'r-1', workflow_id: 'wf-2', workflow_name: 'leave', status }),
        sent({ id: 'pr-1', release_id: 'r-1', status }),
      ])),
      http.post('/api/environments/sent/:id/approve', ({ params }) => {
        approved.push(String(params.id))
        status = 'deployed'
        return HttpResponse.json(sent({ status, decided_by: 'rina (via Staging)' }))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(await screen.findByText('Needs your approval')).toBeInTheDocument()
    expect(screen.getByText('2 workflows to Production')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Approve 2 workflows' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/leave and orders go live in Production now/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Approve and go live' }))
    await waitFor(() => expect(approved).toEqual(['pr-2']))
    expect(await screen.findByText('Recently sent')).toBeInTheDocument()
    expect(screen.queryByText('Needs your approval')).not.toBeInTheDocument()
  })

  it('keeps a flow with an empty secret stopped unless the approver here starts it anyway', async () => {
    signInAs('Administrator', 'rina')
    const bodies: string[] = []
    staging(true, [sent({ secrets: ['CRM_KEY', 'SMTP_PASSWORD'], secrets_filled: ['SMTP_PASSWORD'] })])
    server.use(http.post('/api/environments/sent/:id/approve', async ({ request }) => {
      bodies.push(await request.text())
      return HttpResponse.json(sent({ status: 'deployed' }))
    }))
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(await screen.findByRole('radio', { name: /Keep it stopped until CRM_KEY has a value/ })).toBeChecked()
    await user.click(screen.getByRole('radio', { name: /Start it anyway/ }))
    await user.click(screen.getByRole('button', { name: 'Approve orders' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText(/orders goes live in Production now and starts/)).toBeInTheDocument()
    await user.click(dialog.getByRole('button', { name: 'Approve and go live' }))
    await waitFor(() => expect(bodies).toEqual([JSON.stringify({ start_anyway: true })]))
  })

  it('rejects with a reason', async () => {
    signInAs('Administrator', 'rina')
    let body: unknown = null
    staging(true, [sent()])
    server.use(http.post('/api/environments/sent/:id/reject', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json(sent({ status: 'rejected' }))
    }))
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    await user.click(await screen.findByRole('button', { name: 'Reject orders' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Why'), 'not this week')
    await user.click(within(dialog).getByRole('button', { name: 'Reject' }))
    await waitFor(() => expect(body).toEqual({ reason: 'not this week' }))
  })

  it('only offers it where production allows it, and only to administrators', async () => {
    signInAs('Administrator', 'rina')
    staging(false, [sent(), sent({ id: 'pr-0', workflow_id: 'wf-9', workflow_name: 'payroll', status: 'deployed' })])
    const { unmount } = wrap(<EnvironmentsPage />)
    expect(await screen.findByText('Waiting for approval')).toBeInTheDocument()
    expect(screen.queryByText('Needs your approval')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Approve/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Undo/ })).not.toBeInTheDocument()
    unmount()

    signInAs('Editor', 'ada')
    staging(true, [sent()])
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText('Waiting for approval')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Approve/ })).not.toBeInTheDocument()
  })

  it('undoes in production only the newest deploy of a workflow', async () => {
    signInAs('Administrator', 'rina')
    const undone: string[] = []
    staging(true, [
      sent({ id: 'pr-3', status: 'deployed', decided_by: 'rina (via Staging)' }),
      sent({ id: 'pr-1', status: 'deployed', workflow_name: 'orders (older)' }),
    ])
    server.use(http.post('/api/environments/sent/:id/rollback', ({ params }) => {
      undone.push(String(params.id))
      return HttpResponse.json(sent({ status: 'rolled_back' }))
    }))
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    const buttons = await screen.findAllByRole('button', { name: /^Undo/ })
    expect(buttons).toHaveLength(1)
    expect(screen.getByText(/rina \(via Staging\)/)).toBeInTheDocument()
    await user.click(buttons[0])
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/back to how it was in Production/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(undone).toEqual(['pr-3']))
  })
})

describe('allowing it on the receiving side', () => {
  afterEach(() => { routeParams = {} })

  it('lets the linked instance decide unless the administrator unticks it', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    const bodies: unknown[] = []
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'hr' }] })),
      http.post('/api/environments/pairing-codes', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-10-27T10:10:00Z', vhost: 'hr' }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    const { unmount } = wrap(<AcceptLinkPage />)
    const box = await screen.findByRole('checkbox', { name: /approve, reject and undo what it sends/ })
    expect(box).toBeChecked()
    await user.click(await screen.findByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    unmount()

    wrap(<AcceptLinkPage />)
    await user.click(await screen.findByRole('checkbox', { name: /approve, reject and undo what it sends/ }))
    await user.click(await screen.findByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies).toEqual([
      { label: 'https://stg.example.com', vhost: 'hr', add_vhosts: true, decide: true },
      { label: 'https://stg.example.com', vhost: 'hr', add_vhosts: true },
    ])
  })

  it('switches it for a linked instance', async () => {
    signInAs('Administrator', 'root')
    let decide = false
    const patched: unknown[] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'hr' }] })),
      http.get('/api/environments/peers', () => HttpResponse.json([
        { id: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-10-27T10:00:00Z', vhost: 'hr', decide },
      ])),
      http.patch('/api/environments/peers/:id', async ({ request }) => {
        patched.push(await request.json())
        decide = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)
    const toggle = await screen.findByRole('switch', { name: /Let Staging's administrators approve/ })
    expect(toggle).not.toBeChecked()
    await user.click(toggle)
    await waitFor(() => expect(patched).toEqual([{ decide: true }]))
    await waitFor(() => expect(toggle).toBeChecked())
  })
})
