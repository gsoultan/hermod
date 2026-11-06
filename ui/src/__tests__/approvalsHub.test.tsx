import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ApprovalsHubPage } from '@/pages/approvals/ApprovalsHubPage'

let search: Record<string, unknown> = {}
vi.mock('@tanstack/react-router', () => ({
  useSearch: () => search,
  useNavigate: () => (opts: { search: (prev: Record<string, unknown>) => Record<string, unknown> }) => {
    search = opts.search(search)
    rerender?.()
  },
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

let rerender: (() => void) | undefined
const client = () => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })

function wrap() {
  const qc = client()
  const tree = () => (
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={qc}>
        <ConfirmProvider><ApprovalsHubPage /></ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
  const r = render(tree())
  rerender = () => r.rerender(tree())
  return r
}

const promotion = (over: Record<string, unknown> = {}) => ({
  id: 'pr-1', peer_id: 'p-1', from_env: 'Staging', workflow_id: 'wf-1', workflow_name: 'orders', vhost: 'hr',
  from_version: 3, sender: 'ada', note: 'bigger batches', start: true, auto_rollback: false,
  changes: [], status: 'pending', created_at: '2026-11-05T09:00:00Z',
  ...over,
})

const held = [
  { id: 'ap-1aaaaaaa', workflow_id: 'wf1', node_id: 'check', message_id: 'm1', status: 'pending', created_at: '2026-11-05T08:00:00Z' },
]

beforeEach(() => {
  search = {}
  server.use(
    http.get('/api/environments/promotions', () => HttpResponse.json([promotion()])),
    http.get('/api/environments/sent', () => HttpResponse.json([])),
    http.get('/api/environments/links', () => HttpResponse.json([])),
    http.get('/api/environments/rules', () => HttpResponse.json({ approval: 'any_admin', auto_rollback: false, min_soak_minutes: 0 })),
    http.get('/api/environments/vhost-requests', () => HttpResponse.json([
      { id: 'vr-1', group: 'g', peer_name: 'Staging', vhost: 'finance', requested_at: '2026-11-05T09:00:00Z' },
    ])),
    http.get('/api/approvals', () => HttpResponse.json({ data: held, total: held.length })),
    http.get('/api/workflows', () => HttpResponse.json({ data: [{ id: 'wf1', name: 'Billing sync' }], total: 1 })),
  )
})

describe('the Approvals page', () => {
  it('names both kinds, with how many of each wait, and opens on releases', async () => {
    signInAs('Administrator', 'root')
    wrap()
    expect(screen.getByRole('heading', { level: 1, name: 'Approvals' })).toBeInTheDocument()
    const releases = await screen.findByRole('tab', { name: 'Releases, 2 waiting' })
    expect(releases).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('tab', { name: 'Held messages, 1 waiting' })).toBeInTheDocument()

    expect(screen.getByText(/A release is a set of flow versions sent from another environment/)).toBeInTheDocument()
    expect(await screen.findByText('“bigger batches”')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Approve and go live' })).toBeEnabled()
    expect(screen.getByText(/Staging asks to add/)).toBeInTheDocument()
  })

  it('shows the held messages on their own tab, said for what approving them does', async () => {
    signInAs('Administrator', 'root')
    search = { tab: 'held' }
    wrap()
    expect(await screen.findByText(/An Approval step in a running flow holds each message/)).toBeInTheDocument()
    const list = await screen.findByRole('list', { name: 'Approvals' })
    expect(await within(list).findByText('Billing sync')).toBeInTheDocument()
  })

  it('switches tabs from the tab row', async () => {
    signInAs('Administrator', 'root')
    const user = userEvent.setup()
    wrap()
    await user.click(await screen.findByRole('tab', { name: /Held messages/ }))
    expect(await screen.findByRole('list', { name: 'Approvals' })).toBeInTheDocument()
  })

  it('tells an editor who approves releases instead of an empty list', async () => {
    signInAs('Editor', 'ed')
    wrap()
    expect(await screen.findByText('Administrators approve releases. You can approve held messages.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve and go live' })).not.toBeInTheDocument()
  })

  it('says so when no release waits', async () => {
    signInAs('Administrator', 'root')
    server.use(
      http.get('/api/environments/promotions', () => HttpResponse.json([promotion({ status: 'deployed' })])),
      http.get('/api/environments/vhost-requests', () => HttpResponse.json([])),
    )
    wrap()
    expect(await screen.findByText('No release is waiting for you')).toBeInTheDocument()
  })
})
