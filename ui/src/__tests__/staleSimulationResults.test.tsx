import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse, delay } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowMutations } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowMutations'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {} }))

/**
 * Every simulation run stored its results when it finished, whichever run it
 * was. The refresh icon is disabled while a run is pending, but the Test
 * hotkey is not, so a slow Test that finished after a refresh put the old
 * fields back over the new ones. Only the latest run's results are kept.
 */
describe('simulation results', () => {
  beforeEach(() => {
    signInAs()
    useWorkflowStore.setState({ nodes: [], edges: [], testResults: null, name: 'Orders' })
    server.use(
      http.post('/api/workflows/test', async ({ request }) => {
        const body = (await request.json()) as any
        if (body.message?.run === 'old') {
          await delay(150)
          return HttpResponse.json([{ node_id: 'a', payload: { run: 'old' } }])
        }
        return HttpResponse.json([{ node_id: 'a', payload: { run: 'new' } }])
      })
    )
  })

  const mount = () => {
    const client = new QueryClient()
    return renderHook(() => useWorkflowMutations('wf-1', false, [], () => {}), {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })
  }

  const shown = () => useWorkflowStore.getState().testResults?.[0]?.payload?.run

  it('a slow run that finishes last does not replace a newer run', async () => {
    const { result } = mount()

    // A Test started from the hotkey, then a refresh's run started after it.
    act(() => result.current.handleTest({ run: 'old' }))
    await act(() => result.current.testMutation.mutateAsync({ input: { run: 'new' }, quiet: true }))
    expect(shown()).toBe('new')

    // Let the older run settle.
    await act(() => delay(300))
    await waitFor(() => expect(result.current.testMutation.isPending).toBe(false))

    expect(shown()).toBe('new')
  })

  it('stores the result of a single run', async () => {
    const { result } = mount()

    await act(() => result.current.testMutation.mutateAsync({ input: { run: 'new' }, quiet: true }))

    expect(shown()).toBe('new')
  })
})
