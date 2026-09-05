import { render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
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

/**
 * Stateful harness: the pane derives its error from `config.steps`, so the node
 * must actually take the updates it emits — a static node would never show one.
 */
function Host({ initial, updates }: { initial: string; updates: any[] }) {
  const [data, setData] = useState<Record<string, any>>({ transType: 'pipeline', steps: initial })
  return (
    <TransformationForm
      selectedNode={{ id: 'n1', type: 'transformation', data } as any}
      updateNodeConfig={(_id, cfg) => { updates.push(cfg); setData((d) => ({ ...d, ...cfg })) }}
      availableFields={[]}
      incomingPayload={{ x: 1 }}
      sinkSchema={{}}
    />
  )
}

function renderPipeline(steps = '[]') {
  const updates: any[] = []
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <Host initial={steps} updates={updates} />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
  return updates
}

/**
 * The pipeline Steps pane stores the raw string the user types, which is the
 * right call — it round-trips their formatting. But it stored anything at all,
 * silently: a broken document sat in the node config with nothing on screen to
 * say the pipeline would not run.
 */
describe('pipeline steps validation', () => {
  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
  })

  it('says so when the document is not valid JSON', async () => {
    const user = userEvent.setup()
    renderPipeline()
    const box = await screen.findByLabelText(/steps \(json array\)/i)
    await user.clear(box)
    await user.type(box, '[[{{"transType": "mask"')
    await waitFor(() => expect(screen.getByText(/not valid json/i)).toBeInTheDocument())
  }, 20000)

  it('says so when the document is valid JSON but not an array', async () => {
    const user = userEvent.setup()
    renderPipeline()
    const box = await screen.findByLabelText(/steps \(json array\)/i)
    await user.clear(box)
    await user.type(box, '{{"transType": "mask"}')
    await waitFor(() => expect(screen.getByText(/must be a json array/i)).toBeInTheDocument())
  }, 20000)

  it('accepts a valid array with no complaint and keeps the raw text', async () => {
    const user = userEvent.setup()
    const updates = renderPipeline()
    const box = await screen.findByLabelText(/steps \(json array\)/i)
    await user.clear(box)
    await user.type(box, '[[{{"transType":"mask","field":"email"}]')
    await waitFor(() => expect(updates.at(-1)?.steps).toBe('[{"transType":"mask","field":"email"}]'))
    expect(screen.queryByText(/not valid json|must be a json array/i)).toBeNull()
  }, 20000)
})
