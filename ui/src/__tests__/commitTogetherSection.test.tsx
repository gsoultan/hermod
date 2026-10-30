import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect, beforeEach } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { CommitTogetherSection } from '@/components/workflow/Sink/CommitTogetherSection'
import type { EditorNode } from '@/utils/commitTogether'

/**
 * "Commit together" on a sink node.
 *
 * The section sits above the sink's own settings in the node drawer. It is
 * shown only on a database that can take part, writes the transaction name
 * into the node's config (not the sink record, which other workflows share),
 * and says before the save does whether the transaction can run.
 */
function serve({ logReady = true, logProblem = '' } = {}) {
  server.use(
    http.get('/api/sinks/capabilities/two-phase', () =>
      HttpResponse.json({
        types: ['postgres', 'yugabyte', 'mysql', 'mariadb'],
        log_ready: logReady,
        log_problem: logProblem,
      }),
    ),
  )
}

const sink = (id: string, type: string, extra: Record<string, any> = {}): EditorNode => ({
  id,
  type: 'sink',
  data: { label: `DB ${id}`, type, ref_id: `ref-${id}`, ...extra },
})

function Harness({ nodeId, initial }: { nodeId: string; initial: EditorNode[] }) {
  const [nodes, setNodes] = useState(initial)
  const me = nodes.find((n) => n.id === nodeId)!
  return (
    <CommitTogetherSection
      nodeId={nodeId}
      data={me.data}
      nodes={nodes}
      updateNodeConfig={(id, config) =>
        setNodes((ns) => ns.map((n) => (n.id === id ? { ...n, data: { ...n.data, ...config } } : n)))
      }
    />
  )
}

const ui = (nodeId: string, nodes: EditorNode[]) =>
  render(
    <MantineProvider>
      <Harness nodeId={nodeId} initial={nodes} />
    </MantineProvider>,
  )

describe('the Commit together section', () => {
  beforeEach(() => serve())

  it('is not offered on a sink that cannot take part', async () => {
    serve()
    ui('K', [sink('K', 'kafka')])
    // Give the capability request time to answer; the section must stay empty.
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.queryByTestId('commit-together')).not.toBeInTheDocument()
  })

  it('is off by default and says what off means', async () => {
    ui('A', [sink('A', 'postgres')])
    const toggle = await screen.findByRole('switch', { name: /commit together with other databases/i })
    expect(toggle).not.toBeChecked()
    expect(screen.getByText(/this sink commits on its own/i)).toBeInTheDocument()
  })

  it('turning it on asks for a transaction name and writes it to the node', async () => {
    const user = userEvent.setup()
    ui('A', [sink('A', 'postgres'), sink('B', 'mysql')])
    await user.click(await screen.findByRole('switch', { name: /commit together/i }))
    const name = await screen.findByPlaceholderText('orders')
    await user.type(name, 'orders')
    await waitFor(() => expect(name).toHaveValue('orders'))
    // Alone so far: B has not joined.
    expect(await screen.findByText(/no other sink node is in "orders" yet/i)).toBeInTheDocument()
    expect(screen.getByText(/not ready yet/i)).toBeInTheDocument()
  })

  it('offers the names already in the workflow and joins one when picked', async () => {
    const user = userEvent.setup()
    ui('A', [sink('A', 'postgres'), sink('B', 'mysql', { transaction: 'orders' }), sink('C', 'postgres', { transaction: 'audit' })])
    await user.click(await screen.findByRole('switch', { name: /commit together/i }))
    const name = await screen.findByPlaceholderText('orders')
    await user.type(name, 'o')
    await user.click(await screen.findByRole('option', { name: 'orders', hidden: true }))
    await waitFor(() => expect(name).toHaveValue('orders'))
    expect(await screen.findByText(/DB B/)).toBeInTheDocument()
  })

  it('lists the other members and reports ready when two databases can commit', async () => {
    ui('A', [sink('A', 'postgres', { transaction: 'orders' }), sink('B', 'mysql', { transaction: 'orders' })])
    expect(await screen.findByRole('switch', { name: /commit together/i })).toBeChecked()
    expect(await screen.findByText(/DB B/)).toBeInTheDocument()
    expect(screen.getByText(/MySQL/)).toBeInTheDocument()
    expect(await screen.findByText(/ready to run/i)).toBeInTheDocument()
  })

  it('names a member whose database cannot commit together', async () => {
    ui('A', [sink('A', 'postgres', { transaction: 'orders' }), sink('K', 'kafka', { transaction: 'orders' })])
    expect(await screen.findByText(/DB K is a kafka sink/i)).toBeInTheDocument()
    expect(screen.getByText(/not ready yet/i)).toBeInTheDocument()
  })

  it('shows why the transaction log is not ready, in the server’s words', async () => {
    serve({ logReady: false, logProblem: 'a state store is required so the coordinator’s log survives a restart' })
    ui('A', [sink('A', 'postgres', { transaction: 'orders' }), sink('B', 'mysql', { transaction: 'orders' })])
    expect(await screen.findByText(/state store is required/i)).toBeInTheDocument()
    expect(screen.getByText(/not ready yet/i)).toBeInTheDocument()
  })

  it('turning it off clears the name', async () => {
    const user = userEvent.setup()
    ui('A', [sink('A', 'postgres', { transaction: 'orders' }), sink('B', 'mysql', { transaction: 'orders' })])
    const toggle = await screen.findByRole('switch', { name: /commit together/i })
    await user.click(toggle)
    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(screen.queryByPlaceholderText('orders')).not.toBeInTheDocument()
  })
})
