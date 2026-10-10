import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { server, signInAs } from '../test/setupTests'
import WorkflowsPage from '../pages/workflows/WorkflowsPage'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
  useNavigate: () => () => {},
}))

function renderPage() {
  return render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <VHostProvider>
          <ConfirmProvider>
            <WorkflowsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

/**
 * Using a sample-library template posts it to /api/workflows/import. A refused
 * import must be reported as a failure, never as "imported successfully".
 */
describe('Sample library import', () => {
  it('reports a refused import as a failure', async () => {
    signInAs('Editor')
    server.use(
      http.get('/api/workflows', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/workspaces', () => HttpResponse.json([])),
      http.get('/api/workers', () => HttpResponse.json({ data: [], total: 0 })),
      http.get('/api/templates', () =>
        HttpResponse.json([{ name: 'Starter', description: 'd', icon: 'IconGitBranch', color: 'blue', data: { workflow: { id: 'tpl-1', name: 'Starter' } } }]),
      ),
      http.post('/api/workflows/import', () => HttpResponse.json({ error: 'Failed to save workflow: duplicate' }, { status: 500 })),
    )
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /sample library/i }))
    await user.click(await screen.findByRole('button', { name: /use template/i }))

    expect(await screen.findByText('Import Failed', undefined, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.getByText(/duplicate/)).toBeInTheDocument()
    expect(screen.queryByText(/imported successfully/i)).toBeNull()
  }, 15000)
})
