import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { Suspense, type ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { UsersPage } from '@/pages/admin/UsersPage'
import { VHostsPage } from '@/pages/admin/VHostsPage'
import { WorkersPage } from '@/pages/admin/WorkersPage'

const navigated: any[] = []
vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...rest }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...rest}>{children}</a>
  },
  useNavigate: () => (opts: any) => { navigated.push(opts) },
}))

let deleted: string[] = []
let posted: string[] = []

function renderPage(page: ReactNode) {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <ConfirmProvider>
          <Suspense fallback={null}>{page}</Suspense>
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

const recent = () => new Date(Date.now() - 5_000).toISOString()

beforeEach(() => {
  deleted = []
  posted = []
  navigated.length = 0
  signInAs('Administrator')
  server.use(
    http.get('/api/users', () => HttpResponse.json({ data: [
      { id: 'u1', username: 'gembit', full_name: 'Gembit S', email: 'g@example.com', role: 'Administrator', vhosts: [], two_factor_enabled: true },
      { id: 'u2', username: 'analyst', full_name: '', email: 'a@example.com', role: 'Viewer', vhosts: ['staging'], two_factor_enabled: false },
    ], total: 2 })),
    http.delete('/api/users/:id', ({ params }) => { deleted.push(String(params.id)); return HttpResponse.json({}) }),
    http.get('/api/vhosts', () => HttpResponse.json({ data: [
      { id: 'v1', name: 'prod', description: 'Production flows' },
      { id: 'v2', name: 'staging' },
    ], total: 2 })),
    http.delete('/api/vhosts/:id', ({ params }) => { deleted.push(String(params.id)); return HttpResponse.json({}) }),
    http.get('/api/workers', () => HttpResponse.json({ data: [
      { id: 'w-online-1234', name: 'worker-eu', host: '10.0.0.4', port: 8080, description: 'Frankfurt', status: 'online', last_seen: recent(), cpu_cores: 2, cpu_usage: 0.3 },
      { id: 'w-offline-5678', name: 'worker-us', status: 'offline' },
    ], total: 2 })),
    http.post('/api/workers/:id/start', ({ params }) => { posted.push(`start ${params.id}`); return HttpResponse.json({}) }),
    http.post('/api/workers/:id/shutdown', ({ params }) => { posted.push(`shutdown ${params.id}`); return HttpResponse.json({}) }),
    http.delete('/api/workers/:id', ({ params }) => { deleted.push(String(params.id)); return HttpResponse.json({}) }),
  )
})

describe('People', () => {
  it('is one row per person: initial, name, role and reach, two-factor, into their settings', async () => {
    renderPage(<UsersPage />)
    expect(await screen.findByRole('heading', { name: 'People' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to settings/i })).toHaveAttribute('href', '/settings')
    const rows = within(await screen.findByRole('list', { name: 'People' })).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('G')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Administrator · every virtual host · Gembit S · g@example.com')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Two-factor on')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Viewer · staging · a@example.com')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Two-factor off')).toBeInTheDocument()
    expect(within(rows[0]).getByRole('link', { name: /gembit/ })).toHaveAttribute('href', '/users/u1/edit')
    expect(screen.getByRole('searchbox', { name: /search people/i })).toBeInTheDocument()
  })

  it('adds a person from the header', async () => {
    renderPage(<UsersPage />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add person' }))
    expect(navigated).toEqual([{ to: '/users/new' }])
  })

  it('asks before deleting someone', async () => {
    renderPage(<UsersPage />)
    await screen.findByText('analyst')
    await userEvent.click(screen.getByRole('button', { name: 'More for analyst' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Delete', hidden: true }))
    await userEvent.click(await screen.findByRole('button', { name: 'Delete user' }))
    await vi.waitFor(() => expect(deleted).toEqual(['u2']))
  })
})

describe('Virtual hosts', () => {
  it('is one row per virtual host with its description, and a section header inside Settings', async () => {
    renderPage(<VHostsPage embedded />)
    expect(await screen.findByRole('heading', { name: 'Virtual hosts' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /back to settings/i })).toBeNull()
    const rows = within(screen.getByRole('list', { name: 'Virtual hosts' })).getAllByRole('listitem')
    expect(within(rows[0]).getByText('Production flows')).toBeInTheDocument()
    expect(within(rows[1]).getByText('No description')).toBeInTheDocument()
    expect(within(rows[0]).getByRole('link', { name: /prod/ })).toHaveAttribute('href', '/vhosts/v1/edit')
    await userEvent.click(screen.getByRole('button', { name: 'Add virtual host' }))
    expect(navigated).toEqual([{ to: '/vhosts/new' }])
  })

  it('asks before deleting one', async () => {
    renderPage(<VHostsPage />)
    expect(await screen.findByRole('link', { name: /back to settings/i })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'More for staging' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Delete', hidden: true }))
    await userEvent.click(await screen.findByRole('button', { name: 'Delete virtual host' }))
    await vi.waitFor(() => expect(deleted).toEqual(['v2']))
  })
})

describe('Workers', () => {
  it('is one row per worker: address, id, state and capacity, with Start on an offline one', async () => {
    renderPage(<WorkersPage />)
    expect(await screen.findByRole('heading', { name: 'Workers' })).toBeInTheDocument()
    const rows = within(screen.getByRole('list', { name: 'Workers' })).getAllByRole('listitem')
    expect(within(rows[0]).getByText('10.0.0.4:8080 · Frankfurt · w-online-1234')).toBeInTheDocument()
    expect(within(rows[0]).getByText('Online')).toBeInTheDocument()
    expect(within(rows[0]).getByText('2 cores')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Offline')).toBeInTheDocument()
    expect(within(rows[1]).getByText(/last seen never/i)).toBeInTheDocument()
    expect(within(rows[0]).queryByRole('button', { name: 'Start worker-eu' })).toBeNull()
    await userEvent.click(within(rows[1]).getByRole('button', { name: 'Start worker-us' }))
    await vi.waitFor(() => expect(posted).toEqual(['start w-offline-5678']))
  })

  it('keeps shut down, start instructions, edit and unregister behind ⋯', async () => {
    renderPage(<WorkersPage />)
    await screen.findByText('worker-eu')
    await userEvent.click(screen.getByRole('button', { name: 'More for worker-eu' }))
    expect(await screen.findByRole('menuitem', { name: 'Edit', hidden: true })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: 'Shut down', hidden: true }))
    await userEvent.click(await screen.findByRole('button', { name: 'Shut down' }))
    await vi.waitFor(() => expect(posted).toEqual(['shutdown w-online-1234']))

    await userEvent.click(screen.getByRole('button', { name: 'More for worker-us' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'How to start it', hidden: true }))
    expect(await screen.findByText(/--worker-guid "w-offline-5678"/)).toBeInTheDocument()
  })

  it('unregisters after asking', async () => {
    renderPage(<WorkersPage />)
    await screen.findByText('worker-us')
    await userEvent.click(screen.getByRole('button', { name: 'More for worker-us' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Unregister', hidden: true }))
    await userEvent.click(await screen.findByRole('button', { name: 'Unregister' }))
    await vi.waitFor(() => expect(deleted).toEqual(['w-offline-5678']))
  })
})

describe('adding people, virtual hosts and workers', () => {
  it('uses the page header with the way back to the list', async () => {
    const { AddUserPage } = await import('@/pages/admin/AddUserPage')
    const { AddVHostPage } = await import('@/pages/admin/AddVHostPage')
    const { AddWorkerPage } = await import('@/pages/admin/AddWorkerPage')
    const { unmount } = renderPage(<AddUserPage />)
    expect(await screen.findByRole('heading', { name: 'Add person' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to people/i })).toHaveAttribute('href', '/users')
    unmount()
    const second = renderPage(<AddVHostPage />)
    expect(await screen.findByRole('heading', { name: 'Add virtual host' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to settings/i })).toHaveAttribute('href', '/settings')
    second.unmount()
    renderPage(<AddWorkerPage />)
    expect(await screen.findByRole('heading', { name: 'Register worker' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to settings/i })).toHaveAttribute('href', '/settings')
  })
})

describe('My account', () => {
  it('is titled as its Settings row, with the person under it and the way back', async () => {
    server.use(http.get('/api/me', () => HttpResponse.json({ id: 'u1', username: 'gembit', full_name: 'Gembit S', email: 'g@example.com', role: 'Administrator', vhosts: [], two_factor_enabled: false })))
    const { ProfilePage } = await import('@/pages/system/ProfilePage')
    renderPage(<ProfilePage />)
    expect(await screen.findByRole('heading', { level: 1, name: 'My account' })).toBeInTheDocument()
    expect(screen.getByText('Gembit S · @gembit · Administrator')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /back to settings/i })).toHaveAttribute('href', '/settings')
    expect(screen.getByRole('tab', { name: 'Profile' })).toBeInTheDocument()
  })
})
