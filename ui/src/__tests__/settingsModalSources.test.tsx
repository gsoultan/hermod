import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { vi } from 'vitest'
import type { Node } from '@xyflow/react'
import { WorkflowNodeSettingsModal } from '@/pages/workflows/WorkflowEditor/components/WorkflowNodeSettingsModal'

/**
 * Editors that pick a database source (db_lookup, sql, scd) read the
 * `sources` TransformationForm is given. The node settings modal passed it
 * the workflow's sinks instead, so a lookup opened there listed sinks as its
 * database sources.
 */
const noop = () => {}

describe('node settings modal', () => {
  it('offers the workflow sources, not its sinks, to a db_lookup', async () => {
    const node = {
      id: 'lk',
      type: 'transformation',
      data: { label: 'Lookup', ref_id: 'new', type: 'transformation', transType: 'db_lookup' },
    } as unknown as Node

    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MantineProvider>
          <WorkflowNodeSettingsModal
            opened
            onClose={noop}
            selectedNode={node}
            selectedNodeData={undefined}
            handleInlineSave={noop}
            handleTest={noop}
            handleRefreshFields={async () => {}}
            isRefreshing={false}
            vhost=""
            workerID=""
            availableFields={[]}
            incomingPayload={null}
            sources={[{ id: 'src1', name: 'customers-db', type: 'postgres', config: { use_cdc: 'false' } } as any]}
            sinks={[{ id: 'snk1', name: 'warehouse-sink', type: 'postgres', config: { use_cdc: 'false' } } as any]}
            upstreamSource={null}
            setSettingsOpened={noop}
            updateNodeConfig={vi.fn()}
            deleteNode={noop}
            sinkSchema={null}
          />
        </MantineProvider>
      </QueryClientProvider>,
    )

    await user.click(await screen.findByRole('combobox', { name: /database source/i }, { timeout: 10000 }))
    expect(screen.getByText(/customers-db/)).toBeInTheDocument()
    expect(screen.queryByText(/warehouse-sink/)).toBeNull()
  }, 30000)
})
