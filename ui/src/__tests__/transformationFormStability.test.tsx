import { render, screen, waitFor } from '@testing-library/react'
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

function renderForm(node: any, onConfig?: (id: string, cfg: any, replace?: boolean) => void) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <VHostProvider>
          <TransformationForm
            selectedNode={node}
            updateNodeConfig={onConfig ?? (() => {})}
            availableFields={[]}
            incomingPayload={{ x: 1 }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>
  )
}

describe('TransformationForm stability', () => {
  /**
   * The live preview is debounced, not polled.
   *
   * `usePreviewTransformation` used to spread a React Query mutation result
   * into a fresh object on every render, which gave `runPreview` a new identity
   * every render, which re-armed the debounce effect every render. The 1s
   * "debounce" therefore behaved as a 1s poll: the panel re-ran the preview
   * forever with the user doing nothing, and the Running badge and diff
   * placeholder flickered once a second.
   */
  it('does not re-run the preview while the user is idle', async () => {
    let calls = 0
    server.use(
      http.post('/api/transformations/test', () => {
        calls += 1
        return HttpResponse.json({ ok: true })
      })
    )
    signInAs('editor')

    renderForm({ id: 'n1', type: 'map', data: {} })

    await waitFor(() => expect(calls).toBe(1), { timeout: 3000 })
    // Long enough for several cycles of the old 1s loop.
    await new Promise((resolve) => setTimeout(resolve, 2500))
    expect(calls).toBe(1)
  }, 20000)

  /**
   * The function library kept its search box in local state while being
   * declared inside the parent's render, so every parent render minted a new
   * component type and React remounted the subtree — wiping what the user had
   * typed. Combined with the preview poll above it cleared once a second.
   */
  it('keeps the function library search when the parent re-renders', async () => {
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
    signInAs('editor')
    const user = userEvent.setup()

    renderForm({ id: 'n1', type: 'transformation', data: { transType: 'advanced' } })

    const fnSearch = await screen.findByPlaceholderText('Search functions...')
    await user.type(fnSearch, 'lower')
    expect(fnSearch).toHaveValue('lower')

    // Type into a field owned by the parent, forcing a parent render.
    const settingsSearch = screen.getByPlaceholderText('Filter configuration...')
    await user.type(settingsSearch, 'mask')

    expect(screen.getByPlaceholderText('Search functions...')).toHaveValue('lower')
  }, 20000)

  /**
   * A re-run must not blank the panel. The result was cleared to `undefined`
   * while the next request was in flight, so the JSON pane dropped to
   * "// Loading..." and back on every refresh — the flicker users reported.
   */
  it('keeps showing the previous result while a re-run is in flight', async () => {
    let calls = 0
    server.use(
      http.post('/api/transformations/test', async () => {
        calls += 1
        if (calls > 1) await new Promise((r) => setTimeout(r, 400))
        return HttpResponse.json({ marker: `run-${calls}` })
      })
    )
    signInAs('editor')
    const user = userEvent.setup()

    renderForm({ id: 'n1', type: 'map', data: {} })

    await screen.findByText(/run-1/, undefined, { timeout: 3000 })

    // Ask for a re-run; the previous payload must stay on screen throughout.
    await user.click(screen.getByRole('button', { name: /run preview/i }))
    expect(screen.getByText(/run-1/)).toBeInTheDocument()

    await screen.findByText(/run-2/, undefined, { timeout: 3000 })
  }, 20000)
})
