import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { WorkflowHistoryModal } from '@/components/modals/WorkflowHistoryModal'

/**
 * Cancelling the "Roll back workflow" confirmation returned from the mutation
 * without a value, which React Query counts as a success: the history closed
 * and the caller was told the rollback had happened when nothing was sent.
 */
describe('Restoring a workflow version', () => {
  it('does nothing when the confirmation is cancelled', async () => {
    signInAs('editor')
    let rollbacks = 0
    server.use(
      http.get('/api/workflows/wf-1/versions', () =>
        HttpResponse.json([{ id: 'v2', version: 2, created_at: '2026-10-01T00:00:00Z', created_by: 'ana', message: 'tweak' }])
      ),
      http.post('/api/workflows/wf-1/rollback/2', () => {
        rollbacks += 1
        return HttpResponse.json({ ok: true })
      })
    )
    const onClose = vi.fn()
    const onRollbackSuccess = vi.fn()
    const user = userEvent.setup()

    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <WorkflowHistoryModal workflowId="wf-1" opened onClose={onClose} onRollbackSuccess={onRollbackSuccess} />
          </ConfirmProvider>
        </QueryClientProvider>
      </MantineProvider>
    )

    await user.click(await screen.findByRole('button', { name: /restore/i }))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))

    // Give a wrongly-successful mutation the time to report itself.
    await new Promise((r) => setTimeout(r, 50))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument())
    expect(rollbacks).toBe(0)
    expect(onRollbackSuccess).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
