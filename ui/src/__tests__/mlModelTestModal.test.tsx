import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { ModelTestModal } from '@/pages/ml/ModelTestModal'
import type { MLModel } from '@/lib/mlModels'

/**
 * The Test modal's "Why?": after a mimir model answers, one click asks the
 * server which columns pushed the answer, and the effects are listed biggest
 * first. Another server's model gets no such button.
 */

function renderModal(model: MLModel) {
  signInAs('Editor')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <ModelTestModal vhost="tenant-a" model={model} initialRow={{ plan: 'monthly', tenure: 3 }} onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

const mimir: MLModel = { name: 'churn', backend: 'mimir', url: 'http://mimir', remote_model: 'm-1', features: ['plan', 'tenure'], serving: false }
const oip: MLModel = { name: 'fraud', backend: 'oip', url: 'http://ml', features: ['amount'], serving: false }

describe('Test modal: Why?', () => {
  it('asks the mimir server about the row that was sent and lists each column\'s effect', async () => {
    let asked: any
    server.use(
      http.post('/api/vhosts/tenant-a/ml/models/churn/predict', () => HttpResponse.json({
        model: 'churn',
        predictions: [{ prediction: 'left', confidence: 0.8, prediction_id: 'p-1', guesses: [{ label: 'left', probability: 0.8 }, { label: 'stayed', probability: 0.2 }] }],
      })),
      http.post('/api/vhosts/tenant-a/ml/models/churn/explain', async ({ request }) => {
        asked = await request.json()
        return HttpResponse.json({
          model: 'churn',
          explanation: {
            label: 'left', value: 0.8, typical: 0.3, exact: true,
            contributions: [
              { column: 'plan', value: 'monthly', typical: 'yearly', effect: 0.4 },
              { column: 'tenure', value: '3', typical: '18', effect: -0.1 },
            ],
          },
        })
      }),
    )
    renderModal(mimir)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /run/i }))
    await screen.findByRole('list', { name: /guesses/i })

    await user.click(await screen.findByRole('button', { name: /why\?/i }))
    const list = await screen.findByRole('list', { name: /contributions/i })
    expect(asked).toEqual({ instance: { plan: 'monthly', tenure: 3 }, label: '' })
    const rows = within(list).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('plan')
    expect(rows[0]).toHaveTextContent('+40 pts')
    expect(rows[1]).toHaveTextContent('-10 pts')
    expect(screen.getByText(/left at 80% \(typical 30%\)/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /why\?/i })).not.toBeInTheDocument()
  })

  it('offers no Why? for a model on another server', async () => {
    server.use(http.post('/api/vhosts/tenant-a/ml/models/fraud/predict', () => HttpResponse.json({ model: 'fraud', predictions: [{ prediction: 0.5 }] })))
    renderModal(oip)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /run/i }))
    await screen.findByText(/prediction/i, { selector: 'p, div, span' })
    expect(screen.queryByRole('button', { name: /why\?/i })).not.toBeInTheDocument()
  })
})
