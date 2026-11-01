import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { MLFeedbackConfig } from '@/components/workflow/Transformation/configs/ml/MLFeedbackConfig'
import { TRANSFORM_CONFIGS } from '@/components/workflow/Transformation/configs/registry'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'
import { useWorkflowStore } from '@/pages/workflows/WorkflowEditor/store/useWorkflowStore'

/**
 * The Feedback node: tell a mimir model what really happened after one of
 * its predictions. The saved config is what pkg/comm/transformer/ml reads:
 * model, predictionIdField, actualField and outputField.
 */

function Harness({ initial = {} as Record<string, any>, onConfig }: { initial?: Record<string, any>; onConfig: (c: any) => void }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <MLFeedbackConfig
      nodeId="n1"
      config={config}
      fieldPaths={['outcome', 'prediction.prediction_id']}
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

const models = [
  { name: 'churn', backend: 'mimir', url: 'http://mimir', remote_model: 'm-1', features: ['plan'] },
  { name: 'fraud', backend: 'oip', url: 'http://ml', features: ['amount'] },
]

describe('Feedback node', () => {
  it('is offered in the palette under Machine Learning, with an editor', () => {
    const ml = NODE_CATEGORIES.find((c) => c.title === 'Machine Learning')
    expect(ml?.items.map((i: any) => i.subType)).toContain('ml_feedback')
    expect(TRANSFORM_CONFIGS.ml_feedback).toBe(MLFeedbackConfig)
  })

  it('lets only a mimir model be chosen and saves what the backend reads', async () => {
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: 2 })))
    const config = renderConfig()
    const user = userEvent.setup()

    await screen.findByPlaceholderText('Choose a model')
    await user.click(screen.getByRole('combobox', { name: /^model/i }))
    const other = await screen.findByRole('option', { name: /fraud/i, hidden: true })
    expect(other).toHaveAttribute('data-combobox-disabled', 'true')
    await user.click(await screen.findByRole('option', { name: 'churn', hidden: true }))
    expect(config().model).toBe('churn')

    await user.type(screen.getByRole('combobox', { name: /real answer field/i }), 'outcome')
    expect(config().actualField).toBe('outcome')
    await user.type(screen.getByRole('combobox', { name: /prediction id field/i }), 'pid')
    expect(config().predictionIdField).toBe('pid')
    await user.type(screen.getByRole('textbox', { name: /output field/i }), 'verdict')
    expect(config().outputField).toBe('verdict')
  })

  it('says how to connect a mimir model when the vhost has none', async () => {
    server.use(http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: [models[1]], total: 1 })))
    renderConfig()
    expect(await screen.findByText(/no mimir model in tenant-a/i)).toBeInTheDocument()
  })
})
