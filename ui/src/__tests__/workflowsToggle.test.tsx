import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '../context/VHostContext'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import WorkflowsPage from '../pages/WorkflowsPage'
import { vi } from 'vitest'

// Avoid importing the full app router (heavy). Mock Link to a simple anchor.
vi.mock('@tanstack/react-router', () => {
  return {
    Link: (props: any) => <button {...props} />,
  }
})

describe('Workflows: toggle happy path', () => {
  it('toggles a workflow from Inactive to Active and refetches list', async () => {
    // Authenticated session
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
    window.history.pushState({}, '', '/workflows')

    // Simple in-memory state to reflect server-side active flag
    let active = false
    let postCalls = 0

    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [] })),
      http.get('/api/workflows', (_req) => {
        // Basic shape expected by page: { data: [...], total: number }
        return HttpResponse.json({
          data: [
            {
              id: 'wf1',
              name: 'Test Workflow',
              vhost: 'default',
              worker_id: '',
              active,
              status: '',
              nodes: [],
              edges: [],
            },
          ],
          total: 1,
        })
      }),
      http.post('/api/workflows/wf1/toggle', async () => {
        postCalls += 1
        active = !active
        return HttpResponse.json({ ok: true })
      })
    )

    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <WorkflowsPage />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    // Wait for initial list
    const row = await screen.findByRole('row', { name: /Test Workflow/i })
    expect(within(row).getByText(/Inactive/i)).toBeInTheDocument()

    // Click the Start workflow toggle
    const toggleBtn = within(row).getByRole('button', { name: /Start workflow/i })
    fireEvent.click(toggleBtn)

    // After mutation and refetch, badge should read Active
    await waitFor(() => {
      expect(within(row).getByText(/Active/i)).toBeInTheDocument()
    })

    // Verify POST was issued exactly once
    expect(postCalls).toBe(1)
  })
})
