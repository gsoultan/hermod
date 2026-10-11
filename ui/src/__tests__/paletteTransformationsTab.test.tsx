import { fireEvent, render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, vi } from 'vitest'
import { SidebarDrawer } from '@/pages/workflows/WorkflowEditor/components/SidebarDrawer'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

// The Transformations tab rendered every palette category -- the source and
// sink ones included -- while the Sources and Sinks tabs each filtered to their
// own group, and the per-tab search counts did too. So the tab listed a second
// copy of every source and sink under a Transformations heading, and a search
// for "postgres" there showed "Nothing here matches" directly above the
// PostgreSQL entries it had just counted as absent.
describe('the palette Transformations tab', () => {
  const initialState = useWorkflowStore.getState()

  beforeEach(() => {
    // Installed plugins are fetched on mount; none are installed here.
    vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    // Transformations is locked until the canvas has a source.
    useWorkflowStore.setState({
      drawerOpened: true,
      drawerTab: 'transformations',
      nodes: [{ id: 'src', type: 'source', position: { x: 0, y: 0 }, data: { label: 'Orders' } }] as any,
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useWorkflowStore.setState(initialState, true)
  })

  const renderDrawer = () =>
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <SidebarDrawer onDragStart={() => {}} onAddItem={() => {}} sources={[]} sinks={[]} />
        </QueryClientProvider>
      </MantineProvider>
    )

  const transformationsTab = () => screen.getByRole('tabpanel', { name: 'Transformations' })

  it('lists transformations and nothing else', () => {
    renderDrawer()
    const tab = within(transformationsTab())
    expect(tab.getByText('Mapping', { exact: true })).toBeInTheDocument()
    // A source and a sink, each by the description only its own tab shows.
    expect(tab.queryByText('CDC & query capture from Postgres')).not.toBeInTheDocument()
    expect(tab.queryByText('Write rows to Postgres')).not.toBeInTheDocument()
  })

  it('points a search for a source at the Sources tab instead of listing it', async () => {
    renderDrawer()
    // One change event rather than typing it out: every keystroke re-rendered
    // the whole palette, and eight of them ran past the test timeout on a busy
    // CI runner. The search reads the final value either way.
    fireEvent.change(screen.getByLabelText('Search the node palette'), { target: { value: 'postgres' } })
    const tab = within(transformationsTab())
    expect(await tab.findByText(/Nothing here matches/)).toBeInTheDocument()
    expect(tab.getByText(/in Sources/)).toBeInTheDocument()
    expect(tab.queryByText('PostgreSQL', { exact: true })).not.toBeInTheDocument()
    // Filtering the full palette takes seconds under jsdom, close to the
    // default five-second limit even on an idle machine.
  }, 20_000)
})
