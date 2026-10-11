import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { server, signInAs } from '../test/setupTests'
import { VHostProvider, useVHost } from '@/context/VHostContext'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { ModelsPage } from '@/pages/ml/ModelsPage'

/**
 * In-process scoring: a trained model can be scored inside Hermod instead of
 * on the ML worker. The page sets it and says whether the live version's graph
 * is scored in-process or falls back to the worker, and why.
 */

type Status = { scoring: string; in_process: boolean; version?: string; ops?: string[]; reason?: string }

function scoringApi(models: any[], status: Status, after: Status = status) {
  const writes: Array<{ path: string; body: any }> = []
  server.use(
    http.get('/api/ml/worker', () => HttpResponse.json({ configured: true, ready: true })),
    http.get('/api/vhosts/:vhost/ml/models', () => HttpResponse.json({ data: models, total: models.length })),
    http.get('/api/vhosts/:vhost/ml/models/:name/scoring', () => HttpResponse.json(status)),
    http.put('/api/vhosts/:vhost/ml/models/:name/scoring', async ({ params, request }) => {
      const body = await request.json()
      writes.push({ path: `${params.vhost}/${params.name}`, body })
      return HttpResponse.json(after)
    }),
  )
  return writes
}

function SelectVHost({ vhost }: { vhost: string }) {
  const { setSelectedVHost, setAvailableVHosts } = useVHost()
  useEffect(() => {
    setSelectedVHost(vhost)
    setAvailableVHosts([vhost])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return null
}

function renderPage(role: 'Editor' | 'Viewer' = 'Editor') {
  signInAs(role)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <ConfirmProvider>
            <SelectVHost vhost="tenant-a" />
            <ModelsPage />
          </ConfirmProvider>
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

const trained = (extra: Record<string, unknown> = {}) => ({
  name: 'churn', backend: 'hermod-ml', url: '', remote_version: '2', serving: false, ...extra,
})

describe('Scoring on the Models page', () => {
  it('switches a trained model to in-process scoring and shows what it scores with', async () => {
    const writes = scoringApi([trained()], { scoring: 'worker', in_process: false, version: '2' },
      { scoring: 'in_process', in_process: true, version: '2', ops: ['Concat', 'LinearClassifier', 'Scaler'] })
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /scoring of churn/i }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/scored by the ML worker/i)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('radio', { name: /in-process/i }))
    await user.click(within(dialog).getByRole('button', { name: /^save$/i }))
    await waitFor(() => expect(writes).toEqual([{ path: 'tenant-a/churn', body: { scoring: 'in_process' } }]))
    expect(await within(dialog).findByText(/version 2 is scored in-process/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/Concat, LinearClassifier, Scaler/)).toBeInTheDocument()
  })

  it('says why a model set to in-process is still scored by the worker', async () => {
    scoringApi([trained({ scoring: 'in_process' })], {
      scoring: 'in_process', in_process: false, version: '2',
      reason: 'onnxscore: unsupported graph: operators ai.onnx.ml.SVMClassifier are not supported',
    })
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /scoring of churn/i }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/falls back to the ML worker/i)).toBeInTheDocument()
    expect(within(dialog).getByText(/SVMClassifier are not supported/)).toBeInTheDocument()
  })

  it('offers no scoring setting for a model served elsewhere', async () => {
    scoringApi([{ name: 'fraud', backend: 'oip', url: 'http://ml:8080', serving: false }], { scoring: 'worker', in_process: false })
    renderPage()
    expect(await screen.findByText('fraud')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /scoring of fraud/i })).not.toBeInTheDocument()
  })
})
