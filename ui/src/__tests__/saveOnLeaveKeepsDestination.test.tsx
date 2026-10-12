import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowMutations } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowMutations'
import { hasUnsavedChanges } from '@/pages/workflows/WorkflowEditor/store/persistedSnapshot'

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))

/**
 * Saving a new workflow from the toolbar opens the workflow list. Saving it
 * from the leave-the-editor prompt must not: the operator already chose where
 * to go, and a second navigation would race theirs.
 */
function mountNew() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return renderHook(() => useWorkflowMutations('new', true, [], () => {}), {
    wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  }).result
}

describe('saving a new workflow', () => {
  beforeEach(() => {
    signInAs('Editor')
    navigate.mockReset()
    useWorkflowStore.setState({ name: 'Orders', nodes: [], edges: [], persistedBaseline: 'blank' })
    server.use(http.post('/api/workflows', () => HttpResponse.json({ id: 'wf-1' })))
  })

  it('opens the workflow list after a toolbar save', async () => {
    const result = mountNew()
    await act(() => result.current.saveMutation.mutateAsync(undefined))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/workflows' }))
  })

  it('leaves navigation to the prompt when saving on the way out', async () => {
    const result = mountNew()
    await act(() => result.current.saveMutation.mutateAsync({ leaving: true }))
    expect(navigate).not.toHaveBeenCalled()
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })
})
