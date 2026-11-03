import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { encodeOrigin, groupLinksByServer } from '@/lib/environments'
import { EnvironmentsTab } from '@/pages/environments/EnvironmentsTab'
import { AcceptLinkPage } from '@/pages/environments/AcceptLinkPage'

let routeParams: Record<string, string> = {}
vi.mock('@tanstack/react-router', () => ({
  useParams: () => routeParams,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

beforeAll(() => { vi.spyOn(window, 'close').mockImplementation(() => {}) })

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

const base = { name: 'Production', role: 'production', created_by: 'root', created_at: '2026-11-01T10:00:00Z', reachable: true }
const orders = { ...base, id: 'l-orders', url: PRD, vhost: 'orders', target_vhost: 'orders' }
const billing = { ...base, id: 'l-billing', url: `${PRD}/`, vhost: 'billing', target_vhost: 'billing-prod' }

const staging = (links: unknown[]) => [
  http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
  http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
  http.get('/api/environments/peers', () => HttpResponse.json([])),
  http.get('/api/environments/links', () => HttpResponse.json(links)),
  http.get('/api/vhosts', () => HttpResponse.json({ data: ['default', 'orders', 'billing', 'marketing', 'support'].map((name) => ({ name })) })),
]

describe('one connection per server', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  it('groups links to the same address, whatever the trailing slash', () => {
    const servers = groupLinksByServer([orders, billing, { ...base, id: 'l-x', url: 'https://eu.example.com', vhost: 'hr' }] as never)
    expect(servers.map((s) => [s.url, s.links.map((l) => l.vhost)])).toEqual([
      [PRD, ['billing', 'orders']],
      ['https://eu.example.com', ['hr']],
    ])
  })

  it('shows one card per server with every vhost under it', async () => {
    server.use(...staging([orders, billing, { ...base, id: 'l-hr', url: PRD, vhost: 'hr', target_vhost: 'hr', pending: true, check_note: 'Waiting for an administrator in Production to allow it.' }]))
    wrap(<EnvironmentsTab />)
    const card = await screen.findByRole('region', { name: 'Production' })
    expect(within(card).getByText(PRD)).toBeInTheDocument()
    expect(within(card).getByText('orders → orders')).toBeInTheDocument()
    expect(within(card).getByText('billing → billing-prod')).toBeInTheDocument()
    expect(within(card).getByText('hr → hr')).toBeInTheDocument()
    expect(within(card).getByText('Waiting for approval')).toBeInTheDocument()
    expect(screen.getAllByRole('region', { name: 'Production' })).toHaveLength(1)
  })

  it('adds vhosts to a server without a pairing code', async () => {
    let body: unknown = null
    let links: unknown[] = [orders]
    server.use(
      http.get('/api/environments/links', () => HttpResponse.json(links)),
      ...staging([]),
      http.post('/api/environments/links/l-orders/vhosts', async ({ request }) => {
        body = await request.json()
        const added = [{ ...orders, id: 'l-mk', vhost: 'marketing', target_vhost: 'marketing' }, { ...orders, id: 'l-sp', vhost: 'support', target_vhost: 'support-prod', pending: true }]
        links = [orders, ...added]
        return HttpResponse.json(added, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)
    await user.click(await screen.findByRole('button', { name: 'Add vhosts to Production' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add vhosts to Production' })
    expect(within(dialog).queryByRole('checkbox', { name: 'orders' })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('checkbox', { name: 'marketing' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'support' }))
    const there = within(dialog).getByLabelText('support lands in')
    await user.clear(there)
    await user.type(there, 'support-prod')
    await user.click(within(dialog).getByRole('button', { name: 'Add 2 vhosts' }))
    await waitFor(() => expect(body).toEqual({ routes: [
      { vhost: 'marketing', target_vhost: 'marketing' },
      { vhost: 'support', target_vhost: 'support-prod' },
    ] }))
    expect(await screen.findByText(/support waits for an administrator in Production/)).toBeInTheDocument()
  }, 20000)

  it('offers to add vhosts when the address is already connected', async () => {
    server.use(...staging([orders]))
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)
    await user.click(await screen.findByRole('button', { name: 'Connect a server' }))
    await user.type(await screen.findByLabelText('Address'), `${PRD}/`)
    expect(await screen.findByText(/already connected to Production at this address/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add vhosts instead' }))
    expect(await screen.findByRole('dialog', { name: 'Add vhosts to Production' })).toBeInTheDocument()
  }, 20000)
})

describe('production: connections sending here', () => {
  beforeEach(() => signInAs('Administrator', 'root'))

  const peers = [
    { id: 'p-1', group: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-11-01T10:00:00Z', vhost: 'orders', add_vhosts: true },
    { id: 'p-2', group: 'p-1', name: 'Staging', url: 'https://stg.example.com', created_by: 'root', created_at: '2026-11-02T10:00:00Z', vhost: 'billing', add_vhosts: true },
  ]
  const production = () => [
    http.get('/api/environments/self', () => HttpResponse.json({ name: 'Production', role: 'production' })),
    http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 })),
    http.get('/api/environments/peers', () => HttpResponse.json(peers)),
    http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'orders' }] })),
  ]

  it('shows one connection with its vhosts, and cuts it off whole', async () => {
    let cut = ''
    server.use(
      ...production(),
      http.get('/api/environments/vhost-requests', () => HttpResponse.json([])),
      http.delete('/api/environments/peers/:id', ({ params, request }) => {
        cut = `${String(params.id)}${new URL(request.url).search}`
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)
    const conn = await screen.findByRole('region', { name: 'Staging' })
    expect(within(conn).getByText('into orders')).toBeInTheDocument()
    expect(within(conn).getByText('into billing')).toBeInTheDocument()
    expect(within(conn).getByText(/may add vhosts on its own/)).toBeInTheDocument()
    await user.click(within(conn).getByRole('button', { name: 'Cut Staging off' }))
    await user.click(await screen.findByRole('button', { name: 'Cut off' }))
    await waitFor(() => expect(cut).toBe('p-1?all=true'))
  }, 20000)

  it('allows a vhost a connection asked for', async () => {
    let allowed = ''
    server.use(
      ...production(),
      http.get('/api/environments/vhost-requests', () => HttpResponse.json([{ id: 'r-1', group: 'p-1', peer_name: 'Staging', vhost: 'hr', from_vhost: 'hr', requested_at: '2026-11-03T10:00:00Z' }])),
      http.post('/api/environments/vhost-requests/:id/allow', ({ params }) => {
        allowed = String(params.id)
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsTab />)
    expect(await screen.findByText(/Staging asks to add/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Allow hr' }))
    await waitFor(() => expect(allowed).toBe('r-1'))
  }, 20000)
})

describe('authorizing a connection', () => {
  afterEach(() => { routeParams = {} })

  it('lets the other instance add more vhosts on its own unless told otherwise', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'orders' }
    const bodies: unknown[] = []
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'orders' }] })),
      http.post('/api/environments/pairing-codes', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-11-03T10:10:00Z', vhost: 'orders' }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)
    expect(await screen.findByRole('radio', { name: /may add more on its own/ })).toBeChecked()
    await user.click(await screen.findByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(bodies).toEqual([{ label: 'https://stg.example.com', vhost: 'orders', add_vhosts: true, decide: true }]))
  })

  it('asks each time when the administrator chooses that', async () => {
    signInAs('Administrator', 'root')
    routeParams = { from: encodeOrigin('https://stg.example.com'), vhost: 'orders' }
    const bodies: unknown[] = []
    server.use(
      http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'orders' }] })),
      http.post('/api/environments/pairing-codes', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json({ code: 'ABCD-2345', expires_at: '2026-11-03T10:10:00Z', vhost: 'orders' }, { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<AcceptLinkPage />)
    await user.click(await screen.findByRole('radio', { name: /Ask me each time/ }))
    await user.click(await screen.findByRole('button', { name: 'Allow' }))
    await waitFor(() => expect(bodies).toEqual([{ label: 'https://stg.example.com', vhost: 'orders', decide: true }]))
  })
})
