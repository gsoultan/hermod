import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ReleaseDialog } from '@/pages/environments/ReleaseDialog'
import { EnvironmentsPage } from '@/pages/environments/EnvironmentsPage'
import { PromoteStrip, usePromoteTarget } from '@/pages/environments/PromoteStrip'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({}),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

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

type Item = { workflow_id: string; version: number }

const preview = (overrides: Record<string, unknown> = {}) => ({
  link_id: 'l-1', link_name: 'Production', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
  from_version: 14, current_version: 12,
  checks: [
    { name: 'Workflow is valid', ok: true, blocking: true },
    { name: 'Connections matched', ok: true, blocking: true, detail: '3 of 3' },
  ],
  changes: [
    { kind: 'added', what: 'Adds step Mask card numbers' },
    { kind: 'changed', what: 'Changes the filter on Only paid orders' },
  ],
  rules: { approval: 'admin_except_sender', auto_rollback: true, min_soak_minutes: 0 },
  ready: true,
  ...overrides,
})

const answer = (previews: ReturnType<typeof preview>[]) => ({
  groups: [{ link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', previews, ready: previews.every((p) => p.ready) }],
  unlinked: [],
  ready: previews.every((p) => p.ready),
})

function common() {
  server.use(
    http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'staging' })),
    http.get('/api/environments/ready', () => HttpResponse.json([])),
  )
}

