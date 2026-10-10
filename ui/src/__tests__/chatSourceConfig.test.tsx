import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { SourceConfigFields } from '@/components/workflow/Source/SourceConfigFields'
import { NODE_CATEGORIES } from '@/pages/workflows/WorkflowEditor/constants/nodeCategories'

/**
 * The chat trigger's source form.
 *
 * A chat source answers a web widget, a Slack app or a Telegram bot. Each
 * platform is authenticated by its own credential, and the form writes each
 * one under the key the endpoint reads (internal/chat/transport/http):
 * `api_key`, `widget_key` with `allowed_origins`, `slack_signing_secret` and
 * `slack_bot_token`, `telegram_secret_token`.
 */
function renderChat(config: Record<string, any>) {
  const updateConfig = vi.fn()
  render(
    <MantineProvider>
      <SourceConfigFields
        source={{ id: 's1', name: 'Chat', type: 'chat', config } as any}
        updateConfig={updateConfig}
        discoveredTables={[]}
        discoveredDatabases={[]}
        isFetchingTables={false}
        isFetchingDBs={false}
        fetchTables={() => {}}
        fetchDatabases={() => {}}
        handleFileUpload={() => {}}
        uploading={false}
        allSources={[]}
      />
    </MantineProvider>,
  )
  return updateConfig
}

describe('the chat source form', () => {
  it('writes the path the endpoint is served on', () => {
    const updateConfig = renderChat({})
    fireEvent.change(screen.getByLabelText(/Chat path/i), { target: { value: '/api/chat/support' } })
    expect(updateConfig).toHaveBeenCalledWith('path', '/api/chat/support')
  })

  it('offers the three platforms and writes the one chosen', () => {
    const updateConfig = renderChat({ path: '/api/chat/support' })
    expect(screen.getByRole('radio', { name: 'Web' })).toBeChecked()
    fireEvent.click(screen.getByRole('radio', { name: 'Slack' }))
    expect(updateConfig).toHaveBeenCalledWith('platform', 'slack')
  })

  it('on the web, asks for the origins the public widget key is accepted from', () => {
    const updateConfig = renderChat({ path: '/api/chat/support' })
    expect(screen.getByText(/API key/)).toBeInTheDocument()
    expect(screen.getByText(/Widget key/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/Allowed origins/i), { target: { value: 'https://shop.example.com' } })
    expect(updateConfig).toHaveBeenCalledWith('allowed_origins', 'https://shop.example.com')
  })

  it('shows the embed snippet once there is a widget key, with the key and never the API key', () => {
    renderChat({ path: '/api/chat/support', widget_key: 'wk_public', api_key: 'very-secret' })
    const snippet = screen.getByLabelText(/Embed snippet/i)
    expect(snippet.textContent).toContain('/api/chat/widget.js')
    expect(snippet.textContent).toContain('data-widget-key="wk_public"')
    expect(snippet.textContent).toContain('/api/chat/support')
    expect(snippet.textContent).not.toContain('very-secret')
  })

  it('on Slack, writes the signing secret and the bot token', () => {
    const updateConfig = renderChat({ path: '/api/chat/support', platform: 'slack' })
    fireEvent.change(screen.getByLabelText(/Signing secret/i), { target: { value: 'sig' } })
    fireEvent.change(screen.getByLabelText(/Bot token/i), { target: { value: 'xoxb-1' } })
    expect(updateConfig).toHaveBeenCalledWith('slack_signing_secret', 'sig')
    expect(updateConfig).toHaveBeenCalledWith('slack_bot_token', 'xoxb-1')
    expect(screen.queryByLabelText(/Allowed origins/i)).not.toBeInTheDocument()
  })

  it('on Telegram, writes the webhook secret token', () => {
    const updateConfig = renderChat({ path: '/api/chat/support', platform: 'telegram' })
    fireEvent.change(screen.getByLabelText(/Secret token/i), { target: { value: 'tg_1' } })
    expect(updateConfig).toHaveBeenCalledWith('telegram_secret_token', 'tg_1')
  })

  it('writes where the answer is read from', () => {
    const updateConfig = renderChat({ path: '/api/chat/support' })
    fireEvent.change(screen.getByLabelText(/Reply field/i), { target: { value: 'answer' } })
    expect(updateConfig).toHaveBeenCalledWith('reply_field', 'answer')
  })
})

describe('the chat source in the palette', () => {
  it('is offered as a source', () => {
    const items = NODE_CATEGORIES.flatMap((c) => c.items as any[])
    expect(items.some((i) => i.type === 'source' && i.subType === 'chat')).toBe(true)
  })
})
