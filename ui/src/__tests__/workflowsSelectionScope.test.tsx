import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import WorkflowsPage from '../pages/workflows/WorkflowsPage'

// These render whole pages; on a loaded machine the 5s default is too short.
vi.setConfig({ testTimeout: 30000 })

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
  useNavigate: () => () => {},
}))

/**
 * Batch actions act on the selected ids. The selection used to outlive the
 * rows it was made on: tick a workflow, page on or search, and "Batch Actions
 * (1) → Delete selected" deleted a workflow no longer on screen.
 */
const wf = (id: string, name: string) => ({
  id, name, vhost: 'default', worker_id: '', active: false, status: '', nodes: [], edges: [],
})

const renderPage = () =>
  render(
    <MantineProvider>
      <ConfirmProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <VHostProvider>
            <WorkflowsPage />
          </VHostProvider>
        </QueryClientProvider>
      </ConfirmProvider>
    </MantineProvider>,
  )

describe('workflow list selection', () => {
  beforeEach(() => {
    signInAs()
    window.history.pushState({}, '', '/workflows')
    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [] })),
      http.get('/api/workspaces', () => HttpResponse.json([])),
      http.get('/api/workflows', ({ request }) => {
        const url = new URL(request.url)
        const page = url.searchParams.get('page')
        const search = url.searchParams.get('search')
        if (search) return HttpResponse.json({ data: [wf('wf9', 'Matched Workflow')], total: 1 })
        if (page === '2') return HttpResponse.json({ data: [wf('wf2', 'Second Page Workflow')], total: 31 })
        return HttpResponse.json({ data: [wf('wf1', 'First Page Workflow')], total: 31 })
      }),
    )
  })

  it('is cleared when the page changes', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('checkbox', { name: 'Select workflow First Page Workflow' }, { timeout: 10000 }))
    expect(screen.getByRole('button', { name: /batch actions \(1\)/i })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '2' }))
    await screen.findByText('Second Page Workflow', {}, { timeout: 10000 })

    expect(screen.queryByRole('button', { name: /batch actions/i })).toBeNull()
  })

  it('is cleared when the search changes', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('checkbox', { name: 'Select workflow First Page Workflow' }, { timeout: 10000 }))
    expect(screen.getByRole('button', { name: /batch actions \(1\)/i })).toBeInTheDocument()

    await user.type(screen.getByPlaceholderText('Search workflows...'), 'matched')
    await screen.findByText('Matched Workflow', {}, { timeout: 10000 })

    await waitFor(() => expect(screen.queryByRole('button', { name: /batch actions/i })).toBeNull())
  })
})
