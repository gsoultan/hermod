import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse, delay } from 'msw'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import WorkflowsPage from '../pages/workflows/WorkflowsPage'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
  useNavigate: () => () => {},
}))

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <ConfirmProvider>
            <WorkflowsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

/**
 * Typing in a list's search box changes the React Query key, which normally
 * drops `data` to undefined until the new request lands — the table empties and
 * refills on every keystroke, and the page height jumps with it. Every list in
 * the app did this; none of them set `placeholderData`.
 */
describe('list search does not blank the table', () => {
  it('keeps the current rows on screen while a filtered search is in flight', async () => {
    let calls = 0
    server.use(
      http.get('/api/workflows', async () => {
        calls += 1
        if (calls > 1) await delay(300)
        return HttpResponse.json({
          data: calls === 1 ? [{ id: 'w1', name: 'Alpha Pipeline', active: false, nodes: [] }] : [],
          total: calls === 1 ? 1 : 0,
        })
      }),
      http.get('/api/workspaces', () => HttpResponse.json([])),
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 }))
    )
    signInAs()
    const user = userEvent.setup()

    renderPage()

    await screen.findByText('Alpha Pipeline', undefined, { timeout: 3000 })

    await user.type(screen.getByPlaceholderText('Search workflows...'), 'zzz')

    // Mid-flight: the old row must still be rendered rather than replaced by an
    // empty table.
    expect(screen.getByText('Alpha Pipeline')).toBeInTheDocument()

    // And once the empty result lands it does replace it.
    await waitFor(() => expect(screen.queryByText('Alpha Pipeline')).toBeNull(), { timeout: 4000 })
  }, 20000)
})
