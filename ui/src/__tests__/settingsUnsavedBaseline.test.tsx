import { vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server, signInAs } from '../test/setupTests'
import { ConfirmProvider } from '@/components/common/ConfirmProvider'
import { SettingsPage } from '@/pages/system/SettingsPage'

// These render whole pages; on a loaded machine the 5s default is too short.
vi.setConfig({ testTimeout: 30000 })

/**
 * Settings warns before the tab closes with unsaved edits. The baseline it
 * compares against was taken on the first render, before the saved config had
 * arrived, so filling the fields from the server was itself an "edit": every
 * visit warned on leaving, with nothing changed. The S3 storage fields were
 * not compared at all, so a real edit to them never warned.
 */
const warnsOnLeave = () => {
  const event = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(event)
  return event.defaultPrevented
}

const renderSettings = () =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ConfirmProvider>
          <SettingsPage />
        </ConfirmProvider>
      </QueryClientProvider>
    </MantineProvider>,
  )

describe('Settings unsaved-changes warning', () => {
  beforeEach(() => {
    signInAs()
    server.use(
      http.get('/api/workspaces', () => HttpResponse.json([])),
      http.get('/api/config/database', () => HttpResponse.json({ type: 'postgres', conn: 'postgres://saved/db' })),
      http.get('/api/config/secrets', () => HttpResponse.json({ type: 'vault', vault: { address: 'https://vault', token: 't', mount: 'kv' } })),
      http.get('/api/config/state', () => HttpResponse.json({ type: 'redis', address: 'redis:6379', prefix: 'h:' })),
      http.get('/api/config/observability', () => HttpResponse.json({ otlp: { endpoint: 'otel:4317', service_name: 'svc', insecure: true } })),
      http.get('/api/settings', () => HttpResponse.json({ smtp_host: 'smtp.saved', base_url: 'https://hermod' })),
      http.get('/api/config/storage', () =>
        HttpResponse.json({ type: 's3', local_dir: 'uploads', s3: { endpoint: '', region: 'eu-west-1', bucket: 'saved-bucket', access_key_id: 'AK', secret_access_key: 'SK', use_ssl: true } }),
      ),
    )
  })

  it('does not warn when the saved settings have only been loaded', async () => {
    renderSettings()
    expect(await screen.findByDisplayValue('postgres://saved/db', {}, { timeout: 10000 })).toBeInTheDocument()
    expect(await screen.findByDisplayValue('saved-bucket', {}, { timeout: 10000 })).toBeInTheDocument()

    expect(warnsOnLeave()).toBe(false)
  })

  it('warns after an edit to the S3 storage settings', async () => {
    const user = userEvent.setup()
    renderSettings()
    const bucket = await screen.findByDisplayValue('saved-bucket', {}, { timeout: 10000 })
    await screen.findByDisplayValue('postgres://saved/db', {}, { timeout: 10000 })
    expect(warnsOnLeave()).toBe(false)

    await user.type(bucket, '-2')
    expect(warnsOnLeave()).toBe(true)
  })
})
