import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'
import { EditorToolbar } from '@/pages/workflows/WorkflowEditor/components/EditorToolbar'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...rest }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...rest}>{children}</a>
  },
  useNavigate: () => () => {},
}))

const noop = () => {}
const handlers = {
  onSave: vi.fn(), onTest: vi.fn(), onConfigureTest: vi.fn(), onToggle: vi.fn(), onRebuild: vi.fn(), onClearTest: vi.fn(),
  onAutoLayout: vi.fn(), onSearch: vi.fn(), onExport: vi.fn(), onClone: vi.fn(), onDelete: vi.fn(),
}

function renderToolbar(over: Partial<React.ComponentProps<typeof EditorToolbar>> = {}) {
  return render(
    <MantineProvider>
      <EditorToolbar
        id="wf-1"
        isNew={false}
        {...handlers}
        isSaving={false}
        isTesting={false}
        isToggling={false}
        zoom={1}
        zoomIn={noop}
        zoomOut={noop}
        fitView={noop}
        vhosts={[{ name: 'prod' }]}
        workers={[]}
        workflowStatus="running"
        {...over}
      />
    </MantineProvider>,
  )
}

beforeEach(() => {
  useWorkflowStore.setState({ name: 'Orders', active: false, testResults: null, dryRun: false })
})

describe('the editor toolbar', () => {
  it('is back, the name, its state, Test ▾, Turn on and ⋯, and nothing else', async () => {
    renderToolbar()
    expect(screen.getByRole('link', { name: 'Back to the flow' })).toHaveAttribute('href', '/workflows/wf-1')
    expect(screen.getByRole('textbox', { name: 'Flow name' })).toHaveValue('Orders')
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Turn on' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Tools/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Inspect/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Workflow panel' })).not.toBeInTheDocument()
  })

  it('folds the test actions under Test ▾', async () => {
    renderToolbar()
    await userEvent.click(screen.getByRole('button', { name: 'More test actions' }))
    for (const item of ['Run simulation', 'Configure & run…', 'Rebuild projections', 'Dry-run mode']) {
      expect(await screen.findByRole('menuitem', { name: item, hidden: true })).toBeInTheDocument()
    }
  })

  it('offers Clear simulation once a test has run', async () => {
    useWorkflowStore.setState({ testResults: { ok: true } as any })
    renderToolbar()
    await userEvent.click(screen.getByRole('button', { name: 'More test actions' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Clear simulation', hidden: true }))
    expect(handlers.onClearTest).toHaveBeenCalled()
  })

  it('folds Tools and Inspect into ⋯, with nothing dropped', async () => {
    renderToolbar()
    await userEvent.click(screen.getByRole('button', { name: 'More' }))
    for (const item of ['Schema registry', 'AI generator', 'Data pulse', 'Live stream', 'Trace analysis', 'Compliance report', 'Version history', 'Flow settings…', 'Export JSON', 'Clone', 'Delete flow']) {
      expect(await screen.findByRole('menuitem', { name: new RegExp(`^${item.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`), hidden: true })).toBeInTheDocument()
    }
  })

  it('says Turn off when the flow is on, and hides Turn on for a new flow', () => {
    useWorkflowStore.setState({ active: true })
    renderToolbar()
    expect(screen.getByRole('button', { name: 'Turn off' })).toBeInTheDocument()
  })
})
