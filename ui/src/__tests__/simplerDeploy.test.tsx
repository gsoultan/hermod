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
import { settingsRows } from '@/components/layout/nav'
import { EnvironmentsTab } from '@/pages/environments/EnvironmentsTab'
import { EnvironmentsPage } from '@/pages/environments/EnvironmentsPage'
import { AcceptLinkPage } from '@/pages/environments/AcceptLinkPage'
import { ConnectionMatching } from '@/pages/environments/ConnectionMatching'
import { ReleaseDialog } from '@/pages/environments/ReleaseDialog'

let routeParams: Record<string, string> = {}
vi.mock('@tanstack/react-router', () => ({
  useParams: () => routeParams,
  Link: ({ children, to, search }: { children: ReactNode; to: string; search?: Record<string, string> }) => (
    <a href={search ? `${to}?${new URLSearchParams(search)}` : to}>{children}</a>
  ),
}))

beforeAll(() => { vi.spyOn(window, 'close').mockImplementation(() => {}) })
afterEach(() => {
  routeParams = {}
  window.history.replaceState(null, '', '/')
})

const PRD = 'https://prod.example.com'

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

const defaultRules = { approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 }
const link = {
  id: 'l-1', name: 'Production', url: PRD, role: 'production', created_by: 'root',
  created_at: '2026-10-27T10:00:00Z', reachable: true, vhost: 'hr', target_vhost: 'hr',
}
const vhosts = (...names: string[]) => http.get('/api/vhosts', () => HttpResponse.json({ data: names.map((name) => ({ name })) }))
const people = (...roles: string[]) => http.get('/api/users', () => HttpResponse.json({
  data: roles.map((role, i) => ({ id: `u-${i}`, username: `user${i}`, role })), total: roles.length,
}))

describe('the role is set for you', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  it('on a new instance, offers to connect a server and keeps receiving folded', async () => {
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: '', role: '' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json([])),
      vhosts('default', 'hr'),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    expect(await screen.findByText(/Connecting a server makes it Staging/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect a server' })).toBeInTheDocument()
    const receives = screen.getByRole('button', { name: /Receives from/ })
    expect(receives).toHaveAttribute('aria-expanded', 'false')
    await user.click(receives)
    expect(await screen.findByRole('button', { name: 'Create pairing code' })).toBeInTheDocument()
  })

  it('shows the new role once a server is connected', async () => {
    let role = ''
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: '', role })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json(role ? [link] : [])),
      http.post('/api/environments/links', () => {
        role = 'staging'
        return HttpResponse.json(link, { status: 201 })
      }),
      http.get('/api/environments/links/l-1/matching', () => HttpResponse.json([])),
      vhosts('default', 'hr'),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)

    await user.click(await screen.findByRole('button', { name: 'Connect a server' }))
    await user.type(await screen.findByLabelText('Address'), PRD)
    await user.type(screen.getByRole('combobox', { name: 'Workflows from' }), 'hr{ArrowDown}{Enter}')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await user.type(screen.getByLabelText('Pairing code'), 'ABCDEFGH')
    await user.click(screen.getByRole('button', { name: 'Link' }))

    expect(await screen.findByText('This instance is Staging')).toBeInTheDocument()
  }, 20000)

  it('says on the authorize page that allowing makes this instance Production', async () => {
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: '', role: '' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      people('Administrator', 'Editor'),
      vhosts('default', 'hr'),
    )
    wrap(<AcceptLinkPage />)
    expect(await screen.findByText(/Allowing makes it Production/)).toBeInTheDocument()
  })
})

describe('one Environments page', () => {
  it('is the Settings row for releases', () => {
    const row = settingsRows('Administrator' as never).find((r) => r.to === '/environments')
    expect(row?.label).toBe('Environments')
  })

  it('opens on Setup when no role is set, and the tab is in the address', async () => {
    signInAs('Administrator', 'root')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: '', role: '' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json([])),
      http.get('/api/environments/ready', () => HttpResponse.json([])),
      http.get('/api/environments/sent', () => HttpResponse.json([])),
      vhosts('default'),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    expect(screen.getByRole('heading', { level: 1, name: 'Environments' })).toBeInTheDocument()
    expect(await screen.findByRole('tab', { name: 'Setup', selected: true })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Connect a server' })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Releases' }))
    expect(window.location.search).toBe('?tab=releases')
  })

  it('opens the tab the address names', async () => {
    signInAs('Administrator', 'root')
    window.history.replaceState(null, '', '/environments?tab=setup')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/promotions', () => HttpResponse.json([])),
      vhosts('default'),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByRole('tab', { name: 'Setup', selected: true })).toBeInTheDocument()
    expect(await screen.findByText('Who approves a promoted workflow')).toBeInTheDocument()
  })

  it('shows an editor only the releases', async () => {
    signInAs('Editor', 'ada')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/promotions', () => HttpResponse.json([])),
      http.get('/api/environments/ready', () => HttpResponse.json([])),
      http.get('/api/environments/sent', () => HttpResponse.json([])),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByText('Ready to promote')).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Setup' })).not.toBeInTheDocument()
  })
})

