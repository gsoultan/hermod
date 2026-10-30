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
import { ReleaseDialog } from '@/pages/environments/ReleaseDialog'
import { EnvironmentsPage } from '@/pages/environments/EnvironmentsPage'

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
  created_at: '2026-10-27T10:00:00Z', reachable: true, vhost: 'hr', target_vhost: 'hr',
}

const vhosts = (...names: string[]) => http.get('/api/vhosts', () => HttpResponse.json({ data: names.map((name) => ({ name })) }))

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
    expect(acceptUrl(`${PRD}/`, 'https://stg.example.com', 'hr')).toBe(`${PRD}/environments/accept/${encodeOrigin('https://stg.example.com')}/hr`)
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
      vhosts('default', 'hr'),
      http.put('/api/environments/links/l-1/mapping', async ({ request }) => {
        mapping = await request.json()
        return HttpResponse.json(link)
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    expect(await screen.findByText(/Not linked to anything yet/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Connect an environment' }))
    await user.type(await screen.findByLabelText('Address'), `${PRD}/`)
    await user.type(screen.getByRole('combobox', { name: 'Workflows from' }), 'hr{ArrowDown}{Enter}')
    expect(screen.getByLabelText('Land in')).toHaveValue('hr')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByRole('button', { name: 'Sign in to prod.example.com and approve' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('Pairing code'), 'abcdefgh')
    expect(screen.getByLabelText('Pairing code')).toHaveValue('ABCD-EFGH')
    await user.click(screen.getByRole('button', { name: 'Link' }))

    expect(await screen.findByText('Connected to Production')).toBeInTheDocument()
    expect(linkBody).toEqual({ url: PRD, code: 'ABCD-EFGH', self_url: window.location.origin, vhost: 'hr' })

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
      vhosts('default', 'hr'),
    )
    const opened: string[] = []
    const open = vi.spyOn(window, 'open').mockImplementation((u) => { opened.push(String(u)); return null })
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    await user.click(await screen.findByRole('button', { name: 'Connect an environment' }))
    await user.type(await screen.findByLabelText('Address'), PRD)
    await user.type(screen.getByRole('combobox', { name: 'Workflows from' }), 'hr{ArrowDown}{Enter}')
    await user.clear(screen.getByLabelText('Land in'))
    await user.type(screen.getByLabelText('Land in'), 'people')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.click(screen.getByRole('button', { name: /Sign in to prod.example.com/ }))
    expect(opened).toEqual([acceptUrl(PRD, window.location.origin, 'people')])

    fireEvent(window, new MessageEvent('message', { origin: 'https://evil.example.com', data: { type: PAIRING_MESSAGE, code: 'EVIL-CODE' } }))
    fireEvent(window, new MessageEvent('message', { origin: PRD, data: { type: PAIRING_MESSAGE, code: 'WXYZ2345' } }))

    expect(await screen.findByText('Connected to Production')).toBeInTheDocument()
    expect(linkBodies).toEqual([{ url: PRD, code: 'WXYZ-2345', self_url: window.location.origin, vhost: 'hr' }])
    open.mockRestore()
  }, 20000)

  it('shows each link with the vhost it sends from and lands in', async () => {
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/links', () => HttpResponse.json([
        { ...link, vhost: 'training', target_vhost: 'training', vhost_created: true },
        { ...link, id: 'l-0', vhost: '', target_vhost: '' },
      ])),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      vhosts('default', 'training'),
    )
    wrap(<EnvironmentsTab />)
    expect(await screen.findByText('training → training')).toBeInTheDocument()
    expect(screen.getByText('Created there by this link')).toBeInTheDocument()
    expect(screen.getByText('Every vhost (older link)')).toBeInTheDocument()
  })

  it('on production, shows the rules and issues a pairing code instead of links', async () => {
    let rules: unknown = null
    let codeBody: unknown = null
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod EU', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.put('/api/environments/rules', async ({ request }) => {
        rules = await request.json()
        return HttpResponse.json(rules as Record<string, unknown>)
      }),
      http.get('/api/environments/peers', () => HttpResponse.json([{ id: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-10-27T10:00:00Z', vhost: 'hr' }])),
      http.post('/api/environments/pairing-codes', async ({ request }) => {
        codeBody = await request.json()
        return HttpResponse.json({ code: 'QRST-5678', expires_at: '2026-10-27T10:10:00Z', vhost: 'hr' }, { status: 201 })
      }),
      vhosts('default', 'hr'),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    expect(await screen.findByText('Receives from')).toBeInTheDocument()
    expect(screen.queryByText('Promotes to')).not.toBeInTheDocument()
    expect(await screen.findByText('https://stg.example.com', { exact: false })).toBeInTheDocument()
    expect(screen.getByText('into hr')).toBeInTheDocument()

    expect(screen.getByRole('button', { name: 'Create pairing code' })).toBeDisabled()
    await user.type(screen.getByRole('combobox', { name: 'Into virtual host' }), 'hr')
    await user.click(screen.getByRole('button', { name: 'Create pairing code' }))
    expect(await screen.findByText('QRST-5678')).toBeInTheDocument()
    expect(codeBody).toEqual({ label: 'Created in Settings', vhost: 'hr' })

    await user.click(screen.getByLabelText('Minimum time in the sending environment'))
    await user.clear(screen.getByLabelText('Minimum time in the sending environment'))
    await user.type(screen.getByLabelText('Minimum time in the sending environment'), '60')
    await user.click(screen.getByRole('button', { name: 'Save rules' }))
    await waitFor(() => expect(rules).toEqual({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 60 }))
  }, 20000)

  it('gives one vhost rules of its own', async () => {
    let rules: unknown = null
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.put('/api/environments/rules', async ({ request }) => {
        rules = await request.json()
        return HttpResponse.json(rules as Record<string, unknown>)
      }),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      vhosts('default', 'payroll'),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    await user.type(await screen.findByRole('combobox', { name: 'Vhost with its own rules' }), 'payroll')
    await user.click(screen.getByRole('button', { name: 'Add vhost rules' }))
    const soak = screen.getByLabelText('payroll: minimum time in the sending environment')
    await user.clear(soak)
    await user.type(soak, '1440')
    await user.click(screen.getByRole('button', { name: 'Save rules' }))
    await waitFor(() => expect(rules).toEqual({
      approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0,
      by_vhost: { payroll: { approval: 'admin_except_sender', min_soak_minutes: 1440 } },
    }))
  }, 20000)
})

