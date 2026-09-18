import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Node } from '@xyflow/react'
import { WorkflowNodeSettingsModal } from '@/pages/workflows/WorkflowEditor/components/WorkflowNodeSettingsModal'

// A Foreach (Fan-out) node is not a `transformation`, so clicking it opens
// WorkflowNodeSettingsModal rather than the config drawer. That modal chose what
// to render from a literal list of node types which foreach was never added to,
// so the panel came up with a title, a Remove button and no editor: there was no
// way to set arrayPath, and without it the node errors on every message.
//
// Starting from the modal rather than from ForeachConfig is the point — the
// editor itself was fine, the route to it was missing.
const noop = () => {}

const renderModal = (node: Node) =>
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

describe('flow-control nodes are configurable from the settings modal', () => {
  it('offers Array Path for a foreach node', async () => {
    const node = {
      id: 'fe',
      type: 'foreach',
      data: { label: 'Foreach', ref_id: 'new', type: 'foreach', config: {} },
    } as unknown as Node

    renderModal(node)

    expect(await screen.findByRole('textbox', { name: /array path/i })).toBeTruthy()
  })

  it('offers the collect node its target field', async () => {
    const node = {
      id: 'co',
      type: 'collect',
      data: { label: 'Collect', ref_id: 'new', type: 'collect', config: {} },
    } as unknown as Node

    renderModal(node)

    // Whatever CollectConfig calls it, the panel must not be empty.
    expect(await screen.findAllByRole('textbox')).not.toHaveLength(0)
  })
})