describe('an only administrator', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  it('is asked who approves, with any administrator picked, and the rule is saved on Allow', async () => {
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    const calls: string[] = []
    let rules: unknown = null
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.put('/api/environments/rules', async ({ request }) => {
        rules = await request.json()
        calls.push('rules')
        return HttpResponse.json(rules as Record<string, unknown>)
      }),
      http.post('/api/environments/pairing-codes', () => {
        calls.push('code')
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-10-27T10:10:00Z', vhost: 'hr' }, { status: 201 })
      }),
      people('Administrator', 'Editor'),
      vhosts('default', 'hr'),
    )
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)

    const any = await screen.findByRole('radio', { name: /Any administrator, including me/ })
    expect(any).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(calls).toEqual(['rules', 'code']))
    expect(rules).toEqual({ ...defaultRules, approval: 'any_admin' })
  })

  it('keeps the rule when they pick another administrator', async () => {
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    const calls: string[] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.put('/api/environments/rules', () => { calls.push('rules'); return HttpResponse.json(defaultRules) }),
      http.post('/api/environments/pairing-codes', () => {
        calls.push('code')
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-10-27T10:10:00Z', vhost: 'hr' }, { status: 201 })
      }),
      people('Administrator'),
      vhosts('default', 'hr'),
    )
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)
    await user.click(await screen.findByRole('radio', { name: /other than the sender/ }))
    await user.click(screen.getByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(calls).toEqual(['code']))
  })

  it('is not asked when there is a second administrator', async () => {
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'hr' }
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      people('Administrator', 'Administrator'),
      vhosts('default', 'hr'),
    )
    wrap(<AcceptLinkPage />)
    expect(await screen.findByRole('button', { name: 'Allow' })).toBeEnabled()
    expect(screen.queryByRole('radio', { name: /Any administrator, including me/ })).not.toBeInTheDocument()
  })

  it('finds the rule from a release they cannot approve', async () => {
    signInAs('Administrator', 'ada')
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Prod', role: 'production' })),
      http.get('/api/environments/rules', () => HttpResponse.json(defaultRules)),
      http.get('/api/environments/peers', () => HttpResponse.json([])),
      http.get('/api/environments/promotions', () => HttpResponse.json([{
        id: 'pr-1', peer_id: 'p-1', from_env: 'Staging', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
        from_version: 3, sender: 'ada', start: true, status: 'pending', created_at: '2026-10-27T10:00:00Z',
      }])),
    )
    wrap(<EnvironmentsPage />)
    expect(await screen.findByRole('link', { name: 'Change who approves' })).toHaveAttribute('href', '/environments?tab=setup')
  })
})