describe('the production accept page', () => {
  afterEach(() => { routeParams = {} })

  it('sends the code to the origin it shows, and nowhere else', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    let body: unknown = null
    server.use(
      vhosts('default', 'hr'),
      http.post('/api/environments/pairing-codes', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-10-27T10:10:00Z', vhost: 'hr' }, { status: 201 })
      }),
    )
    const postMessage = vi.fn()
    const opener = Object.getOwnPropertyDescriptor(window, 'opener')
    Object.defineProperty(window, 'opener', { configurable: true, value: { postMessage } })
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)

    expect(screen.getByText('https://stg.example.com')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(postMessage).toHaveBeenCalledWith({ type: PAIRING_MESSAGE, code: 'ABCD-2345' }, 'https://stg.example.com'))
    expect(body).toEqual({ label: 'https://stg.example.com', vhost: 'hr' })

    if (opener) Object.defineProperty(window, 'opener', opener)
  })

  it('creates a vhost that is not here yet when it allows the link', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'training' }
    server.use(vhosts('default', 'hr'))
    wrap(<AcceptLinkPage />)
    expect(await screen.findByRole('button', { name: 'Create training and allow' })).toBeInTheDocument()
    expect(screen.getByText(/training is not here yet/)).toBeInTheDocument()
  })

  it('refuses a vhost name the server would not take', () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: '../etc' }
    wrap(<AcceptLinkPage />)
    expect(screen.getByText('This link is broken')).toBeInTheDocument()
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
  link_id: 'l-1', link_name: 'Production', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
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

type Item = { workflow_id: string; version: number }