describe('the promote dialog', () => {
  it('asks for the values of secrets new to production and sends only what was typed', async () => {
    signInAs('Editor', 'ada')
    common()
    const bodies: Record<string, unknown>[] = []
    server.use(
      http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([
        preview({ missing_secrets: ['STRIPE_KEY', 'SMTP_PASSWORD'] }),
      ]))),
      http.post('/api/environments/releases', async ({ request }) => {
        bodies.push(await request.json() as Record<string, unknown>)
        return HttpResponse.json([{ link_id: 'l-1', link_name: 'Production', promotions: [{ id: 'p-1', status: 'pending' }] }], { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('2 secrets are new to Production')).toBeInTheDocument()
    expect(screen.getByText('2 secret values left for the approver.')).toBeInTheDocument()
    await user.type(screen.getByLabelText('STRIPE_KEY'), 'sk_live_1')
    expect(screen.getByText('1 secret value left for the approver.')).toBeInTheDocument()

    const button = screen.getByRole('button', { name: 'Send for approval' })
    await waitFor(() => expect(button).toBeEnabled())
    await user.click(button)
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0].secrets).toEqual({ STRIPE_KEY: 'sk_live_1' })
  })

  it('sends no secrets field when none are needed', async () => {
    signInAs('Editor', 'ada')
    common()
    const bodies: Record<string, unknown>[] = []
    server.use(
      http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([preview()]))),
      http.post('/api/environments/releases', async ({ request }) => {
        bodies.push(await request.json() as Record<string, unknown>)
        return HttpResponse.json([{ link_id: 'l-1', link_name: 'Production', promotions: [{ id: 'p-1', status: 'pending' }] }], { status: 201 })
      }),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)
    const button = await screen.findByRole('button', { name: 'Send for approval' })
    await waitFor(() => expect(button).toBeEnabled())
    expect(screen.queryByText(/secret value/)).not.toBeInTheDocument()
    await user.click(button)
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).not.toHaveProperty('secrets')
  })

  it('says where it goes, who approves, and what changes, on one screen', async () => {
    signInAs('Editor', 'ada')
    common()
    server.use(http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([preview()]))))
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('Ready to send. All 2 checks passed.')).toBeInTheDocument()
    expect(screen.getByText('An administrator other than you approves it for Production.')).toBeInTheDocument()
    expect(screen.getByText('v14')).toBeInTheDocument()
    expect(screen.getByText('now runs v12')).toBeInTheDocument()
    expect(screen.getByText('What changes in Production')).toBeInTheDocument()
    expect(screen.getByText('+ Adds step Mask card numbers')).toBeInTheDocument()
    expect(screen.getByText('~ Changes the filter on Only paid orders')).toBeInTheDocument()
    expect(screen.getByText('Goes live in Production after approval.')).toBeInTheDocument()
    expect(screen.getByText('Newest version · start after deploy · undo if it fails')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Show checks' }))
    expect(await screen.findByText('✓ Connections matched: 3 of 3')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send for approval' })).toBeEnabled())
  })

  it('lets an administrator match a connection right where the problem is', async () => {
    signInAs('Administrator', 'root')
    common()
    let matched = false
    let mapping: unknown = null
    const unmatched = preview({
      ready: false, current_version: 0,
      unmatched: [{ kind: 'source', id: 's-2', name: 'Orders DB', type: 'postgres', vhost: 'hr', status: 'missing', manual: false, candidates: [] }],
      checks: [
        { name: 'Workflow is valid', ok: true, blocking: true },
        { name: 'Connections matched', ok: false, blocking: true, detail: '2 of 3 matched. Match these in Settings › Environments: Orders DB' },
      ],
    })
    server.use(
      http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([matched ? preview() : unmatched]))),
      http.get('/api/environments/links/:id/matching', () => HttpResponse.json([
        { kind: 'source', id: 's-2', name: 'Orders DB', type: 'postgres', vhost: 'hr', status: 'missing', manual: false,
          candidates: [{ id: 'p-9', name: 'orders-prod', type: 'postgres', vhost: 'hr' }] },
      ])),
      http.put('/api/environments/links/:id/mapping', async ({ request }) => {
        mapping = await request.json()
        matched = true
        return HttpResponse.json({ id: 'l-1' })
      }),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('1 thing to fix before sending')).toBeInTheDocument()
    expect(screen.getByText('new there')).toBeInTheDocument()
    expect(screen.getByText('Fix 1 thing to send.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send for approval' })).toBeDisabled()
    // The raw "Match these in Settings" line is replaced by the fix itself.
    expect(screen.queryByText(/Match these in Settings/)).not.toBeInTheDocument()

    const pick = screen.getByRole('combobox', { name: 'Production connection for Orders DB' })
    await waitFor(() => expect(pick).toBeEnabled())
    await user.click(pick)
    await user.click(await screen.findByRole('option', { name: 'orders-prod · postgres', hidden: true }))
    await waitFor(() => expect(mapping).toEqual({ mapping: { 's-2': 'p-9' } }))
    expect(await screen.findByText('Ready to send. All 2 checks passed.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send for approval' })).toBeEnabled())
  })

  it('tells an editor who can match it, and what to set for a server variable', async () => {
    signInAs('Editor', 'ada')
    common()
    let askedForMatching = false
    server.use(
      http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([preview({
        ready: false,
        unmatched: [{ kind: 'sink', id: 'k-1', name: 'lake', type: 's3', vhost: 'hr', status: 'missing', manual: false, candidates: [] }],
        missing_envs: ['REGION'],
        checks: [
          { name: 'Connections matched', ok: false, blocking: true, detail: '0 of 1 matched' },
          { name: 'Has run long enough', ok: false, blocking: true, detail: 'v14 is 10 minutes old; Production wants 60' },
        ],
      })]))),
      http.get('/api/environments/links/:id/matching', () => {
        askedForMatching = true
        return HttpResponse.json([])
      }),
    )
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('3 things to fix before sending')).toBeInTheDocument()
    expect(screen.getByText('An administrator matches it here, or in Settings › Environments.')).toBeInTheDocument()
    expect(screen.getByText('2. REGION is not set on the Production server')).toBeInTheDocument()
    expect(screen.getByText('HERMOD_ENV_REGION')).toBeInTheDocument()
    expect(screen.getByText(/Production wants 60/)).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: /Production connection for/ })).not.toBeInTheDocument()
    expect(askedForMatching).toBe(false)
  })

  it('adds another flow that is ready, and fixes or leaves out one that is not', async () => {
    signInAs('Editor', 'ada')
    common()
    const previews: Item[][] = []
    server.use(
      http.get('/api/environments/ready', () => HttpResponse.json([
        { link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', workflows: [
          { id: 'wf-1', name: 'orders', vhost: 'hr', version: 14, last_sent_version: 12, active: true },
          { id: 'wf-2', name: 'leave-requests', vhost: 'hr', version: 8, last_sent_version: 7, active: true },
        ] },
      ])),
      http.post('/api/environments/releases/preview', async ({ request }) => {
        const { items } = (await request.json()) as { items: Item[] }
        previews.push(items)
        return HttpResponse.json(answer(items.map((i) => i.workflow_id === 'wf-2'
          ? preview({
            workflow_id: 'wf-2', workflow_name: 'leave-requests', from_version: 8, current_version: 7, ready: false,
            unmatched: [{ kind: 'source', id: 's-3', name: 'HR DB', type: 'mysql', vhost: 'hr', status: 'missing', manual: false, candidates: [] }],
            checks: [{ name: 'Connections matched', ok: false, blocking: true }],
          })
          : preview())))
      }),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    await screen.findByText('Ready to send. All 2 checks passed.')
    await user.click(screen.getByRole('combobox', { name: 'Add another flow' }))
    await user.click(await screen.findByRole('option', { name: 'leave-requests', hidden: true }))
    await waitFor(() => expect(previews.at(-1)).toEqual([{ workflow_id: 'wf-1', version: 0 }, { workflow_id: 'wf-2', version: 0 }]))

    expect(await screen.findByText('1 of 2 flows are ready')).toBeInTheDocument()
    expect(screen.getByText(/v7 → v8 · Pick the Production connection for “HR DB”/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Fix leave-requests' }))
    expect(await screen.findByText('1. Pick the Production connection for “HR DB”')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Leave out leave-requests' }))
    await waitFor(() => expect(previews.at(-1)).toEqual([{ workflow_id: 'wf-1', version: 0 }]))
    expect(await screen.findByRole('button', { name: 'Send for approval' })).toBeInTheDocument()
  })

  it('says it goes live at once where nobody approves, and points to Releases after sending', async () => {
    signInAs('Editor', 'ada')
    common()
    server.use(
      http.post('/api/environments/releases/preview', () => HttpResponse.json(answer([preview({ rules: { approval: 'none', auto_rollback: false, min_soak_minutes: 0 } })]))),
      http.post('/api/environments/releases', () => HttpResponse.json([
        { link_id: 'l-1', link_name: 'Production', release_id: 'r-1', promotions: [{ id: 'pr-1', status: 'deployed' }] },
      ], { status: 201 })),
    )
    const user = userEvent.setup()
    wrap(<ReleaseDialog opened onClose={() => {}} items={[{ workflow_id: 'wf-1', version: 0 }]} />)

    expect(await screen.findByText('Goes live in Production without approval.')).toBeInTheDocument()
    expect(screen.getByText('Newest version · start after deploy · no automatic undo')).toBeInTheDocument()
    const send = screen.getByRole('button', { name: 'Send and go live' })
    await waitFor(() => expect(send).toBeEnabled())
    await user.click(send)
    expect(await screen.findByText(/the release is live in Production · hr/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Follow it on Releases' })).toHaveAttribute('href', '/environments')
  })
})

function Strip({ id, vhost }: { id: string; vhost: string }) {
  return <PromoteStrip here="Staging" target={usePromoteTarget(id, vhost, true)} />
}

describe('the flow page strip', () => {
  beforeEach(() => {
    signInAs('Editor', 'ada')
    server.use(
      http.get('/api/environments/links', () => HttpResponse.json([
        { id: 'l-1', name: 'Production', url: 'https://prod', role: 'production', created_by: 'root', created_at: '2026-10-27T10:00:00Z', reachable: true, vhost: 'hr' },
      ])),
      http.get('/api/environments/ready', () => HttpResponse.json([
        { link_id: 'l-1', link_name: 'Production', vhost: 'hr', workflows: [
          { id: 'wf-1', name: 'orders', vhost: 'hr', version: 14, last_sent_version: 12, active: true },
          { id: 'wf-2', name: 'refunds', vhost: 'hr', version: 3, last_sent_version: 0, active: true },
        ] },
      ])),
    )
  })

  it('shows how far behind the next environment is', async () => {
    const { unmount } = wrap(<Strip id="wf-1" vhost="hr" />)
    const strip = await screen.findByRole('region', { name: 'Staging and Production' })
    expect(within(strip).getByText('last sent v12 · 2 versions to promote')).toBeInTheDocument()
    unmount()

    wrap(<Strip id="wf-2" vhost="hr" />)
    expect(await screen.findByText('not there yet')).toBeInTheDocument()
  })

  it('says up to date, and shows nothing where no environment is connected', async () => {
    const { unmount } = wrap(<Strip id="wf-7" vhost="hr" />)
    expect(await screen.findByText('Up to date')).toBeInTheDocument()
    unmount()
    const { container } = wrap(<Strip id="wf-7" vhost="marketing" />)
    await waitFor(() => expect(screen.queryByText('Up to date')).not.toBeInTheDocument())
    expect(container.querySelector('section')).toBeNull()
  })
})

describe('the Releases page', () => {
  it('promotes only what stays ticked', async () => {
    signInAs('Editor', 'ada')
    const previews: Item[][] = []
    server.use(
      http.get('/api/environments/self', () => HttpResponse.json({ name: 'Staging', role: 'development' })),
      http.get('/api/environments/sent', () => HttpResponse.json([])),
      http.get('/api/environments/links', () => HttpResponse.json([])),
      http.get('/api/environments/ready', () => HttpResponse.json([
        { link_id: 'l-1', link_name: 'Production', vhost: 'hr', target_vhost: 'hr', workflows: [
          { id: 'wf-1', name: 'orders', vhost: 'hr', version: 3, last_sent_version: 2, active: true },
          { id: 'wf-2', name: 'leave', vhost: 'hr', version: 1, last_sent_version: 0, active: true },
          { id: 'wf-3', name: 'payroll', vhost: 'hr', version: 4, last_sent_version: 3, active: true },
        ] },
      ])),
      http.post('/api/environments/releases/preview', async ({ request }) => {
        const { items } = (await request.json()) as { items: Item[] }
        previews.push(items)
        return HttpResponse.json(answer(items.map((i) => preview({ workflow_id: i.workflow_id }))))
      }),
    )
    const user = userEvent.setup()
    wrap(<EnvironmentsPage />)

    await user.click(await screen.findByRole('checkbox', { name: 'Include leave' }))
    expect(screen.getByRole('button', { name: 'Promote hr' })).toHaveTextContent('Promote 2')
    await user.click(screen.getByRole('button', { name: 'Promote 2 of 3' }))
    await waitFor(() => expect(previews[0]).toEqual([{ workflow_id: 'wf-1', version: 0 }, { workflow_id: 'wf-3', version: 0 }]))
  })
})
