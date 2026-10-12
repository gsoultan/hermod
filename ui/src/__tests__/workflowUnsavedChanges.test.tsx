import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import {
  Link, Outlet, RouterProvider, createBrowserHistory, createMemoryHistory, createRootRoute, createRoute, createRouter,
} from '@tanstack/react-router'
import { vi } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { hasUnsavedChanges, persistedSnapshot } from '@/pages/workflows/WorkflowEditor/store/persistedSnapshot'
import { UnsavedChangesGuard } from '@/pages/workflows/WorkflowEditor/components/UnsavedChangesGuard'

const sourceNode = { id: 'in', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Inbox', ref_id: 's1' } }

// A loaded, saved workflow: the canvas matches its baseline.
function loadSaved() {
  useWorkflowStore.setState({ name: 'Orders', nodes: [sourceNode] as any, edges: [] })
  useWorkflowStore.setState({ persistedBaseline: persistedSnapshot(useWorkflowStore.getState()) })
}

function rename(name: string) {
  useWorkflowStore.setState({ name })
}

describe('hasUnsavedChanges', () => {
  beforeEach(loadSaved)

  it('is false while nothing has been loaded yet', () => {
    useWorkflowStore.setState({ persistedBaseline: null, name: 'anything' })
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })

  it('is false for a canvas that matches what was saved', () => {
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })

  it('is true after an edit', () => {
    rename('Orders v2')
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(true)
  })

  it('is false again once the edit is undone', () => {
    rename('Orders v2')
    rename('Orders')
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })

  it('ignores selecting a node, which is not an edit', () => {
    useWorkflowStore.setState({ nodes: [{ ...sourceNode, selected: true, measured: { width: 10, height: 10 } }] as any })
    expect(hasUnsavedChanges(useWorkflowStore.getState())).toBe(false)
  })
})

function renderEditor(onSave: () => Promise<unknown>, history = createMemoryHistory({ initialEntries: ['/workflows/w1'] })) {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const editor = createRoute({
    getParentRoute: () => rootRoute,
    path: '/workflows/$id',
    component: () => (
      <>
        <Link to="/workflows">All workflows</Link>
        <UnsavedChangesGuard onSave={onSave} />
      </>
    ),
  })
  const list = createRoute({ getParentRoute: () => rootRoute, path: '/workflows', component: () => <p>Workflow list</p> })
  const router = createRouter({
    routeTree: rootRoute.addChildren([editor, list]),
    history,
  })
  render(
    <MantineProvider>
      <RouterProvider router={router} />
    </MantineProvider>,
  )
  return router
}

describe('leaving the workflow editor', () => {
  beforeEach(loadSaved)

  it('leaves without asking when nothing changed', async () => {
    const router = renderEditor(vi.fn())
    await userEvent.click(await screen.findByText('All workflows'))
    expect(await screen.findByText('Workflow list')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/workflows')
  })

  it('leaves without asking when an edit was undone', async () => {
    renderEditor(vi.fn())
    rename('Orders v2')
    rename('Orders')
    await userEvent.click(await screen.findByText('All workflows'))
    expect(await screen.findByText('Workflow list')).toBeInTheDocument()
  })

  it('asks before leaving with unsaved changes, and Cancel stays', async () => {
    const router = renderEditor(vi.fn())
    rename('Orders v2')
    await userEvent.click(await screen.findByText('All workflows'))
    expect(await screen.findByText('Save changes to this workflow?')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Save changes to this workflow?')).not.toBeInTheDocument())
    expect(router.state.location.pathname).toBe('/workflows/w1')
    expect(useWorkflowStore.getState().name).toBe('Orders v2')
  })

  it('Discard leaves without saving', async () => {
    const onSave = vi.fn()
    renderEditor(onSave)
    rename('Orders v2')
    await userEvent.click(await screen.findByText('All workflows'))
    await userEvent.click(await screen.findByRole('button', { name: 'Discard' }))
    expect(await screen.findByText('Workflow list')).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('Save saves, then leaves', async () => {
    const onSave = vi.fn(async () => {
      useWorkflowStore.setState({ persistedBaseline: persistedSnapshot(useWorkflowStore.getState()) })
    })
    renderEditor(onSave)
    rename('Orders v2')
    await userEvent.click(await screen.findByText('All workflows'))
    await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Workflow list')).toBeInTheDocument()
    expect(onSave).toHaveBeenCalledOnce()
  })

  it('stays on the editor when the save fails', async () => {
    const onSave = vi.fn(async () => { throw new Error('boom') })
    const router = renderEditor(onSave)
    rename('Orders v2')
    await userEvent.click(await screen.findByText('All workflows'))
    await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(screen.getByText('Save changes to this workflow?')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/workflows/w1')
  })

  it('asks the browser to confirm closing the tab only when there are unsaved changes', async () => {
    // Only the browser history listens for the tab closing.
    window.history.replaceState(null, '', '/workflows/w1')
    renderEditor(vi.fn(), createBrowserHistory())
    await screen.findByText('All workflows')

    const clean = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(clean)
    expect(clean.defaultPrevented).toBe(false)

    rename('Orders v2')
    const dirty = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(dirty)
    expect(dirty.defaultPrevented).toBe(true)
  })
})
