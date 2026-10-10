import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { MLPredictConfig } from '@/components/workflow/Transformation/configs/ml/MLPredictConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * The Predict node: pick one of the workflow vhost's models, say which record
 * field feeds which model feature, and name the field the prediction lands in.
 * The saved config is what pkg/comm/transformer/ml reads: model, inputs (JSON
 * object text of feature -> field path) and outputField.
 */

function Harness({ initial = {} as Record<string, any>, onConfig }: { initial?: Record<string, any>; onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <MLPredictConfig
      nodeId="n1"
      config={config}
      fieldPaths={['order.total', 'country']}
      updateNodeConfig={(_id: string, patch: any) => {
        setConfig((c) => {
          const next = { ...c, ...patch }
          onConfig(next)
          return next
        })
      }}
    />
  )
}

function renderConfig(initial?: Record<string, any>) {
  signInAs('Editor')
  useWorkflowStore.setState({ vhost: 'tenant-a' } as any)
  let last: any = initial ?? {}
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <Harness initial={initial} onConfig={(c) => { last = c }} />
      </QueryClientProvider>
    </MantineProvider>,
  )
  return () => last
}

describe('Predict node', () => {
  it('is offered in the palette under Machine Learning, with an editor', () => {
    const ml = NODE_CATEGORIES.find((c) => c.title === 'Machine Learning')
    expect(ml?.items.map((i: any) => i.subType)).toContain('ml_predict')
    expect(TRANSFORM_CONFIGS.ml_predict).toBe(MLPredictConfig)
  })

  it("lists the workflow vhost's models and saves the mapping the backend reads", async () => {
    let askedVHost = ''
    server.use(
      http.get('/api/vhosts/:vhost/ml/models', ({ params }) => {
        askedVHost = params.vhost as string
        return HttpResponse.json({
          data: [{ name: 'fraud', backend: 'oip', url: 'http://ml', features: ['amount', 'country'] }],
          total: 1,
        })
      }),
    )
    const config = renderConfig()
    const user = userEvent.setup()

    await screen.findByPlaceholderText('Choose a model')
    await user.click(screen.getByRole('combobox', { name: /^model/i }))
    await user.click(await screen.findByRole('option', { name: 'fraud', hidden: true }))
    expect(askedVHost).toBe('tenant-a')
    expect(config().model).toBe('fraud')

    // The model declares its features, so a row per feature is offered.
    const fieldInputs = await screen.findAllByRole('combobox', { name: /record field for/i })
    expect(fieldInputs).toHaveLength(2)
    await user.type(fieldInputs[0], 'order.total')
    await user.type(fieldInputs[1], 'country')
    await waitFor(() =>
      expect(JSON.parse(config().inputs)).toEqual({ amount: 'order.total', country: 'country' }),
    )

    const output = screen.getByRole('textbox', { name: /output field/i })
    await user.clear(output)
    await user.type(output, 'fraud_score')
    expect(config().outputField).toBe('fraud_score')
  })

  it('says how to add a model when the vhost has none', async () => {
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [], total: 0 })))
    renderConfig()
    expect(await screen.findByText(/no models in tenant-a/i)).toBeInTheDocument()
  })
})
