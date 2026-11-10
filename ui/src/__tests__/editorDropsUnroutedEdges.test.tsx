import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { notificationsStore, cleanNotifications } from '@mantine/notifications'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowInitialization } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowInitialization'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { hasUnsavedChanges } from '@/pages/workflows/WorkflowEditor/store/persistedSnapshot'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {} }))

/**
 * A workflow saved before edges followed their case can hold an edge on a
 * case's old name. The canvas never drew it and the server now refuses to save
 * it, so the editor drops it on open, says so, and leaves it as an unsaved
 * change: one save clears it.
 */

const workflow = (edges: any[]) => ({
  id: 'wf-1',
  name: 'Requisitions',
  vhost: 'default',
  nodes: [
    { id: 'src', type: 'source', ref_id: 'src-1', x: 0, y: 0, config: { label: 'in', ref_id: 'src-1' } },
    { id: 'sw', type: 'switch', ref_id: '', x: 250, y: 0, config: { label: 'Pipeline Category', cases: [{ label: 'APPROVED' }] } },
    { id: 'out', type: 'sink', ref_id: 'snk-1', x: 500, y: 0, config: { label: 'out', ref_id: 'snk-1' } },
  ],
  edges: [{ id: 'e0', source_id: 'src', target_id: 'sw' }, ...edges],
})

const branch = (id: string, label: string) => ({
  id, source_id: 'sw', target_id: 'out', source_handle: label, config: { label },
})

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderHook(() => useWorkflowInitialization('wf-1', 'default'), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
}

const edgeIds = () => useWorkflowStore.getState().edges.map((e) => e.id)
const notice = () =>
  notificationsStore.getState().notifications.find((n) => n.id === 'workflow-unrouted-edges-wf-1')

describe('opening a workflow with an edge on a renamed case', () => {
  beforeEach(() => {
    cleanNotifications()
    signInAs()
    useWorkflowStore.setState({ nodes: [], edges: [], name: '', selectedNode: null, testResults: null })
  })

  it('drops the edge, says so, and leaves the removal to save', async () => {
    const wf = workflow([branch('stale', 'APPROVE'), branch('ok', 'APPROVED')])
    server.use(http.get('/api/workflows/wf-1', () => HttpResponse.json(wf)))
    mount()

    await waitFor(() => expect(edgeIds()).toEqual(['e0', 'ok']))
    expect(notice()).toBeDefined()
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(true)
  })

  it('changes nothing when every edge names a case', async () => {
    const wf = workflow([branch('ok', 'APPROVED'), branch('d', 'default')])
    server.use(http.get('/api/workflows/wf-1', () => HttpResponse.json(wf)))
    mount()

    await waitFor(() => expect(edgeIds()).toEqual(['e0', 'ok', 'd']))
    expect(notice()).toBeUndefined()
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })
})
