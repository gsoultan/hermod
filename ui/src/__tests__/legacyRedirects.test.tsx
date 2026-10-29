import { render, screen, waitFor } from '@testing-library/react'
import { RouterProvider } from '@tanstack/react-router'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { createAppRouter } from '../router'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '@/test/setupTests'

function renderApp(href: string) {
  window.history.pushState({}, '', href)
  const router = createAppRouter()
  render(
    <MantineProvider>
      <ConfirmProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <VHostProvider>
            <RouterProvider router={router} />
          </VHostProvider>
        </QueryClientProvider>
      </ConfirmProvider>
    </MantineProvider>,
  )
}

beforeEach(() => {
  signInAs('Administrator')
  server.use(
    http.get('/api/config/status', () => HttpResponse.json({ configured: true, user_setup: true })),
    http.get('/api/*', () => HttpResponse.json({ data: [], total: 0 })),
  )
})

/**
 * The sidebar lost its long list of pages, but none of those pages went away
 * and neither did their URLs: a bookmark, a link in an alert or a page that
 * still navigates to /logs lands in the hub that holds it now.
 */
describe('retired URLs', () => {
  it('sends /sources into Connections on the Sources tab', async () => {
    renderApp('/sources')
    await waitFor(() => expect(window.location.pathname).toBe('/connections'), { timeout: 5000 })
    expect(new URLSearchParams(window.location.search).get('tab')).toBe('sources')
    expect(await screen.findByRole('heading', { name: 'Connections' }, { timeout: 5000 })).toBeInTheDocument()
  })

  it('keeps the rest of the query string on the way to Activity', async () => {
    renderApp('/logs?workflow_id=wf-9')
    await waitFor(() => expect(window.location.pathname).toBe('/activity'), { timeout: 5000 })
    const search = new URLSearchParams(window.location.search)
    expect(search.get('tab')).toBe('logs')
    expect(search.get('workflow_id')).toBe('wf-9')
  })

  it('leaves the source form where it is', async () => {
    renderApp('/sources/new')
    await screen.findByRole('navigation', { name: 'Main' }, { timeout: 5000 })
    expect(window.location.pathname).toBe('/sources/new')
  })

  it('shows every role the Settings list, and only administrators the platform tabs', async () => {
    signInAs('Viewer')
    renderApp('/settings?tab=security')
    expect(await screen.findByRole('list', { name: 'Settings' }, { timeout: 5000 })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/settings')
  })
})