// What production answers for a release of these items: orders and leave go to
// HR, campaigns to Marketing, and leave cannot land until its sink is matched.
function releasePreview(items: Item[]) {
  const has = (id: string) => items.some((i) => i.workflow_id === id)
  const hr = [
    ...(has('wf-1') ? [preview()] : []),
    ...(has('wf-2') ? [preview({
      workflow_id: 'wf-2', workflow_name: 'leave', ready: false,
      checks: [{ name: 'Connections matched', ok: false, blocking: true, detail: 'lake has no match in Production' }],
    })] : []),
  ]
  const mkt = has('wf-3') ? [preview({ link_id: 'l-2', workflow_id: 'wf-3', workflow_name: 'campaigns', vhost: 'marketing' })] : []
  const groups = [
    ...(hr.length ? [{ link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', previews: hr, ready: hr.every((p) => p.ready) }] : []),
    ...(mkt.length ? [{ link_id: 'l-2', link_name: 'Production', vhost: 'marketing', target_vhost: 'mkt', previews: mkt, ready: true }] : []),
  ]
  const unlinked = has('wf-9') ? [{ workflow_id: 'wf-9', workflow_name: 'payroll', vhost: 'payroll' }] : []
  return { groups, unlinked, ready: unlinked.length === 0 && groups.every((g) => g.ready) }
}

describe('the release dialog', () => {
  beforeEach(() => signInAs('Editor', 'ada'))

  it('leaves out what cannot land and sends the rest as one release', async () => {
    const previews: Item[][] = []
    let body: unknown = null
    server.use(
      http.post('/api/environments/releases/preview', async ({ request }) => {
        const { items } = (await request.json()) as { items: Item[] }
        previews.push(items)
        return HttpResponse.json(releasePreview(items))
      }),
      http.post('/api/environments/releases', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json([
          { link_id: 'l-1', link_name: 'Production', release_id: 'r-1', promotions: [{ id: 'pr-1', status: 'pending' }] },
          { link_id: 'l-2', link_name: 'Production', release_id: 'r-1', promotions: [{ id: 'pr-2', status: 'deployed' }] },
        ], { status: 201 })
      }),
    )
    const user = userEvent.setup()
    const items = ['wf-1', 'wf-2', 'wf-3'].map((id) => ({ workflow_id: id, version: 0 }))
    wrap(<ReleaseDialog opened onClose={() => {}} items={items} />)

    expect(await screen.findByText('lake has no match in Production')).toBeInTheDocument()
    expect(screen.getByText('Production · hr')).toBeInTheDocument()
    expect(screen.getByText('Production · mkt')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Promote/ })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Leave out leave' }))
    const send = await screen.findByRole('button', { name: 'Promote 2 workflows to 2 environments' })
    await waitFor(() => expect(send).toBeEnabled())
    expect(previews.at(-1)).toEqual([{ workflow_id: 'wf-1', version: 0 }, { workflow_id: 'wf-3', version: 0 }])

    await user.type(screen.getByLabelText('Note for the approver'), 'new HRIS fields')
    await user.click(send)
    expect(await screen.findByText(/waits for approval/)).toBeInTheDocument()
    expect(screen.getByText(/is live/)).toBeInTheDocument()
    expect(body).toEqual({
      items: [{ workflow_id: 'wf-1', version: 0 }, { workflow_id: 'wf-3', version: 0 }],
      note: 'new HRIS fields', start: true, auto_rollback: true,
    })
  }, 20000)

  it('names a vhost no environment is connected for', async () => {
    server.use(http.post('/api/environments/releases/preview', async ({ request }) => {
      const { items } = (await request.json()) as { items: Item[] }
      return HttpResponse.json(releasePreview(items))
    }))
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-9', version: 0 }]} />)
    expect(await screen.findByText(/Nothing is connected for payroll/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Leave out payroll' })).toBeInTheDocument()
  })

  it('lets one workflow pick its version', async () => {
    const previews: Item[][] = []
    server.use(http.post('/api/environments/releases/preview', async ({ request }) => {
      const { items } = (await request.json()) as { items: Item[] }
      previews.push(items)
      return HttpResponse.json(releasePreview(items))
    }))
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} versions={[{ version: 3 }]} />)
    await screen.findByText('Node n2: batch_size')
    await user.click(screen.getByRole('combobox', { name: 'Version' }))
    await user.click(await screen.findByRole('option', { name: 'v3', hidden: true }))
    await waitFor(() => expect(previews.at(-1)).toEqual([{ workflow_id: 'wf-1', version: 3 }]))
    expect(screen.getByRole('button', { name: 'Promote 1 workflow to 1 environment' })).toBeInTheDocument()
  })
})

describe('the Environments page on staging', () => {
  beforeEach(() => {
    signInAs('Editor', 'ada')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/sent', () => HttpResponse.json([])),
    )
  })

  it('shows a lane per vhost and promotes everything ready at once', async () => {
    const previews: Item[][] = []
    server.use(
      http.get('/api/environments/ready', () => HttpResponse.json([
        { link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', workflows: [
          { id: 'wf-1', name: 'orders', vhost: 'hr', version: 3, last_sent_version: 2, active: true },
          { id: 'wf-2', name: 'leave', vhost: 'hr', version: 1, last_sent_version: 0, active: true },
        ] },
        { link_id: 'l-2', link_name: 'Production', vhost: 'marketing', target_vhost: 'mkt', workflows: [
          { id: 'wf-3', name: 'campaigns', vhost: 'marketing', version: 7, last_sent_version: 6, active: true },
        ] },
        { link_id: 'l-3', link_name: 'Production', vhost: 'finance', target_vhost: 'finance', workflows: [] },
      ])),
      http.post('/api/environments/releases/preview', async ({ request }) => {
        const { items } = (await request.json()) as { items: Item[] }
        previews.push(items)
        return HttpResponse.json(releasePreview(items))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(await screen.findByText('hr → Production · hr')).toBeInTheDocument()
    expect(screen.getByText('marketing → Production · mkt')).toBeInTheDocument()
    expect(screen.getByText('v2 → v3')).toBeInTheDocument()
    expect(screen.getByText('New')).toBeInTheDocument()
    expect(screen.getByText('Up to date')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Promote all ready (3)' }))
    await waitFor(() => expect(previews[0]).toEqual([
      { workflow_id: 'wf-1', version: 0 }, { workflow_id: 'wf-2', version: 0 }, { workflow_id: 'wf-3', version: 0 },
    ]))
  }, 20000)

  it('shows the status of each release it sent', async () => {
    server.use(
      http.get('/api/environments/ready', () => HttpResponse.json([])),
      http.get('/api/environments/sent', () => HttpResponse.json([
        promotion({ id: 'a', release_id: 'r-1', from_env: 'Production', workflow_name: 'orders', status: 'deployed' }),
        promotion({ id: 'b', release_id: 'r-1', from_env: 'Production', workflow_name: 'leave', status: 'deployed' }),
        promotion({ id: 'c', release_id: 'r-0', from_env: 'Production', workflow_name: 'payroll', status: 'pending' }),
      ])),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText('2 workflows to Production')).toBeInTheDocument()
    expect(screen.getByText('orders and leave')).toBeInTheDocument()
    expect(screen.getByText('Live')).toBeInTheDocument()
    expect(screen.getByText('Waiting for approval')).toBeInTheDocument()
  })
})

const promotion = (overrides: Record<string, unknown> = {}) => ({
  id: 'pr-1', peer_id: 'p-1', from_env: 'Staging', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
  from_version: 3, sender: 'ada', note: 'bigger batches', start: true, auto_rollback: true,
  changes: [{ kind: 'changed', what: 'Node n2: batch_size' }], status: 'pending', created_at: '2026-10-27T10:00:00Z',
  ...overrides,
})

describe('the Environments page on production', () => {
  beforeEach(() => {
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
      http.get('/api/environments/peers', () => HttpResponse.json([
        { id: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-10-27T10:00:00Z', vhost: 'hr' },
      ])),
    )
  })

  it('approves a whole release with one click', async () => {
    signInAs('Administrator', 'root')
    let status = 'pending'
    const approved: string[] = []
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([
        promotion({ id: 'pr-2', release_id: 'r-1', workflow_id: 'wf-2', workflow_name: 'leave', status, changes: [] }),
        promotion({ id: 'pr-1', release_id: 'r-1', status }),
      ])),
      http.post('/api/environments/promotions/:id/approve', ({ params }) => {
        approved.push(String(params.id))
        status = 'deployed'
        return HttpResponse.json(promotion({ status }))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(await screen.findByText('“bigger batches”')).toBeInTheDocument()
    expect(screen.getByText('Staging → hr')).toBeInTheDocument()
    expect(screen.getByText('into hr')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Approve and go live' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/leave and orders go live here now/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Approve and go live' }))
    expect(await screen.findByText('Recently live')).toBeInTheDocument()
    expect(approved).toEqual(['pr-2'])
  })

  it('does not let the sender approve their own release', async () => {
    signInAs('Administrator', 'ada')
    server.use(http.get('/api/environments/promotions', () => HttpResponse.json([promotion()])))
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText(/another administrator approves it/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Approve and go live' })).toBeDisabled()
  })

  it('offers Undo only on a release that is still the newest deploy of its workflows', async () => {
    signInAs('Administrator', 'root')
    server.use(http.get('/api/environments/promotions', () => HttpResponse.json([
      promotion({ id: 'pr-3', release_id: 'r-2', status: 'deployed', previous_version: 4, applied_version: 5 }),
      promotion({ id: 'pr-2', release_id: 'r-1', workflow_id: 'wf-2', workflow_name: 'leave', status: 'deployed', previous_version: 1, applied_version: 2 }),
      promotion({ id: 'pr-1', release_id: 'r-1', status: 'deployed', previous_version: 3, applied_version: 4 }),
    ])))
    wrap(<EnvironmentsPage />)
    await screen.findByText('Recently live')
    expect(screen.getAllByRole('button', { name: 'Undo' })).toHaveLength(1)
  })
})

describe('the Releases page', () => {
  it('is titled as its Settings row, with the way back, and says where the role is set', async () => {
    server.use(http.get('/api/environments/self', () => HttpResponse.json({ role: '', name: '' })))
    wrap(<EnvironmentsPage />)
    expect(screen.getByRole('heading', { level: 1, name: 'Releases' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to settings/i })).toHaveAttribute('href', '/settings')
    expect(await screen.findByText(/Settings › Workspace › Environments/)).toBeInTheDocument()
  })
})
