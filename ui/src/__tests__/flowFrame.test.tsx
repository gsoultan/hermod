import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FlowHeader } from '@/components/flows/FlowHeader'
import { CanvasControls } from '@/components/flows/CanvasControls'
import { stepTitle } from '@/components/flows/stepTitle'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children, to, params, search: _s, ...rest }: any) => {
    const href = params ? Object.entries(params).reduce((p, [k, v]) => p.replace(`$${k}`, String(v)), to) : to
    return <a href={href} {...rest}>{children}</a>
  },
}))

const realMatchMedia = window.matchMedia
function asPhone() {
  window.matchMedia = ((query: string) => ({
    matches: query.includes('max-width'), media: query, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as any
}
afterEach(() => { window.matchMedia = realMatchMedia })

function header() {
  return render(
    <MantineProvider>
      <FlowHeader
        back={{ to: '/workflows', label: 'Back to flows' }}
        title={<h1>Orders</h1>}
        meta={<span>Off</span>}
        actions={<button>Edit flow</button>}
        menu={<button aria-label="More">⋯</button>}
      />
    </MantineProvider>,
  )
}

describe('the flow header', () => {
  it('is back, the name and its state line, then the actions and ⋯ on the same row', () => {
    header()
    expect(screen.getByRole('link', { name: 'Back to flows' })).toHaveAttribute('href', '/workflows')
    expect(screen.getByRole('heading', { name: 'Orders' })).toBeInTheDocument()
    expect(screen.getByText('Off')).toBeInTheDocument()
    expect(within(screen.getByRole('banner')).getByRole('button', { name: 'Edit flow' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Flow actions' })).not.toBeInTheDocument()
  })

  it('moves the actions to a bar along the bottom on a phone, and keeps ⋯ by the name', () => {
    asPhone()
    header()
    const bar = screen.getByRole('region', { name: 'Flow actions' })
    expect(within(bar).getByRole('button', { name: 'Edit flow' })).toBeInTheDocument()
    expect(within(screen.getByRole('banner')).queryByRole('button', { name: 'Edit flow' })).not.toBeInTheDocument()
    expect(within(screen.getByRole('banner')).getByRole('button', { name: 'More' })).toBeInTheDocument()
  })
})

describe('the canvas controls', () => {
  it('are zoom out, the real zoom, zoom in, Fit and Tidy up in one group', async () => {
    const calls: string[] = []
    render(
      <MantineProvider>
        <CanvasControls zoom={0.5} onZoomIn={() => calls.push('in')} onZoomOut={() => calls.push('out')} onFit={() => calls.push('fit')} onTidy={() => calls.push('tidy')} />
      </MantineProvider>,
    )
    const group = screen.getByRole('group', { name: 'Canvas controls' })
    expect(within(group).getByText('50%')).toBeInTheDocument()
    for (const name of ['Zoom out', 'Zoom in', 'Fit to view', 'Tidy up']) {
      await userEvent.click(within(group).getByRole('button', { name }))
    }
    expect(calls).toEqual(['out', 'in', 'fit', 'tidy'])
  })

  it('has no Tidy up where the layout cannot change', () => {
    render(<MantineProvider><CanvasControls zoom={1} onZoomIn={() => {}} onZoomOut={() => {}} onFit={() => {}} /></MantineProvider>)
    expect(screen.queryByRole('button', { name: 'Tidy up' })).not.toBeInTheDocument()
  })
})

describe('a step title', () => {
  it('prefers the label, then the connection it uses, then what kind of step it is, never an id', () => {
    expect(stepTitle({ label: 'Mask email' }, 'Mask', 'orders-db')).toBe('Mask email')
    expect(stepTitle({}, 'Source', 'orders-db')).toBe('orders-db')
    expect(stepTitle({}, 'Source')).toBe('Source')
    expect(stepTitle({ label: '  ' }, 'Sink')).toBe('Sink')
  })
})

describe('a step on the read-only canvas', () => {
  it('is its kind, the connection name when it has no label, and its connector, never the step id', async () => {
    const { ReactFlowProvider } = await import('@xyflow/react')
    const { detailNodeTypes } = await import('@/pages/workflows/WorkflowEditor/components/DetailFlowCanvas')
    const Source = detailNodeTypes.source as any
    render(
      <MantineProvider>
        <ReactFlowProvider>
          <Source id="n-src" type="source" data={{ label: '', type: 'postgres', connectionName: 'orders-db' }} />
        </ReactFlowProvider>
      </MantineProvider>,
    )
    expect(screen.getByText('orders-db')).toBeInTheDocument()
    expect(screen.getByText('Source')).toBeInTheDocument()
    expect(screen.getByText('postgres')).toBeInTheDocument()
    expect(screen.queryByText('n-src')).not.toBeInTheDocument()
  })
})
