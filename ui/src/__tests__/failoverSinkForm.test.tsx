import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { FailoverSinkConfig } from '@/components/workflow/Sink/FailoverSinkConfig'

const STORED_SINKS = [
  { id: 'pg1', name: 'Orders', type: 'postgres' },
  { id: 'k1', name: 'Events', type: 'kafka' },
  { id: 'fo', name: 'Old failover', type: 'failover' },
]

function Harness({ initial, sinks }: { initial: Record<string, string>; sinks?: any[] }) {
  const [config, setConfig] = useState<Record<string, any>>(initial)
  return (
    <FailoverSinkConfig
      config={config}
      sinks={sinks}
      updateConfig={(key, value) => setConfig((c) => ({ ...c, [key]: value }))}
    />
  )
}

/**
 * The Sinks page and the workflow editor's sink form never passed a sink list
 * down, so the primary and fallback pickers were always empty and a failover
 * group could not be set up from the UI. The form now asks for the list.
 */
describe('the failover group form finds its own sinks', () => {
  it('offers the stored sinks by name with nothing passed in', async () => {
    server.use(http.get('/api/sinks', () => HttpResponse.json({ data: STORED_SINKS, total: STORED_SINKS.length })))
    render(
      <MantineProvider>
        <Harness initial={{}} />
      </MantineProvider>,
    )
    await userEvent.setup().click(await screen.findByPlaceholderText(/select primary sink/i))
    // Both pickers render their options, so each sink appears more than once.
    expect(await screen.findAllByRole('option', { name: /Orders/, hidden: true })).not.toHaveLength(0)
    expect(screen.getAllByRole('option', { name: /Events/, hidden: true })).not.toHaveLength(0)
    expect(screen.queryByRole('option', { name: /Old failover/, hidden: true })).not.toBeInTheDocument()
  })

  it('shows a saved primary by name once the list has loaded', async () => {
    server.use(http.get('/api/sinks', () => HttpResponse.json({ data: STORED_SINKS, total: STORED_SINKS.length })))
    render(
      <MantineProvider>
        <Harness initial={{ primary_id: 'pg1' }} />
      </MantineProvider>,
    )
    expect(await screen.findByDisplayValue(/Orders/)).toBeInTheDocument()
  })
})
