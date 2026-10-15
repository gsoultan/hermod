import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * Workflow settings: a workflow keeps the messages it cannot deliver on the
 * Dead Letters page, or leaves them on the source. There is no dead letter
 * sink to pick any more.
 */
describe('workflow settings: dead letters', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({ drawerOpened: true, drawerTab: 'settings' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  const renderDrawer = () =>
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[{ id: 's1', name: 'billing-dlq', type: 'postgres' }]} />
        </QueryClientProvider>
      </MantineProvider>,
    )

  it('keeps dead letters on the page by default, and offers no sink to pick', () => {
    expect(initialState.deadLetters).toBe(true)
    renderDrawer()

    expect(screen.getByRole('switch', { name: /keep failed messages on the dead letters page/i })).toBeChecked()
    expect(screen.queryByRole('combobox', { name: /dead letter sink/i })).not.toBeInTheDocument()
  })

  it('switching it off leaves failed messages on the source and turns off replay on startup', async () => {
    const user = userEvent.setup()
    useWorkflowStore.setState({ prioritizeDLQ: true })
    renderDrawer()

    const replay = screen.getByRole('checkbox', { name: /replay dead letters on startup/i })
    expect(replay).toBeEnabled()

    await user.click(screen.getByRole('switch', { name: /keep failed messages on the dead letters page/i }))

    expect(useWorkflowStore.getState().deadLetters).toBe(false)
    expect(screen.getByText(/stay on the source and are retried/i)).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: /replay dead letters on startup/i })).toBeDisabled()
  })
})
