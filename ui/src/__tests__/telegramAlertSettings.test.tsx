import { useState } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { ConnectivityTab } from '@/pages/system/settings/ConnectivityTab'

function Harness() {
  const [notifSettings, setNotifSettings] = useState<Record<string, any>>({
    telegram_token: '123:abc', telegram_chat_id: '', telegram_message_thread_id: '7',
  })
  const ctx: any = {
    notifSettings,
    setNotifSettings,
    saveNotifMutation: { mutate: vi.fn(), isPending: false },
    testNotifMutation: { mutate: vi.fn(), isPending: false },
  }
  return (
    <>
      <ConnectivityTab ctx={ctx} />
      <pre data-testid="settings">{JSON.stringify(notifSettings)}</pre>
    </>
  )
}

// Picking a chat writes the chat ID and clears the topic in one go. Each
// write copied the settings it was rendered with, so the second (the topic)
// put back the empty chat ID the first had just filled: the pick did nothing.
it('picking a chat for the Telegram alerts fills the Default Chat ID', async () => {
  server.use(
    http.post('/api/sinks/telegram/chats', () =>
      HttpResponse.json({
        bot: { id: '1', name: 'Alerts', username: 'alerts_bot', can_join_groups: true, reads_all_group_messages: false },
        chats: [{ id: '1001', kind: 'person', name: 'Ana Lim' }],
      })
    )
  )
  render(<MantineProvider><Harness /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: /find my chats/i }))
  fireEvent.click(await screen.findByRole('radio', { name: 'Ana Lim' }))
  const saved = JSON.parse(screen.getByTestId('settings').textContent || '{}')
  expect(saved.telegram_chat_id).toBe('1001')
  expect(saved.telegram_message_thread_id).toBe('')
})
