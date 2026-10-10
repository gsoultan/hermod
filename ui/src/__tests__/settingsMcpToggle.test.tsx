import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

describe('workflow settings: Expose to MCP clients', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    useWorkflowStore.setState({ drawerOpened: true, drawerTab: 'settings', tags: ['billing'] })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  it('writes the mcp tag into the workflow being edited', async () => {
    const user = userEvent.setup()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[]} />
        </QueryClientProvider>
      </MantineProvider>,
    )

    await user.click(screen.getByRole('switch', { name: /expose to mcp clients/i }))
    expect(useWorkflowStore.getState().tags).toEqual(['billing', 'mcp'])

    await user.click(screen.getByRole('switch', { name: /expose to mcp clients/i }))
    expect(useWorkflowStore.getState().tags).toEqual(['billing'])
  })
})
