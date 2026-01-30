import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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

describe.skip('Workflows: import/save via modal', () => {
  it('imports a workflow JSON and shows it in the list (happy path)', async () => {
    localStorage.setItem('hermod_token', 'dummy.jwt.token')
    window.history.pushState({}, '', '/workflows')

    // In-memory items emulate server state
    const items: any[] = []

    server.use(
      http.get('/api/workers', () => HttpResponse.json({ data: [] })),
      http.get('/api/workflows', () => {
        return HttpResponse.json({ data: items, total: items.length })
      }),
      http.post('/api/workflows', async ({ request }) => {
        const text = await request.text()
        const body: any = text ? JSON.parse(text as any) : {}
        // Create minimal shape used by table
        const id = 'wf-' + (items.length + 1)
        items.push({
          id,
          name: body?.name || 'Imported Workflow',
          vhost: body?.vhost || 'default',
          worker_id: body?.worker_id || '',
          active: false,
          status: '',
          nodes: body?.nodes || [],
          edges: body?.edges || [],
        })
        return HttpResponse.json({ ok: true, id })
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

    // Initially no workflows
    await waitFor(() => {
      expect(screen.getByText(/No workflows found/i)).toBeInTheDocument()
    })

    // Open Import modal
    fireEvent.click(screen.getByRole('button', { name: /Import JSON/i }))

    // Paste minimal JSON
    const json = JSON.stringify({ name: 'Imported Workflow', nodes: [], edges: [] })
    const textbox = await screen.findByRole('textbox')
    fireEvent.change(textbox, { target: { value: json } })

    // Click Import button
    const importBtn = screen.getByRole('button', { name: /Import Workflow/i })
    fireEvent.click(importBtn)

    // Wait until server-side list has the new item and force a refetch to avoid flakiness
    await waitFor(() => {
      expect(items.length).toBe(1)
    }, { timeout: 5000 })
    await queryClient.invalidateQueries({ queryKey: ['workflows'] })
    await queryClient.refetchQueries({ queryKey: ['workflows'] })

    // Expect the empty state to disappear and the new workflow to be listed
    await waitFor(() => {
      expect(screen.queryByText(/No workflows found/i)).toBeNull()
    }, { timeout: 10000 })
    const nameCell = await screen.findByText(/Imported Workflow/i, {}, { timeout: 10000 })
    expect(nameCell).toBeInTheDocument()
  }, 10000)
})
