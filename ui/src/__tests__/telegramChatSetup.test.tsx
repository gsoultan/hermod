import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, it, expect } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/setupTests'
import { NotificationSinkConfig } from '@/components/workflow/Sink/NotificationSinkConfig'
import { guessKind, telegramLinks } from '@/components/workflow/Sink/telegramSetup'

function Harness({ initial, vhost }: { initial: Record<string, string>; vhost?: string }) {
  const [config, setConfig] = useState<Record<string, string>>(initial)
  return (
    <>
      <NotificationSinkConfig
        type="telegram"
        vhost={vhost}
        config={config}
        updateConfig={(key, value) => setConfig((c) => ({ ...c, [key]: value }))}
      />
      <pre data-testid="config">{JSON.stringify(config)}</pre>
    </>
  )
}

const ui = (initial: Record<string, string> = {}, vhost?: string) =>
  render(
    <MantineProvider>
      <Harness initial={initial} vhost={vhost} />
    </MantineProvider>
  )

const written = () => JSON.parse(screen.getByTestId('config').textContent || '{}')

const found = {
  bot: { id: '1', name: 'Alerts', username: 'alerts_bot', can_join_groups: true, reads_all_group_messages: false },
  chats: [
    { id: '1001', kind: 'person', name: 'Ana Lim', username: 'ana' },
    { id: '-1004', kind: 'group', name: 'Support', topics: [{ id: '7', name: 'Alerts' }] },
    { id: '-1003', kind: 'channel', name: 'Announcements' },
  ],
}

// The chat ID was what nobody could find: the form asked for it and said
// "the chat, channel or group the bot posts to". Now the bot's own chats are
// listed and picking one fills the ID in.
describe('Telegram setup', () => {
  it('lists the people the bot has seen and fills the chat ID from a pick', async () => {
    let body: any
    server.use(
      http.post('/api/sinks/telegram/chats', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(found)
      })
    )
    ui({ bot_token: '123:abc' }, 'sales')

    fireEvent.click(screen.getByRole('button', { name: /find my chats/i }))
    const ana = await screen.findByRole('radio', { name: 'Ana Lim' })
    // Only people on the person tab.
    expect(screen.queryByRole('radio', { name: 'Support' })).toBeNull()
    fireEvent.click(ana)

    expect(written().chat_id).toBe('1001')
    expect(body).toEqual({ vhost: 'sales', config: { bot_token: '123:abc' } })
    expect(screen.getByText('Connected to @alerts_bot')).toBeTruthy()
    // The one-tap link a person needs to press Start.
    const open = screen.getByRole('link', { name: /open the bot in telegram/i })
    expect(open.getAttribute('href')).toBe('https://t.me/alerts_bot?start=hermod')
  })

  it('picks a group and one of its topics', async () => {
    server.use(http.post('/api/sinks/telegram/chats', () => HttpResponse.json(found)))
    ui({ bot_token: '123:abc' })

    fireEvent.click(screen.getByRole('radio', { name: 'Group' }))
    fireEvent.click(screen.getByRole('button', { name: /find my chats/i }))
    fireEvent.click(await screen.findByRole('radio', { name: 'Support' }))
    expect(written().chat_id).toBe('-1004')
    expect(screen.getByRole('link', { name: /add the bot to a group/i }).getAttribute('href')).toBe(
      'https://t.me/alerts_bot?startgroup=hermod'
    )

    fireEvent.click(screen.getByRole('radio', { name: 'Alerts' }))
    expect(written().message_thread_id).toBe('7')
    fireEvent.click(screen.getByRole('radio', { name: 'General' }))
    expect(written().message_thread_id).toBe('')
  })

  it('says what to do when the bot has seen no chat of that kind', async () => {
    server.use(http.post('/api/sinks/telegram/chats', () => HttpResponse.json({ ...found, chats: [found.chats[0]] })))
    ui({ bot_token: '123:abc' })

    fireEvent.click(screen.getByRole('radio', { name: 'Channel' }))
    fireEvent.click(screen.getByRole('button', { name: /find my chats/i }))
    expect(await screen.findByText('No channels found yet')).toBeTruthy()
    expect(screen.getByText(/found 1 person under the other tabs/i)).toBeTruthy()
  })

  it('explains a bot whose updates go to a webhook', async () => {
    server.use(
      http.post('/api/sinks/telegram/chats', () =>
        HttpResponse.json({ bot: found.bot, chats: [], updates_unavailable: true, notice: 'This bot delivers its messages to a webhook.' })
      )
    )
    ui({ bot_token: '123:abc' })
    fireEvent.click(screen.getByRole('button', { name: /find my chats/i }))
    expect(await screen.findByText(/delivers its messages to a webhook/)).toBeTruthy()
  })

  it('shows the fix for a refused token', async () => {
    server.use(
      http.post('/api/sinks/telegram/chats', () =>
        HttpResponse.json({ error: 'Unauthorized', hint: 'Telegram does not accept this bot token.' }, { status: 400 })
      )
    )
    ui({ bot_token: '123:wrong' })
    fireEvent.click(screen.getByRole('button', { name: /check bot/i }))
    expect(await screen.findByText('Telegram does not accept this bot token.')).toBeTruthy()
  })

  it('cannot look for chats without a token', () => {
    ui()
    expect((screen.getByRole('button', { name: /find my chats/i }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /send test message/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('sends a test message to the chat and topic and names where it landed', async () => {
    let body: any
    server.use(
      http.post('/api/sinks/telegram/test-send', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: '-1004', kind: 'group', name: 'Support', message_id: 3 })
      })
    )
    ui({ bot_token: '123:abc', chat_id: '-1004', message_thread_id: '7' })
    fireEvent.click(screen.getByRole('button', { name: /send test message/i }))
    expect(await screen.findByText(/delivered it to Support \(group\)/)).toBeTruthy()
    expect(body.config).toEqual({ bot_token: '123:abc', chat_id: '-1004', message_thread_id: '7' })
  })

  // An upgraded group has a new ID and Telegram names it; one click uses it.
  it('offers the new ID of a group that became a supergroup', async () => {
    server.use(
      http.post('/api/sinks/telegram/test-send', () =>
        HttpResponse.json(
          { error: 'Bad Request: group chat was upgraded to a supergroup chat', hint: 'Use that ID instead.', migrate_to_chat_id: '-1009' },
          { status: 400 }
        )
      )
    )
    ui({ bot_token: '123:abc', chat_id: '-200' })
    fireEvent.click(screen.getByRole('button', { name: /send test message/i }))
    fireEvent.click(await screen.findByRole('button', { name: /use the new id -1009/i }))
    await waitFor(() => expect(written().chat_id).toBe('-1009'))
  })
})

describe('telegram helpers', () => {
  it.each([
    ['', 'person'],
    ['123', 'person'],
    ['-200', 'group'],
    ['-100123', 'group'],
    ['@news', 'channel'],
  ])('guesses %s is a %s', (id, kind) => {
    expect(guessKind(id)).toBe(kind)
  })

  it('links to the start, add-to-group and add-to-channel pickers', () => {
    expect(telegramLinks('alerts_bot')).toEqual({
      person: 'https://t.me/alerts_bot?start=hermod',
      group: 'https://t.me/alerts_bot?startgroup=hermod',
      channel: 'https://t.me/alerts_bot?startchannel=hermod&admin=post_messages',
    })
  })
})
