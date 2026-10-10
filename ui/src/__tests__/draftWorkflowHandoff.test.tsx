import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { useWorkflowInitialization } from '@/pages/workflows/WorkflowEditor/hooks/useWorkflowInitialization'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { persistedSnapshot } from '@/pages/workflows/WorkflowEditor/store/persistedSnapshot'
import { stashDraftWorkflow, takeDraftWorkflow } from '@/lib/draftWorkflow'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => () => {} }))

const draft = {
  id: 'should-not-survive',
  name: 'Triage support mail',
  vhost: 'support',
  active: true,
  tags: ['ai'],
  nodes: [
    { id: 'in', type: 'source', ref_id: 'src-1', x: 0, y: 0, config: { label: 'Inbox' } },
    { id: 'cls', type: 'ai_classify', x: 250, y: 0, config: { label: 'Classify' } },
  ],
  edges: [{ id: 'e1', source_id: 'in', target_id: 'cls' }],
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderHook(() => useWorkflowInitialization('new', 'default'), {
    wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  })
}

describe('opening a drafted workflow in the editor', () => {
  beforeEach(() => {
    signInAs('Editor')
    takeDraftWorkflow()
    useWorkflowStore.setState({ nodes: [], edges: [], name: '', tags: [], active: false })
    server.use(http.get('/api/workers/recommend', () => HttpResponse.json({})))
  })

  it('hands a draft over once', () => {
    stashDraftWorkflow(draft)
    expect(takeDraftWorkflow()?.name).toBe('Triage support mail')
    expect(takeDraftWorkflow()).toBeNull()
  })

  it('opens the draft as a new, stopped workflow with unsaved changes', async () => {
    stashDraftWorkflow(draft)
    mount()

    await waitFor(() => expect(useWorkflowStore.getState().name).toBe('Triage support mail'))
    const s = useWorkflowStore.getState()
    expect(s.nodes.map((n) => n.id)).toEqual(['in', 'cls'])
    expect(s.edges.map((e) => [e.source, e.target])).toEqual([['in', 'cls']])
    expect(s.vhost).toBe('support')
    expect(s.tags).toEqual(['ai'])
    // Never started from a draft, whatever the draft says.
    expect(s.active).toBe(false)
    // Nothing is saved yet: the canvas counts as unsaved edits.
    expect(persistedSnapshot(s)).not.toBe(s.persistedBaseline)
    // One use: the next new workflow starts blank.
    expect(takeDraftWorkflow()).toBeNull()
  })

  it('opens a blank workflow when there is no draft', async () => {
    mount()
    await waitFor(() => expect(useWorkflowStore.getState().name).toBe('New Workflow'))
    expect(useWorkflowStore.getState().nodes).toEqual([])
  })
})
