import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, renderHook, screen, within, fireEvent, waitFor, act } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import type { Node, Edge } from '@xyflow/react'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { useNodeContext } from '@/pages/workflows/WorkflowEditor/hooks/useNodeContext'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

/**
 * A DB Lookup's Run Preview showed the object it adds ("osiris", under
 * "after"), but the DB Lookup after it was handed the source's sample instead:
 * the preview's result lived only in the first node's form, so the second
 * node's raw payload had no osiris and its Available fields could only guess
 * "after.osiris: any (inferred)" from the first node's target field.
 */

const sample = {
  after: { id: 'r-1', organization_id: 'org-1' },
  operation: 'snapshot',
}

const previewed = {
  after: {
    id: 'r-1',
    organization_id: 'org-1',
    osiris: { OrganizationCode: 'IMPC', OrganizationName: 'PT. IMPACK PRATAMA' },
  },
  operation: 'snapshot',
}

const src = { id: 'src', type: 'source', data: { ref_id: 'r1' } } as unknown as Node
const l1 = {
  id: 'l1',
  type: 'transformation',
  data: { transType: 'db_lookup', targetField: 'osiris' },
} as unknown as Node
const l2 = {
  id: 'l2',
  type: 'transformation',
  data: { transType: 'db_lookup', targetField: 'tier' },
} as unknown as Node
const edges = [
  { id: 'e1', source: 'src', target: 'l1' },
  { id: 'e2', source: 'l1', target: 'l2' },
] as Edge[]

const sources = [{ id: 'r1', config: { use_cdc: 'true' } }]

describe('a node preview feeds the node after it', () => {
  beforeEach(() => {
    useWorkflowStore.setState({
      nodes: [src, l1, l2],
      edges,
      nodeSamples: { src: sample },
      nodePreviews: {},
      testResults: null,
    })
  })

  it('hands the next node the previewed output, nested keys and all', () => {
    useWorkflowStore.getState().setNodePreview('l1', previewed)

    const { result } = renderHook(() => useNodeContext(l2, null, sources, []))
    const ctx = result.current

    expect(ctx.incomingPayload.after.osiris.OrganizationCode).toBe('IMPC')
    const byPath = Object.fromEntries(ctx.availableFields.map((f: any) => [f.path, f.type]))
    expect(byPath['after.osiris.OrganizationCode']).toBe('string')
    expect(byPath['after.osiris.OrganizationName']).toBe('string')
    expect(byPath['after.osiris']).not.toBe('any (inferred)')
    // Nothing was skipped: the input is the output of the node right before.
    expect(ctx.inputSkipped).toBe(0)
  })

  it('falls back to the source sample when the lookup was never previewed', () => {
    const { result } = renderHook(() => useNodeContext(l2, null, sources, []))
    expect(result.current.incomingPayload.after.osiris).toBeUndefined()
  })

  it('forgets a preview once its node or anything before it is edited', () => {
    const store = useWorkflowStore.getState()
    store.setNodePreview('l1', previewed)
    store.setNodePreview('l2', previewed)

    store.updateNodeConfig('l1', { targetField: 'org' })

    const previews = useWorkflowStore.getState().nodePreviews
    expect(previews.l1).toBeUndefined()
    // l2's preview ran on l1's old output.
    expect(previews.l2).toBeUndefined()
  })

  it('keeps previews of nodes before the edited one', () => {
    const store = useWorkflowStore.getState()
    store.setNodePreview('l1', previewed)

    store.updateNodeConfig('l2', { targetField: 'level' })

    expect(useWorkflowStore.getState().nodePreviews.l1).toEqual(previewed)
  })
})

describe('Run Preview records what the node emitted', () => {
  const queryClient = new QueryClient()

  beforeEach(() => {
    useWorkflowStore.setState({ nodes: [src, l1, l2], edges, nodePreviews: {} })
  })

  it('stores the result under the node, and drops it when the input goes away', async () => {
    signInAs()
    server.use(http.post('*/api/transformations/test', () => HttpResponse.json(previewed)))

    const view = (incoming: any) => (
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <VHostProvider>
            <TransformationForm
              selectedNode={l1 as any}
              updateNodeConfig={() => {}}
              availableFields={[]}
              incomingPayload={incoming}
              sinkSchema={{}}
            />
          </VHostProvider>
        </QueryClientProvider>
      </MantineProvider>
    )
    const { rerender } = render(view(sample))

    const panel = await screen.findByTestId('live-preview')
    fireEvent.click(within(panel).getByRole('button', { name: /run preview/i }))
    await waitFor(() => expect(useWorkflowStore.getState().nodePreviews.l1).toEqual(previewed))

    await act(async () => rerender(view(undefined)))
    await waitFor(() => expect(useWorkflowStore.getState().nodePreviews.l1).toBeUndefined())
  }, 15000)
})
