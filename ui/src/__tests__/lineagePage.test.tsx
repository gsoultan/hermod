import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { installReactFlowLayoutShims } from '../test/reactFlowCanvas'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { LineagePage } from '@/pages/monitoring/LineagePage'
import type { LineageGraph, LineageImpact } from '@/lib/lineage'

/**
 * Data lineage: the graph of a vhost's assets and workflows, and what is
 * downstream of the asset the user picks — "what breaks if I change this".
 */

installReactFlowLayoutShims()

const TABLE = 'table:postgres://db:5432/shop/public.orders'
const TOPIC = 'topic:kafka://kafka:9092/orders'
const INDEX = 'index:elasticsearch://es:9200/orders'

const graph: LineageGraph = {
  vhost: 'tenant-a',
  nodes: [
    { id: INDEX, kind: 'index', label: 'orders', system: 'elasticsearch', locator: 'es:9200', resolved: true },
    { id: TABLE, kind: 'table', label: 'public.orders', system: 'postgres', locator: 'db:5432/shop', resolved: true,
      refs: [{ kind: 'source', id: 'pg', name: 'orders db', type: 'postgres' }] },
    { id: TOPIC, kind: 'topic', label: 'orders', system: 'kafka', locator: 'kafka:9092', resolved: true },
    { id: 'workflow:capture', kind: 'workflow', label: 'capture orders', resolved: true, vhost: 'tenant-a', active: true },
    { id: 'workflow:index', kind: 'workflow', label: 'index orders', resolved: true, vhost: 'tenant-a', active: false },
  ],
  edges: [
    { from: TABLE, to: 'workflow:capture', relation: 'reads', workflow_id: 'capture', node_id: 's' },
    { from: 'workflow:capture', to: TOPIC, relation: 'writes', workflow_id: 'capture', node_id: 't' },
    { from: TOPIC, to: 'workflow:index', relation: 'reads', workflow_id: 'index', node_id: 's' },
    { from: 'workflow:index', to: INDEX, relation: 'writes', workflow_id: 'index', node_id: 't' },
  ],
  summary: { workflows: 2, assets: 3, unresolved: 0, edges: 4 },
}

const impactOfTable: LineageImpact = {
  vhost: 'tenant-a',
  root: graph.nodes[1],
  impacted: [
    { node: graph.nodes[3], depth: 1, path: [TABLE, 'workflow:capture'], relation: 'reads' },
    { node: graph.nodes[2], depth: 2, path: [TABLE, 'workflow:capture', TOPIC], relation: 'writes' },
    { node: graph.nodes[4], depth: 3, path: [TABLE, 'workflow:capture', TOPIC, 'workflow:index'], relation: 'reads' },
    { node: graph.nodes[0], depth: 4, path: [TABLE, 'workflow:capture', TOPIC, 'workflow:index', INDEX], relation: 'writes' },
  ],
  workflows: 2,
  assets: 2,
}

function lineageApi(opts: { graph?: LineageGraph; status?: number; impact?: LineageImpact } = {}) {
  const asked: string[] = []
  server.use(
    http.get('/api/vhosts/:vhost/lineage', ({ params }) => {
      asked.push(`graph ${params.vhost}`)
      if (opts.status) return HttpResponse.json({ error: opts.status === 403 ? 'Forbidden: you do not have access to this vhost' : 'Failed to build the lineage' }, { status: opts.status })
      return HttpResponse.json(opts.graph ?? graph)
    }),
    http.get('/api/vhosts/:vhost/lineage/impact', ({ params, request }) => {
      const node = new URL(request.url).searchParams.get('node') ?? ''
      asked.push(`impact ${params.vhost} ${node}`)
      if (opts.impact) return HttpResponse.json(opts.impact)
      if (node === TABLE) return HttpResponse.json(impactOfTable)
      const root = graph.nodes.find((n) => n.id === node)!
      return HttpResponse.json({ vhost: 'tenant-a', root, impacted: [], workflows: 0, assets: 0 })
    }),
  )
  return asked
}

function SelectVHost({ vhost }: { vhost: string }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts([vhost])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(vhost = 'tenant-a') {
  signInAs('Viewer')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <SelectVHost vhost={vhost} />
          <div style={{ width: 1200, height: 900 }}>
            <LineagePage />
          </div>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Lineage page', () => {
  it('loads the vhost graph and shows its counts', async () => {
    const asked = lineageApi()
    renderPage()
    expect(screen.getByRole('status', { name: /loading the lineage/i })).toBeInTheDocument()
    expect(await screen.findByText('2 flows · 3 assets')).toBeInTheDocument()
    expect(asked).toContain('graph tenant-a')
    // Every node is reachable from the keyboard.
    expect(screen.getByRole('button', { name: /show what depends on public\.orders/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /show what depends on capture orders/i })).toBeInTheDocument()
  })

  it('lists everything downstream of the chosen asset, nearest first, with the path', async () => {
    const asked = lineageApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('combobox', { name: /asset or workflow/i }))
    await user.click(await screen.findByRole('option', { name: /public\.orders/i, hidden: true }))

    const list = await screen.findByRole('list', { name: /downstream of public\.orders/i })
    const items = within(list).getAllByRole('listitem')
    expect(items.map((li) => within(li).getByTestId('impact-label').textContent)).toEqual([
      'capture orders', 'orders', 'index orders', 'orders',
    ])
    expect(items[3]).toHaveTextContent('public.orders → capture orders → orders → index orders → orders')
    expect(screen.getByText(/2 workflows and 2 assets are downstream/i)).toBeInTheDocument()
    expect(asked).toContain(`impact tenant-a ${TABLE}`)
  })

  it('picks an asset from the graph with the keyboard', async () => {
    lineageApi()
    renderPage()
    const user = userEvent.setup()
    const node = await screen.findByRole('button', { name: /show what depends on public\.orders/i })
    node.focus()
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('list', { name: /downstream of public\.orders/i })).toBeInTheDocument()
    expect(node).toHaveAttribute('aria-pressed', 'true')
  })

  it('says when nothing is downstream', async () => {
    lineageApi()
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /show what depends on orders.*elasticsearch/i }))
    expect(await screen.findByText(/nothing reads this downstream/i)).toBeInTheDocument()
  })

  it('explains an empty vhost', async () => {
    lineageApi({ graph: { vhost: 'tenant-a', nodes: [], edges: [], summary: { workflows: 0, assets: 0, unresolved: 0, edges: 0 } } })
    renderPage()
    expect(await screen.findByText('No lineage to show yet')).toBeInTheDocument()
  })

  it('says when the user has no access to the vhost', async () => {
    lineageApi({ status: 403 })
    renderPage()
    expect(await screen.findByRole('alert', { name: /no access to tenant-a/i })).toBeInTheDocument()
  })

  it('shows a failure to load', async () => {
    lineageApi({ status: 500 })
    renderPage()
    const alert = await screen.findByRole('alert', { name: /could not be loaded/i })
    expect(alert).toHaveTextContent('Failed to build the lineage')
    await waitFor(() => expect(screen.queryByRole('status', { name: /loading the lineage/i })).not.toBeInTheDocument())
  })
})
