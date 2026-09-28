import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Node } from '@xyflow/react'
import { WorkflowNodeSettingsModal } from '@/pages/workflows/WorkflowEditor/components/WorkflowNodeSettingsModal'
import { FilterDataConfig } from '@/components/workflow/Transformation/configs/data/FilterDataConfig'

// An If node with no conditions sends every message down its TRUE branch. Its
// editor shared the Filter's empty state, "All messages will pass", which is
// true of a filter and says nothing about which branch an If node takes. The If
// node is configured from the settings modal a click on it opens, so that is
// where this starts.
const noop = () => {}

const renderIfNode = (data: Record<string, any>) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MantineProvider>
        <WorkflowNodeSettingsModal
          opened
          onClose={noop}
          selectedNode={{ id: 'if', type: 'condition', position: { x: 0, y: 0 }, data } as unknown as Node}
          selectedNodeData={undefined}
          handleInlineSave={noop}
          handleTest={noop}
          handleRefreshFields={async () => {}}
          isRefreshing={false}
          vhost=""
          workerID=""
          availableFields={[]}
          incomingPayload={null}
          sinks={[]}
          upstreamSource={null}
          setSettingsOpened={noop}
          updateNodeConfig={vi.fn()}
          deleteNode={noop}
          sinkSchema={null}
        />
      </MantineProvider>
    </QueryClientProvider>
  )

describe('an If node with no conditions', () => {
  it('says every message takes the TRUE branch', async () => {
    renderIfNode({ label: 'Condition (If)', ref_id: 'new', type: 'condition' })

    expect(await screen.findByText(/every message takes the TRUE branch/i)).toBeTruthy()
    expect(screen.queryByText(/all messages will pass/i)).toBeNull()
  })

  it('stops saying so once it has a condition', async () => {
    renderIfNode({
      label: 'Condition (If)',
      ref_id: 'new',
      type: 'condition',
      conditions: [{ field: 'status', operator: '=', value: 'active' }],
    })

    expect(await screen.findByDisplayValue('status')).toBeTruthy()
    expect(screen.queryByText(/every message takes the TRUE branch/i)).toBeNull()
  })
})

describe('a Filter with no conditions', () => {
  it('still says every message passes', async () => {
    render(
      <MantineProvider>
        <FilterDataConfig config={{}} updateNodeConfig={vi.fn()} nodeId="f" availableFields={[]} transType="filter_data" />
      </MantineProvider>
    )

    expect(await screen.findByText(/all messages will pass/i)).toBeTruthy()
  })
})
