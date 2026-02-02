import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { WorkflowHistoryModal } from '../components/WorkflowHistoryModal'
import { vi } from 'vitest'

describe('WorkflowHistoryModal', () => {
  it('lists workflow versions and allows rollback', async () => {
    let rollbackCalled = false;
    
    server.use(
      http.get('/api/workflows/wf1/versions', () => {
        return HttpResponse.json([
          {
            id: 'v1',
            workflow_id: 'wf1',
            version: 1,
            message: 'First version',
            created_at: new Date().toISOString(),
            created_by: 'admin'
          },
          {
            id: 'v2',
            workflow_id: 'wf1',
            version: 2,
            message: 'Second version',
            created_at: new Date().toISOString(),
            created_by: 'admin'
          }
        ])
      }),
      http.post('/api/workflows/wf1/rollback/1', () => {
        rollbackCalled = true;
        return HttpResponse.json({ ok: true })
      })
    )

    // Mock confirm dialog
    window.confirm = vi.fn().mockReturnValue(true);

    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <WorkflowHistoryModal 
            workflowId="wf1" 
            opened={true} 
            onClose={() => {}} 
          />
        </QueryClientProvider>
      </MantineProvider>
    )

    // Check if versions are listed
    expect(await screen.findByText(/v1/i)).toBeInTheDocument()
    expect(await screen.findByText(/First version/i)).toBeInTheDocument()
    expect(await screen.findByText(/v2/i)).toBeInTheDocument()
    expect(await screen.findByText(/Second version/i)).toBeInTheDocument()

    // Click restore on version 1
    const restoreBtns = await screen.findAllByText(/Restore/i)
    fireEvent.click(restoreBtns[1]) // Second button in list (older version)

    await waitFor(() => {
      expect(rollbackCalled).toBe(true)
    })
  })
})
