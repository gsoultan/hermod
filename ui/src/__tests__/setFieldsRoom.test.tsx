import { fireEvent, render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useState } from 'react'
import { http, HttpResponse } from 'msw'
import { vi } from 'vitest'
import { VHostProvider } from '@/context/VHostContext'
import { TransformationForm } from '@/components/forms/TransformationForm'
import { SetFieldsConfig } from '@/components/workflow/Transformation/configs/data/SetFieldsConfig'
import { server, signInAs } from '../test/setupTests'

vi.mock('@tanstack/react-router', () => ({
  Link: (props: any) => <button {...props} />,
}))

/**
 * The Set Fields editor sat in the middle of three columns, between the source
 * data and the live preview, so its rows and its JSON were squeezed to about a
 * third of the screen. Focus editor gives the configuration the whole width,
 * and each row's value has room for a long path or a JSON document.
 */

function renderForm(transType: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <MantineProvider>
      <QueryClientProvider client={qc}>
        <VHostProvider>
          <TransformationForm
            selectedNode={{ id: 'n1', type: 'transformation', data: { transType, 'column.a': 'source.b' } } as any}
            updateNodeConfig={() => {}}
            availableFields={[]}
            incomingPayload={{ x: 1 }}
            sinkSchema={{}}
          />
        </VHostProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('Focus editor', () => {
  beforeEach(() => {
    signInAs('editor')
    server.use(http.post('/api/transformations/test', () => HttpResponse.json({ ok: true })))
    try {
      localStorage.clear()
    } catch {}
  })

  it('hides the source data and the preview so the configuration has the full width, and brings them back', async () => {
    renderForm('set')
    const focus = await screen.findByRole('button', { name: /focus editor/i })
    expect(focus).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('available-fields-panel')).toBeInTheDocument()
    expect(await screen.findByText(/live preview/i)).toBeInTheDocument()

    fireEvent.click(focus)

    expect(focus).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByTestId('available-fields-panel')).toBeNull()
    expect(screen.queryByText(/live preview/i)).toBeNull()

    fireEvent.click(focus)

    expect(focus).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('available-fields-panel')).toBeInTheDocument()
  })
})

function Harness({ initial }: { initial: Record<string, unknown> }) {
  const [config, setConfig] = useState<Record<string, unknown>>(initial)
  const updateNodeConfig = (_id: string, next: any, replace = false) =>
    setConfig((prev) => (replace ? next : { ...prev, ...next }))
  return (
    <MantineProvider>
      <SetFieldsConfig config={config} updateNodeConfig={updateNodeConfig} nodeId="n1" availableFields={[]} addField={() => {}} />
    </MantineProvider>
  )
}

describe('a Set Fields row', () => {
  it('wraps a long expression instead of cutting it off', async () => {
    render(<Harness initial={{ transType: 'set', 'column.a': 'source.after.session.sessions.0.access_token' }} />)
    const value = await screen.findByRole('textbox', { name: /value or expression/i })
    expect(value.tagName).toBe('TEXTAREA')
  })

  it('opens a JSON value in a larger editor', async () => {
    render(<Harness initial={{ transType: 'set', 'column.q': { session: 'source.after.token' } }} />)
    const row = (await screen.findByRole('textbox', { name: /json value/i })).closest('.mantine-Paper-root') as HTMLElement
    expect(within(row).getByRole('button', { name: /expand json editor/i })).toBeInTheDocument()
    expect(within(row).getByRole('button', { name: /format/i })).toBeInTheDocument()
  })
})
