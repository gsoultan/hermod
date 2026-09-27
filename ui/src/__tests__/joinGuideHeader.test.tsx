import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { VHostProvider } from '@/context/VHostContext'
import { server, signInAs } from '../test/setupTests'
import { http, HttpResponse } from 'msw'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { vi } from 'vitest'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

function renderNode(node: { type: string; data: Record<string, unknown> }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <TransformationForm
            selectedNode={{ id: 'n1', ...node } as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={{ order_id: 1 }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

/**
 * The sentence over a node's editor says what to do first, so it has to name
 * something the editor shows.
 *
 * Both join editors opened under "Combines several fields into one. Pick the
 * fields and the separator." Neither has a list of fields or a separator, so
 * the first instruction on the form sent the user looking for controls that do
 * not exist. The nodes are built the way the palette builds them
 * (useWorkflowEvents' addNodeAtPosition).
 */
describe('the first step over each join editor', () => {
  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
  })

  const cases = [
    {
      name: 'Stateful Join',
      node: { type: 'join', data: { label: 'Stateful Join', ref_id: 'new', type: 'join' } },
      control: /correlation key path/i,
    },
    {
      name: 'Join / Enrich',
      node: { type: 'transformation', data: { label: 'Join / Enrich', ref_id: 'new', transType: 'join' } },
      control: /join key/i,
    },
  ]

  for (const c of cases) {
    it(`${c.name}: names a control the editor shows, and no separator`, async () => {
      renderNode(c.node)

      const controls = await screen.findAllByLabelText(c.control, undefined, { timeout: 5000 })
      expect(controls.length).toBeGreaterThan(0)

      // The first step is the span inside the header sentence; the control's
      // own label is a <label>, so it cannot satisfy this.
      const firstStep = screen.getByText(c.control, { selector: 'span' })
      expect(firstStep.parentElement?.textContent).not.toMatch(/separator/i)
    }, 20000)
  }
})
