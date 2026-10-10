import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { notificationsStore, cleanNotifications } from '@mantine/notifications'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowInitialization } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowInitialization'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { useWorkflowMutations } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowMutations'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {} }))

/**
 * The editor filled the canvas from the server once per workflow and then
 * ignored every later fetch of it. A History rollback refetched the workflow,
 * the canvas kept the graph from before the rollback, and the next Save wrote
 * that graph back over it. Reopening a workflow edited elsewhere did the same
 * with the cached copy.
 *
 * A newer server version now replaces the canvas when the canvas holds nothing
 * unsaved. When it does, the edits stay and the operator is told that saving
 * will overwrite the newer version.
 */

// The server marshals config maps with sorted keys, so its key order is not
// the editor's. That alone must never read as an edit.
const version = (label: string, extra: Record<string, unknown> = {}) => ({
  id: 'wf-1',
  name: 'Orders',
  vhost: 'default',
  nodes: [
    { id: 'src', type: 'source', ref_id: 'src-1', x: 0, y: 0, config: { label: 'orders', ref_id: 'src-1' } },
    { id: 'a', type: 'transformation', ref_id: '', x: 250, y: 0, config: { transType: 'set', label, ...extra } },
  ],
  edges: [{ id: 'e1', source_id: 'src', target_id: 'a' }],
})

const labelOf = (id: string) => useWorkflowStore.getState().nodes.find((n) => n.id === id)?.data.label

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderHook(() => useWorkflowInitialization('wf-1', 'default'), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
  return client
}

const newerVersionWarning = () =>
  notificationsStore.getState().notifications.find((n) => n.id === 'workflow-newer-version-wf-1')

describe('the editor and a newer server version', () => {
  let current: ReturnType<typeof version>

  beforeEach(() => {
    cleanNotifications()
    signInAs()
    useWorkflowStore.setState({ nodes: [], edges: [], name: '', selectedNode: null, testResults: null })
    current = version('Before')
    server.use(http.get('/api/workflows/wf-1', () => HttpResponse.json(current)))
  })

  it('replaces a canvas with no unsaved edits', async () => {
    const client = mount()
    await waitFor(() => expect(labelOf('a')).toBe('Before'))

    // A rollback, or an edit made somewhere else.
    current = version('Rolled back')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(labelOf('a')).toBe('Rolled back'))
    expect(newerVersionWarning()).toBeUndefined()
  })

  it('keeps unsaved edits and says a save would overwrite the newer version', async () => {
    const client = mount()
    await waitFor(() => expect(labelOf('a')).toBe('Before'))

    act(() => useWorkflowStore.getState().updateNodeConfig('a', { label: 'My edit' }))
    current = version('Rolled back')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(newerVersionWarning()).toBeDefined())
    expect(labelOf('a')).toBe('My edit')
  })

  it('takes its own save coming back as the new baseline, then reloads the next version', async () => {
    const client = mount()
    await waitFor(() => expect(labelOf('a')).toBe('Before'))

    act(() => useWorkflowStore.getState().updateNodeConfig('a', { label: 'Saved edit' }))
    // What the server returns after that save: the same graph, keys sorted.
    current = version('Saved edit')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))
    await waitFor(() =>
      expect((client.getQueryData(['workflow', 'wf-1']) as any)?.nodes[1].config.label).toBe('Saved edit')
    )
    expect(newerVersionWarning()).toBeUndefined()

    current = version('Rolled back')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(labelOf('a')).toBe('Rolled back'))
    expect(newerVersionWarning()).toBeUndefined()
  })

  it('does not count the server sorting config keys as an edit', async () => {
    current = version('Before', { zeta: 1, alpha: 2 })
    const client = mount()
    await waitFor(() => expect(labelOf('a')).toBe('Before'))

    current = version('After', { alpha: 2, zeta: 1 })
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(labelOf('a')).toBe('After'))
  })

  it('points the open node at its reloaded copy', async () => {
    const client = mount()
    await waitFor(() => expect(labelOf('a')).toBe('Before'))
    act(() => useWorkflowStore.setState({ selectedNode: useWorkflowStore.getState().nodes.find((n) => n.id === 'a')! }))

    current = version('Rolled back')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(useWorkflowStore.getState().selectedNode?.data.label).toBe('Rolled back'))
  })
})

describe('a save from this editor', () => {
  it('is the version a later rollback is compared against', async () => {
    cleanNotifications()
    signInAs()
    useWorkflowStore.setState({ nodes: [], edges: [], name: '', selectedNode: null, testResults: null, active: false })
    let current = version('Before')
    server.use(
      http.get('/api/workflows/wf-1', () => HttpResponse.json(current)),
      // Answers before the editor reads the workflow back, the way a save
      // that is not followed by a refetch leaves things.
      http.put('/api/workflows/wf-1', () => HttpResponse.json({ ok: true })),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    renderHook(() => useWorkflowInitialization('wf-1', 'default'), { wrapper })
    const { result } = renderHook(() => useWorkflowMutations('wf-1', false, [], () => {}), { wrapper })
    await waitFor(() => expect(labelOf('a')).toBe('Before'))

    act(() => useWorkflowStore.getState().updateNodeConfig('a', { label: 'Saved edit' }))
    await act(() => result.current.saveMutation.mutateAsync())

    current = version('Rolled back')
    await act(() => client.invalidateQueries({ queryKey: ['workflow', 'wf-1'] }))

    await waitFor(() => expect(labelOf('a')).toBe('Rolled back'))
    expect(newerVersionWarning()).toBeUndefined()
  })
})
