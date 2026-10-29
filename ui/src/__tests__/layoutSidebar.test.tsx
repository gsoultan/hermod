import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import { Layout } from '@/components/layout/Layout'

let pathname = '/'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params: _params, search: _search, ...props }: any) => <a href={to} {...props}>{children}</a>,
  useNavigate: () => () => {},
  useRouterState: () => ({ location: { pathname }, isLoading: false }),
}))

function renderLayout() {
  return render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ConfirmProvider>
          <VHostProvider>
            <Layout>
              <div>page body</div>
            </Layout>
          </VHostProvider>
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  pathname = '/'
  signInAs('Administrator', 'gembit')
  server.use(
    http.get('/api/vhosts', () => HttpResponse.json({ data: [{ name: 'prod' }, { name: 'staging' }] })),
    http.get('/api/dashboard/stats', () => HttpResponse.json({})),
    http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
    http.get('/api/version', () => HttpResponse.json({ version: 'v1.2.5' })),
    http.get('/api/instance', () => HttpResponse.json({})),
  )
})

describe('the sidebar', () => {
  it('offers the five places and nothing else at the top level', () => {
    renderLayout()
    const nav = screen.getByRole('navigation', { name: 'Main' })
    const labels = within(nav).getAllByRole('link').map((a) => a.textContent?.trim())
    expect(labels).toEqual(['Home', 'Flows', 'Connections', 'Activity', 'Settings'])
    expect(screen.queryByText('Main Menu')).not.toBeInTheDocument()
    expect(screen.queryByText('Administration')).not.toBeInTheDocument()
  })

  it('has no header bar: search and the account live at the bottom of the sidebar', () => {
    renderLayout()
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Search/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Account menu' })).toBeInTheDocument()
  })

  it('keeps the virtual host picker, theme and sign out inside the account menu', async () => {
    renderLayout()
    await userEvent.click(screen.getByRole('button', { name: 'Account menu' }))
    expect(await screen.findByText(/gembit · Administrator/)).toBeInTheDocument()
    // Mantine's dropdown stays display:none under jsdom, so query it hidden,
    // as the other menu tests in this suite do.
    expect(screen.getAllByLabelText('Virtual host').length).toBeGreaterThan(0)
    expect(screen.getByRole('menuitem', { name: /My account/, hidden: true })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Switch to dark mode/, hidden: true })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Sign out/, hidden: true })).toBeInTheDocument()
  })

  it('keeps the release tag one click away, in the account menu', async () => {
    renderLayout()
    await userEvent.click(screen.getByRole('button', { name: 'Account menu' }))
    expect(await screen.findByText(/v1\.2\.5/)).toBeInTheDocument()
  })

  it('folds to the icon rail while a flow is open', () => {
    pathname = '/workflows/abc'
    renderLayout()
    const nav = screen.getByRole('navigation', { name: 'Main' })
    expect(within(nav).queryByText('Connections')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Expand sidebar' })).toBeInTheDocument()
  })

  it('stays open on the flows list', () => {
    pathname = '/workflows'
    renderLayout()
    const nav = screen.getByRole('navigation', { name: 'Main' })
    expect(within(nav).getByText('Connections')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Collapse sidebar' })).toBeInTheDocument()
  })

  it('marks the place a retired URL now belongs to', () => {
    pathname = '/sources/new'
    renderLayout()
    const nav = screen.getByRole('navigation', { name: 'Main' })
    const active = within(nav).getAllByRole('link').filter((a) => a.getAttribute('aria-current') === 'page')
    expect(active.map((a) => a.textContent?.trim())).toEqual(['Connections'])
  })

  it('gives phones the same five places as bottom tabs', () => {
    renderLayout()
    const tabs = screen.getByRole('navigation', { name: 'Bottom tabs' })
    expect(within(tabs).getAllByRole('link').map((a) => a.textContent?.trim())).toEqual([
      'Home', 'Flows', 'Connections', 'Activity', 'Settings',
    ])
  })
})
