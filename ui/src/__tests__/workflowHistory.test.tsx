import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { server } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { WorkflowHistoryModal } from '@/components/modals/WorkflowHistoryModal'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'

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

    // Rollback now goes through the in-app ConfirmProvider rather than
    // window.confirm, so the test must click the real dialog.

    const queryClient = new QueryClient()
    render(
      <MantineProvider>
        <ConfirmProvider>
        <QueryClientProvider client={queryClient}>
          <WorkflowHistoryModal 
            workflowId="wf1" 
            opened={true} 
            onClose={() => {}} 
          />
        </QueryClientProvider>
      </ConfirmProvider>
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

    // Confirm in the dialog; without this the rollback must NOT fire.
    fireEvent.click(await screen.findByRole('button', { name: 'Roll back' }))

    await waitFor(() => {
      expect(rollbackCalled).toBe(true)
    })
  })
})