describe('connections that come with the first release', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  it('are ready, and only a plain credential asks for a step', async () => {
    const moved: unknown[] = []
    server.use(
      http.get('/api/environments/links/l-1/matching', () => HttpResponse.json([
        { kind: 'source', id: 's-db', name: 'payroll-db', type: 'postgres', vhost: 'hr', status: 'missing', manual: false, candidates: [], plain_keys: ['password'] },
        { kind: 'source', id: 's-api', name: 'employees-api', type: 'http', vhost: 'hr', status: 'missing', manual: false, candidates: [], travels: true },
        { kind: 'sink', id: 'k-ev', name: 'hr-events', type: 'kafka', vhost: 'hr', status: 'missing', manual: false, candidates: [], travels: true },
        { kind: 'sink', id: 'k-wh', name: 'warehouse', type: 'snowflake', vhost: 'hr', target_id: 'p-wh', target_name: 'warehouse-prod', status: 'matched', manual: false, candidates: [{ id: 'p-wh', name: 'warehouse-prod', type: 'snowflake', vhost: 'hr' }] },
      ])),
      http.post('/api/environments/connections/:kind/:id/secret', async ({ request, params }) => {
        moved.push({ ...params, ...(await request.json() as object) })
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    wrap(<ConnectionMatching link={link as never} />)

    const list = await screen.findByRole('list', { name: 'Connections matched in Production' })
    expect(within(list).getAllByText('Comes with the first release')).toHaveLength(2)
    expect(within(list).queryByText('Missing')).not.toBeInTheDocument()
    expect(within(list).queryByRole('link', { name: /Create it in Production/ })).not.toBeInTheDocument()
    expect(screen.getByText(/3 of 4/)).toBeInTheDocument()
    expect(screen.getByText(/2 of them travel with the first release/)).toBeInTheDocument()

    await user.click(within(list).getByRole('button', { name: 'Move password to a secret' }))
    expect(screen.getByLabelText('Secret name')).toHaveValue('PAYROLL_DB_PASSWORD')
    await user.click(screen.getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(moved).toEqual([{ kind: 'source', id: 's-db', key: 'password', name: 'PAYROLL_DB_PASSWORD' }]))
  })

  it('says every flow can be promoted when all are ready', async () => {
    server.use(http.get('/api/environments/links/l-1/matching', () => HttpResponse.json([
      { kind: 'source', id: 's-api', name: 'employees-api', type: 'http', vhost: 'hr', status: 'missing', manual: false, candidates: [], travels: true },
    ])))
    wrap(<ConnectionMatching link={link as never} />)
    expect(await screen.findByText(/You can promote now/)).toBeInTheDocument()
  })
})

const preview = (overrides: Record<string, unknown> = {}) => ({
  link_id: 'l-1', link_name: 'Production', workflow_id: 'wf-1', workflow_name: 'Payroll sync', vhost: 'hr',
  from_version: 7, current_version: 0,
  checks: [
    { name: 'Workflow is valid', ok: true, blocking: true },
    { name: 'Connections matched', ok: true, blocking: true, detail: '1 of 2; 1 created in Production on approval' },
    { name: 'Secrets exist in Production', ok: false, blocking: false, detail: 'Not found in the vhost\'s secrets there: HR_TOKEN.' },
  ],
  changes: [{ kind: 'added', what: 'New workflow with 3 nodes' }],
  rules: defaultRules,
  missing_secrets: ['HR_TOKEN'],
  connections: [
    { kind: 'source', id: 's-api', name: 'employees-api', type: 'http', status: 'create', settings: [
      { key: 'base_url', value: 'https://hr-stg.example.com' }, { key: 'token', value: '{{secret.HR_TOKEN}}', secret: true },
    ] },
    { kind: 'sink', id: 'k-wh', name: 'warehouse', type: 'snowflake', status: 'uses', target_id: 'p-wh', target_name: 'warehouse-prod' },
  ],
  ready: true,
  ...overrides,
})

describe('the shorter promote dialog', () => {
  it('sums it up in one line and folds the connection settings', async () => {
    signInAs('Editor', 'ada')
    const bodies: Record<string, unknown>[] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
      http.get('/api/environments/ready', () => HttpResponse.json([])),
      http.post('/api/environments/releases/preview', () => HttpResponse.json({
        groups: [{ link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', previews: [preview()], ready: true }],
        unlinked: [], ready: true,
      })),
      http.post('/api/environments/releases', async ({ request }) => {
        bodies.push(await request.json() as Record<string, unknown>)
        return HttpResponse.json([{ link_id: 'l-1', link_name: 'Production', promotions: [{ id: 'p-1', status: 'pending' }] }], { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('Ready. 1 connection is created, 1 secret to fill.')).toBeInTheDocument()
    expect(screen.getByText(/All required checks passed, 1 warning\. An administrator other than you approves it for Production\./)).toBeInTheDocument()
    // The secrets box says it; no warning repeats it.
    expect(screen.queryByText(/Not found in the vhost's secrets there/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Base URL')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Change settings for Production/ }))
    const url = await screen.findByLabelText('Base URL')
    expect(screen.getByText('{{secret.HR_TOKEN}}')).toBeInTheDocument()
    expect(screen.getByText("Uses Production's warehouse-prod")).toBeInTheDocument()
    await user.clear(url)
    await user.type(url, 'https://hr.example.com')
    const send = screen.getByRole('button', { name: 'Send for approval' })
    await waitFor(() => expect(send).toBeEnabled())
    await user.click(send)
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0].settings).toEqual({ 's-api': { base_url: 'https://hr.example.com' } })
  })
})
