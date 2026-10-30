import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { SchemaRegistryModal } from '@/components/modals/SchemaRegistryModal'
import type { LineageNode } from '@/lib/lineage'

/**
 * A schema change, analysed from the workflow editor: the AI verdict on the
 * change, and the sinks and workflows downstream of the workflow it feeds.
 */

const TOPIC = 'topic:kafka://kafka:9092/orders'
const INDEX = 'index:elasticsearch://es:9200/orders'
const capture: LineageNode = { id: 'workflow:capture', kind: 'workflow', label: 'capture orders', resolved: true, active: true }
const topic: LineageNode = { id: TOPIC, kind: 'topic', label: 'orders', system: 'kafka', locator: 'kafka:9092', resolved: true }
const indexer: LineageNode = { id: 'workflow:index', kind: 'workflow', label: 'index orders', resolved: true, active: true }
const index: LineageNode = { id: INDEX, kind: 'index', label: 'orders-search', system: 'elasticsearch', locator: 'es:9200', resolved: true }

function analyzeApi(downstream?: unknown, status = 200) {
  const asked: unknown[] = []
  server.use(
    http.get('/api/schemas', () => HttpResponse.json([])),
    http.post('/api/ai/analyze-schema', async ({ request }) => {
      asked.push(await request.json())
      if (status !== 200) return HttpResponse.json({ error: 'AI is not configured' }, { status })
      return HttpResponse.json({ breaking: true, suggestion: 'Map total before removing it.', impacted_nodes: ['t'], downstream })
    }),
  )
  return asked
}

async function analyze(workflowId?: string, oldSchema = '{"id":1,"total":2}') {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <SchemaRegistryModal opened onClose={() => {}} workflowId={workflowId} />
      </QueryClientProvider>
    </MantineProvider>,
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: /ai evolution/i }))
  const oldBox = screen.getByRole('textbox', { name: /old schema/i })
  await user.click(oldBox)
  await user.paste(oldSchema)
  await user.click(screen.getByRole('textbox', { name: /new schema/i }))
  await user.paste('{"id":1}')
  await user.click(screen.getByRole('button', { name: /analyze impact/i }))
}

describe('Schema change analysis', () => {
  it('asks about the open workflow and lists what is downstream of it', async () => {
    const asked = analyzeApi({
      root: capture,
      impacted: [
        { node: topic, depth: 1, path: [capture.id, TOPIC], relation: 'writes' },
        { node: indexer, depth: 2, path: [capture.id, TOPIC, indexer.id], relation: 'reads' },
        { node: index, depth: 3, path: [capture.id, TOPIC, indexer.id, INDEX], relation: 'writes' },
      ],
      workflows: 1,
      assets: 2,
    })
    await analyze('capture')

    expect(await screen.findByText(/breaking changes detected/i)).toBeInTheDocument()
    expect(asked).toEqual([{ workflow_id: 'capture', old_schema: { id: 1, total: 2 }, new_schema: { id: 1 } }])

    const list = await screen.findByRole('list', { name: /downstream of capture orders/i })
    const items = within(list).getAllByRole('listitem')
    expect(items.map((li) => within(li).getByTestId('impact-label').textContent)).toEqual(['orders', 'index orders', 'orders-search'])
    expect(items[2]).toHaveTextContent('capture orders → orders → index orders → orders-search')
    expect(screen.getByRole('heading', { name: /affected downstream/i })).toBeInTheDocument()
  })

  it('says when nothing downstream of the workflow is known', async () => {
    analyzeApi({ root: capture, impacted: [], workflows: 0, assets: 0 })
    await analyze('capture')
    expect(await screen.findByText(/no sink or workflow downstream of capture orders/i)).toBeInTheDocument()
  })

  it('does not claim a workflow before it has been saved', async () => {
    const asked = analyzeApi()
    await analyze(undefined)
    expect(await screen.findByText(/breaking changes detected/i)).toBeInTheDocument()
    expect(asked).toEqual([{ old_schema: { id: 1, total: 2 }, new_schema: { id: 1 } }])
    expect(screen.getByText(/save the workflow to see what is downstream of it/i)).toBeInTheDocument()
  })

  it('shows the server error instead of a verdict', async () => {
    analyzeApi(undefined, 500)
    await analyze('capture')
    expect(await screen.findByText(/ai is not configured/i)).toBeInTheDocument()
    expect(screen.queryByText(/safe to proceed/i)).not.toBeInTheDocument()
  })

  it('points at the schema that is not JSON instead of throwing', async () => {
    const asked = analyzeApi()
    await analyze('capture', '{id: 1')
    expect(await screen.findByText(/old schema is not valid json/i)).toBeInTheDocument()
    expect(asked).toEqual([])
  })
})
