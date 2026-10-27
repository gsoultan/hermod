import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import {
  acceptUrl, decodeOrigin, encodeOrigin, formatPairingCode, PAIRING_MESSAGE, urlProblem,
} from '@/lib/environments'
import { EnvironmentsTab } from '@/pages/environments/EnvironmentsTab'
import { AcceptLinkPage } from '@/pages/environments/AcceptLinkPage'
import { PromoteDialog } from '@/pages/environments/PromoteDialog'
import { PromotionsPage } from '@/pages/environments/PromotionsPage'

let routeParams: Record<string, string> = {}
vi.mock('@tanstack/react-router', () => ({
  useParams: () => routeParams,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

const PRD = 'https://prod.example.com'

// The accept page closes its window once the code is sent; in jsdom that
// would tear down the document under every later test.
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

const link = {
  id: 'l-1', name: 'Production', url: PRD, role: 'production', created_by: 'root',
  created_at: '2026-10-27T10:00:00Z', reachable: true,
}

const matchRows = [
  { kind: 'source', id: 's-1', name: 'orders-db', type: 'postgres', vhost: 'default', target_id: 'p-1', target_name: 'orders-db', status: 'matched', manual: false, candidates: [{ id: 'p-1', name: 'orders-db', type: 'postgres', vhost: 'default' }] },
  { kind: 'source', id: 's-2', name: 'crm-stg', type: 'mysql', vhost: 'default', target_id: 'p-2', target_name: 'crm', status: 'suggested', manual: false, candidates: [{ id: 'p-2', name: 'crm', type: 'mysql', vhost: 'default' }, { id: 'p-3', name: 'crm-eu', type: 'mysql', vhost: 'default' }] },
  { kind: 'sink', id: 'k-1', name: 'lake', type: 's3', vhost: 'default', status: 'missing', manual: false, candidates: [] },
]

describe('environment helpers', () => {
  it('carries the sending origin through one path segment and back', () => {
    const seg = encodeOrigin('https://stg.example.com:8443')
    expect(seg).toMatch(/^[A-Za-z0-9_-]+$/)
    expect(decodeOrigin(seg)).toBe('https://stg.example.com:8443')
    expect(acceptUrl(`${PRD}/`, 'https://stg.example.com')).toBe(`${PRD}/environments/accept/${encodeOrigin('https://stg.example.com')}`)
  })

  it('refuses a segment that is not just an origin', () => {
    expect(decodeOrigin(encodeOrigin('https://stg.example.com/evil'))).toBeNull()
    expect(decodeOrigin(encodeOrigin('javascript:alert(1)'))).toBeNull()
    expect(decodeOrigin('%%%')).toBeNull()
  })

  it('says what is wrong with an address', () => {
    expect(urlProblem(PRD)).toBeNull()
    expect(urlProblem('prod.example.com')).toMatch(/not a web address/)
    expect(urlProblem('ftp://prod.example.com')).toMatch(/http/)
    expect(urlProblem('https://u:p@prod.example.com')).toMatch(/user/)
  })

  it('formats a pairing code as it is typed', () => {
    expect(formatPairingCode('abcd efgh')).toBe('ABCD-EFGH')
    expect(formatPairingCode('ab')).toBe('AB')
    expect(formatPairingCode('ABCD-EFGH-IJ')).toBe('ABCD-EFGH')
  })
})

describe('Settings › Environments', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  it('on staging, links production with a pairing code and saves a connection match', async () => {
    let linkBody: Record<string, string> | null = null
    let mapping: unknown = null
    let links: unknown[] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json(links)),
      http.post('/api/environments/links', async ({ request }) => {
        linkBody = (await request.json()) as Record<string, string>
        links = [link]
        return HttpResponse.json(link, { status: 201 })
      }),
      http.get('/api/environments/links/l-1/matching', () => HttpResponse.json(matchRows)),
      http.put('/api/environments/links/l-1/mapping', async ({ request }) => {
        mapping = await request.json()
        return HttpResponse.json(link)
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    expect(await screen.findByText(/Not linked to anything yet/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Link an environment' }))
    await user.type(await screen.findByLabelText('Address'), `${PRD}/`)
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByRole('button', { name: 'Sign in to prod.example.com and approve' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('Pairing code'), 'abcdefgh')
    expect(screen.getByLabelText('Pairing code')).toHaveValue('ABCD-EFGH')
    await user.click(screen.getByRole('button', { name: 'Link' }))

    expect(await screen.findByText('Linked to Production')).toBeInTheDocument()
    expect(linkBody).toEqual({ url: PRD, code: 'ABCD-EFGH', self_url: window.location.origin })

    const table = await screen.findByRole('table', { name: 'Connections matched in Production' })
    expect(within(table).getByText('Missing')).toBeInTheDocument()
    expect(within(table).getByText('Check this')).toBeInTheDocument()
    expect(within(table).getByRole('link', { name: /Create it in Production/ })).toHaveAttribute('href', `${PRD}/sinks/new`)
    expect(screen.getByText(/1 of 3/)).toBeInTheDocument()

    await user.click(within(table).getByLabelText('Match for crm-stg'))
    await user.click(await screen.findByRole('option', { name: /crm-eu/, hidden: true }))
    await user.click(screen.getByRole('button', { name: 'Save matches' }))
    await waitFor(() => expect(mapping).toEqual({ mapping: { 's-2': 'p-3' } }))
  }, 20000)

  it('takes the code from the production sign-in window, and only from production', async () => {
    const linkBodies: unknown[] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json([])),
      http.post('/api/environments/links', async ({ request }) => {
        linkBodies.push(await request.json())
        return HttpResponse.json(link, { status: 201 })
      }),
      http.get('/api/environments/links/l-1/matching', () => HttpResponse.json([])),
    )
    const opened: string[] = []
    const open = vi.spyOn(window, 'open').mockImplementation((u) => { opened.push(String(u)); return null })
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    await user.click(await screen.findByRole('button', { name: 'Link an environment' }))
    await user.type(await screen.findByLabelText('Address'), PRD)
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.click(screen.getByRole('button', { name: /Sign in to prod.example.com/ }))
    expect(opened).toEqual([acceptUrl(PRD, window.location.origin)])

    fireEvent(window, new MessageEvent('message', { origin: 'https://evil.example.com', data: { type: PAIRING_MESSAGE, code: 'EVIL-CODE' } }))
    fireEvent(window, new MessageEvent('message', { origin: PRD, data: { type: PAIRING_MESSAGE, code: 'WXYZ2345' } }))

    expect(await screen.findByText('Linked to Production')).toBeInTheDocument()
    expect(linkBodies).toEqual([{ url: PRD, code: 'WXYZ-2345', self_url: window.location.origin }])
    open.mockRestore()
  }, 20000)

  it('on production, shows the rules and issues a pairing code instead of links', async () => {
    let rules: unknown = null
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod EU', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.put('/api/environments/rules', async ({ request }) => {
        rules = await request.json()
        return HttpResponse.json(rules as Record<string, unknown>)
      }),
      http.get('/api/environments/peers', () => HttpResponse.json([{ id: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-10-27T10:00:00Z' }])),
      http.post('/api/environments/pairing-codes', () => HttpResponse.json({ code: 'QRST-5678', expires_at: '2026-10-27T10:10:00Z' }, { status: 201 })),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    expect(await screen.findByText('Receives from')).toBeInTheDocument()
    expect(screen.queryByText('Promotes to')).not.toBeInTheDocument()
    expect(await screen.findByText('https://stg.example.com', { exact: false })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Create pairing code' }))
    expect(await screen.findByText('QRST-5678')).toBeInTheDocument()

    await user.click(screen.getByLabelText('Minimum time in the sending environment'))
    await user.clear(screen.getByLabelText('Minimum time in the sending environment'))
    await user.type(screen.getByLabelText('Minimum time in the sending environment'), '60')
    await user.click(screen.getByRole('button', { name: 'Save rules' }))
    await waitFor(() => expect(rules).toEqual({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 60 }))
  }, 20000)
})

describe('the production accept page', () => {
  afterEach(() => { routeParams = {} })

  it('sends the code to the origin it shows, and nowhere else', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com') }
    server.use(http.post('/api/environments/pairing-codes', () => HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-10-27T10:10:00Z' }, { status: 201 })))
    const postMessage = vi.fn()
    const opener = Object.getOwnPropertyDescriptor(window, 'opener')
    Object.defineProperty(window, 'opener', { configurable: true, value: { postMessage } })
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)

    expect(screen.getByText('https://stg.example.com')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: PAIRING_MESSAGE, code: 'ABCD-2345' }, 'https://stg.example.com'))

    if (opener) Object.defineProperty(window, 'opener', opener)
  })

  it('does not issue a code for a viewer', () => {
    signInAs('Viewer', 'vic')
    routeParams = { from: encodeOrigin('https://stg.example.com') }
    wrap(<AcceptLinkPage />)
    expect(screen.getByText('An administrator has to approve this')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Allow' })).not.toBeInTheDocument()
  })
})

const preview = (overrides: Record<string, unknown> = {}) => ({
  link_id: 'l-1', link_name: 'Production', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'default',
  from_version: 3, current_version: 2,
  checks: [
    { name: 'Workflow is valid', ok: true, blocking: true },
    { name: 'Connections matched', ok: true, blocking: true },
    { name: 'Secrets exist', ok: false, blocking: false, detail: 'CRM_KEY is not set in Production' },
  ],
  changes: [{ kind: 'changed', node_id: 'n2', what: 'Node n2: batch_size' }],
  rules: { approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 },
  ready: true,
  ...overrides,
})

describe('the Promote dialog', () => {
  beforeEach(() => signInAs('Editor', 'ada'))

  it('sends for approval with the note and options', async () => {
    let body: unknown = null
    server.use(
      http.get('/api/environments/links', () => HttpResponse.json([link])),
      http.post('/api/environments/links/l-1/preview', () => HttpResponse.json(preview())),
      http.post('/api/environments/links/l-1/promotions', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 'pr-1', status: 'pending' }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<PromoteDialog workflowId="wf-1" workflowName="orders" opened onClose={() => {}} versions={[{ version: 3 }]} />)

    expect(await screen.findByText('CRM_KEY is not set in Production')).toBeInTheDocument()
    expect(screen.getByText('Node n2: batch_size')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Note for the approver'), 'bigger batches')
    await user.click(screen.getByRole('button', { name: 'Send for approval' }))

    expect(await screen.findByText('Sent to Production')).toBeInTheDocument()
    expect(body).toEqual({ workflow_id: 'wf-1', version: 0, note: 'bigger batches', start: true, auto_rollback: true })
  })

  it('keeps the button off while a blocking check fails', async () => {
    server.use(
      http.get('/api/environments/links', () => HttpResponse.json([link])),
      http.post('/api/environments/links/l-1/preview', () => HttpResponse.json(preview({
        ready: false,
        checks: [{ name: 'Connections matched', ok: false, blocking: true, detail: 'lake has no match in Production' }],
      }))),
    )
    wrap(<PromoteDialog workflowId="wf-1" workflowName="orders" opened onClose={() => {}} />)
    expect(await screen.findByText('lake has no match in Production')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send for approval' })).toBeDisabled()
  })

  it('says Deploy when production needs no approval', async () => {
    server.use(
      http.get('/api/environments/links', () => HttpResponse.json([link])),
      http.post('/api/environments/links/l-1/preview', () => HttpResponse.json(preview({ rules: { approval: 'none', auto_rollback: false, min_soak_minutes: 0 } }))),
    )
    wrap(<PromoteDialog workflowId="wf-1" workflowName="orders" opened onClose={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Deploy to Production' })).toBeEnabled()
  })
})

const promotion = (overrides: Record<string, unknown> = {}) => ({
  id: 'pr-1', peer_id: 'p-1', from_env: 'Staging', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'default',
  from_version: 3, sender: 'ada', note: 'bigger batches', start: true, auto_rollback: true,
  changes: [{ kind: 'changed', what: 'Node n2: batch_size' }], status: 'pending', created_at: '2026-10-27T10:00:00Z',
  ...overrides,
})

describe('Promotions on production', () => {
  beforeEach(() => {
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
    )
  })

  it('approves a pending promotion after confirming', async () => {
    signInAs('Administrator', 'root')
    let status = 'pending'
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([promotion({ status })])),
      http.post('/api/environments/promotions/pr-1/approve', () => {
        status = 'deployed'
        return HttpResponse.json(promotion({ status }))
      }),
    )
    const user = userEvent.setup()
    wrap(<PromotionsPage />)

    expect(await screen.findByText('“bigger batches”')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Approve and deploy' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Approve and deploy' }))
    expect(await screen.findByText('Deployed')).toBeInTheDocument()
  })

  it('does not let the sender approve their own promotion', async () => {
    signInAs('Administrator', 'ada')
    server.use(http.get('/api/environments/promotions', () => HttpResponse.json([promotion()])))
    wrap(<PromotionsPage />)
    expect(await screen.findByText(/another administrator approves it/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Approve and deploy' })).toBeDisabled()
  })

  it('offers rollback only on the newest deploy of a workflow', async () => {
    signInAs('Administrator', 'root')
    server.use(http.get('/api/environments/promotions', () => HttpResponse.json([
      promotion({ id: 'pr-2', status: 'deployed', previous_version: 4, applied_version: 5 }),
      promotion({ id: 'pr-1', status: 'deployed', previous_version: 3, applied_version: 4 }),
    ])))
    wrap(<PromotionsPage />)
    await screen.findAllByText('Deployed')
    expect(screen.getAllByRole('button', { name: 'Roll back' })).toHaveLength(1)
  })
})
