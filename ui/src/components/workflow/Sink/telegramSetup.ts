import { apiFetch } from '@/api'

/** Who a chat reaches, as POST /api/sinks/telegram/chats names it. */
export type TelegramChatKind = 'person' | 'group' | 'channel'

export interface TelegramBot {
  id: string
  name: string
  username: string
  can_join_groups: boolean
  /** False while privacy mode is on. Sending works either way. */
  reads_all_group_messages: boolean
}

export interface TelegramTopic {
  id: string
  name: string
}

export interface TelegramChat {
  id: string
  kind: TelegramChatKind
  name: string
  username?: string
  topics?: TelegramTopic[]
}

/** What POST /api/sinks/telegram/chats answers with. */
export interface TelegramChats {
  bot: TelegramBot
  chats: TelegramChat[]
  updates_unavailable?: boolean
  notice?: string
}

/** A refusal from either endpoint: Telegram's words and the fix. */
export interface TelegramSetupError {
  error: string
  hint?: string
  migrate_to_chat_id?: string
}

/** What POST /api/sinks/telegram/test-send answers with. */
export interface TelegramSent extends TelegramChat {
  message_id: number
}

export interface TelegramSetupConfig {
  bot_token: string
  chat_id?: string
  message_thread_id?: string
}

async function post<T>(url: string, vhost: string | undefined, config: TelegramSetupConfig): Promise<T> {
  try {
    const res = await apiFetch(url, {
      method: 'POST',
      // silent: the form shows the refusal where it happened.
      silent: true,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ vhost: vhost && vhost !== 'all' ? vhost : '', config }),
    })
    return (await res.json()) as T
  } catch (err: any) {
    const data = err?.data ?? {}
    const refusal: TelegramSetupError = {
      error: String(data.error || err?.message || 'Telegram could not be reached.'),
      hint: data.hint,
      migrate_to_chat_id: data.migrate_to_chat_id,
    }
    throw refusal
  }
}

export const findTelegramChats = (vhost: string | undefined, config: TelegramSetupConfig) =>
  post<TelegramChats>('/api/sinks/telegram/chats', vhost, config)

export const sendTelegramTest = (vhost: string | undefined, config: TelegramSetupConfig) =>
  post<TelegramSent>('/api/sinks/telegram/test-send', vhost, config)

/**
 * Telegram links that do the hard part in one tap: open the bot so a person
 * can press Start, or open the picker that adds the bot to a group or, as an
 * administrator that can post, to a channel.
 */
export function telegramLinks(username: string) {
  const bot = encodeURIComponent(username)
  return {
    person: `https://t.me/${bot}?start=hermod`,
    group: `https://t.me/${bot}?startgroup=hermod`,
    channel: `https://t.me/${bot}?startchannel=hermod&admin=post_messages`,
  }
}

export const BOTFATHER_URL = 'https://t.me/BotFather'

/**
 * Which kind a chat ID most likely is, for the initial tab. A person's ID is
 * positive; groups and channels are negative, and a channel shares the -100
 * prefix with a supergroup, so a negative ID opens the group tab.
 */
export function guessKind(chatId: string | undefined): TelegramChatKind {
  const id = (chatId ?? '').trim()
  if (id.startsWith('@')) return 'channel'
  if (id.startsWith('-')) return 'group'
  return 'person'
}

/** A chat's kind in the words of the tab it is listed under, for a count. */
export const KIND_WORDS: Record<TelegramChatKind, [string, string]> = {
  person: ['person', 'people'],
  group: ['group', 'groups'],
  channel: ['channel', 'channels'],
}
