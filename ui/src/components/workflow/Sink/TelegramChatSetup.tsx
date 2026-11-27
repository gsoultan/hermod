import { useState, type ReactNode } from 'react'
import {
  Alert,
  Anchor,
  Button,
  Chip,
  Group,
  List,
  Paper,
  Radio,
  SegmentedControl,
  Stack,
  Text,
  ThemeIcon,
} from '@mantine/core'
import {
  IconAlertTriangle,
  IconCheck,
  IconExternalLink,
  IconRefresh,
  IconSend,
  IconSpeakerphone,
  IconUser,
  IconUsersGroup,
} from '@tabler/icons-react'
import { TextInput } from '@/components/shared/ReferenceInputs'
import {
  BOTFATHER_URL,
  KIND_WORDS,
  findTelegramChats,
  guessKind,
  sendTelegramTest,
  telegramLinks,
  type TelegramChat,
  type TelegramChatKind,
  type TelegramChats,
  type TelegramSent,
  type TelegramSetupError,
} from './telegramSetup'

const KIND_ICON: Record<TelegramChatKind, typeof IconUser> = {
  person: IconUser,
  group: IconUsersGroup,
  channel: IconSpeakerphone,
}

interface TelegramChatSetupProps {
  /** The token field, rendered by the caller: the sink and the settings page store it differently. */
  tokenField: ReactNode
  token: string
  /** The vhost whose secrets the token may reference. */
  vhost?: string
  chatId: string
  topicId: string
  onChatIdChange: (value: string) => void
  onTopicIdChange: (value: string) => void
  chatLabel?: string
}

/**
 * Telegram set up in three steps: make a bot, choose a person, group or
 * channel, and send a test.
 *
 * The form used to ask for a bot token and a chat ID and say no more. The
 * chat ID was the wall: Telegram shows it nowhere, the usual advice was to
 * call getUpdates by hand and read the JSON, it differs for a person, a group,
 * a supergroup and a channel, and a bot cannot message a person until they
 * press Start. Here the bot's own recent chats are listed to pick from, with
 * the one-tap Telegram links that create them, and a real test message checks
 * the result.
 */
export function TelegramChatSetup({
  tokenField,
  token,
  vhost,
  chatId,
  topicId,
  onChatIdChange,
  onTopicIdChange,
  chatLabel = 'Chat ID',
}: TelegramChatSetupProps) {
  const [kind, setKind] = useState<TelegramChatKind>(() => guessKind(chatId))
  const [found, setFound] = useState<TelegramChats | null>(null)
  const [findError, setFindError] = useState<TelegramSetupError | null>(null)
  const [finding, setFinding] = useState(false)
  // The chat and topic the last successful test reached; step 3 is done while
  // they are still the ones configured.
  const [tested, setTested] = useState<string | null>(null)
  const hasToken = token.trim() !== ''

  const find = async () => {
    setFinding(true)
    setFindError(null)
    try {
      setFound(await findTelegramChats(vhost, { bot_token: token }))
    } catch (err) {
      setFound(null)
      setFindError(err as TelegramSetupError)
    } finally {
      setFinding(false)
    }
  }

  const pick = (chat: TelegramChat) => {
    onChatIdChange(chat.id)
    // A topic belongs to its group; picking another chat leaves General.
    if (chat.id !== chatId) onTopicIdChange('')
  }

  const bot = found?.bot

  return (
    <Stack gap="md">
      <SetupStep n={1} title="Create a bot" done={!!bot}>
        <Text size="sm" c="dimmed">
          In Telegram, open{' '}
          <Anchor href={BOTFATHER_URL} target="_blank" rel="noopener noreferrer">
            @BotFather
          </Anchor>
          , send <Code>/newbot</Code> and follow its two questions. Paste the token it gives you below. Already have a bot?
          Send <Code>/mybots</Code>, pick it, then API Token.
        </Text>
        {tokenField}
        <Group gap="sm">
          <Button
            size="xs"
            variant="light"
            leftSection={<IconCheck size="0.9rem" />}
            onClick={find}
            loading={finding}
            disabled={!hasToken}
          >
            Check bot
          </Button>
          {bot && (
            <Text size="sm" c="teal" fw={500} style={{ overflowWrap: 'anywhere' }}>
              {`Connected to @${bot.username}`}
            </Text>
          )}
        </Group>
        {findError && <Refusal refusal={findError} />}
      </SetupStep>

      <SetupStep n={2} title="Choose who gets the messages" done={chatId.trim() !== ''}>
        <SegmentedControl
          fullWidth
          size="xs"
          value={kind}
          onChange={(v) => setKind(v as TelegramChatKind)}
          aria-label="Who gets the messages"
          data={[
            { value: 'person', label: 'Person' },
            { value: 'group', label: 'Group' },
            { value: 'channel', label: 'Channel' },
          ]}
        />
        <KindSteps kind={kind} botUsername={bot?.username} readsAll={bot?.reads_all_group_messages} />
        <Group gap="sm">
          <Button
            size="xs"
            variant="default"
            leftSection={<IconRefresh size="0.9rem" />}
            onClick={find}
            loading={finding}
            disabled={!hasToken}
          >
            {found ? 'Refresh chats' : 'Find my chats'}
          </Button>
          {!hasToken && (
            <Text size="xs" c="dimmed">
              Paste the bot token first.
            </Text>
          )}
        </Group>
        {found && <ChatList found={found} kind={kind} chatId={chatId} onPick={pick} />}
        {found && (
          <TopicPicker chat={found.chats.find((c) => c.id === chatId)} topicId={topicId} onChange={onTopicIdChange} />
        )}
      </SetupStep>

      <SetupStep n={3} title="Check it works" done={tested === `${chatId}/${topicId}`}>
        <TextInput
          label={chatLabel}
          placeholder={kind === 'person' ? '123456789' : kind === 'channel' ? '-100123456789 or @channelname' : '-100123456789'}
          value={chatId}
          onChange={(e) => onChatIdChange(e.currentTarget.value)}
          required
          description="Filled in when you pick a chat above. A person's ID is a positive number, a group's or channel's starts with -. A public channel can also be @channelname."
        />
        <TextInput
          label="Topic ID"
          placeholder="Leave empty for General"
          value={topicId}
          onChange={(e) => onTopicIdChange(e.currentTarget.value)}
          description="Only for a group with topics turned on. Leave empty to post in General."
        />
        <TestSend
          vhost={vhost}
          token={token}
          chatId={chatId}
          topicId={topicId}
          onUseChatId={onChatIdChange}
          onSent={() => setTested(`${chatId}/${topicId}`)}
        />
      </SetupStep>
    </Stack>
  )
}

function Code({ children }: { children: ReactNode }) {
  return (
    <Text span ff="monospace" size="sm" c="var(--mantine-color-text)">
      {children}
    </Text>
  )
}

function SetupStep({ n, title, done, children }: { n: number; title: string; done: boolean; children: ReactNode }) {
  return (
    <Paper withBorder radius="md" p={{ base: 'xs', sm: 'md' }} component="section" aria-label={`Step ${n}: ${title}`}>
      <Stack gap="sm">
        <Group gap="sm" wrap="nowrap">
          <ThemeIcon size="md" radius="xl" variant={done ? 'filled' : 'light'} color={done ? 'teal' : 'blue'} aria-hidden>
            {done ? <IconCheck size="0.9rem" /> : <Text size="xs" fw={700}>{n}</Text>}
          </ThemeIcon>
          <Text fw={600} size="sm">
            {title}
          </Text>
        </Group>
        {children}
      </Stack>
    </Paper>
  )
}

/** What to do in Telegram for each kind of chat, with the link that does it. */
function KindSteps({ kind, botUsername, readsAll }: { kind: TelegramChatKind; botUsername?: string; readsAll?: boolean }) {
  const links = botUsername ? telegramLinks(botUsername) : null
  const botName = botUsername ? `@${botUsername}` : 'your bot'
  const open = (href: string, label: string) => (
    <Button
      component="a"
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      size="xs"
      variant="light"
      leftSection={<IconExternalLink size="0.9rem" />}
    >
      {label}
    </Button>
  )

  if (kind === 'person') {
    return (
      <Stack gap="xs">
        <List size="sm" type="ordered" spacing={4}>
          <List.Item>{`The person who should get the messages opens ${botName} in Telegram and presses Start.`}</List.Item>
          <List.Item>Press Find my chats and pick them from the list.</List.Item>
        </List>
        <Text size="xs" c="dimmed">
          A bot cannot message anyone first, so each person has to press Start once. Share the link below with them.
        </Text>
        {links && <Group gap="xs">{open(links.person, 'Open the bot in Telegram')}</Group>}
      </Stack>
    )
  }
  if (kind === 'group') {
    return (
      <Stack gap="xs">
        <List size="sm" type="ordered" spacing={4}>
          <List.Item>{`Add ${botName} to the group.`}</List.Item>
          <List.Item>{`Send a message in the group that mentions ${botName}, for example /start@${botUsername ?? 'your_bot'}.`}</List.Item>
          <List.Item>Press Find my chats and pick the group. If it has topics, send the message in the topic you want, then pick that topic too.</List.Item>
        </List>
        {readsAll === false && (
          <Text size="xs" c="dimmed">
            The bot has privacy mode on, which is fine for sending: it only needs to see the message that mentions it.
          </Text>
        )}
        {links && <Group gap="xs">{open(links.group, 'Add the bot to a group')}</Group>}
      </Stack>
    )
  }
  return (
    <Stack gap="xs">
      <List size="sm" type="ordered" spacing={4}>
        <List.Item>{`Add ${botName} to the channel as an administrator that can post messages.`}</List.Item>
        <List.Item>Post anything in the channel.</List.Item>
        <List.Item>Press Find my chats and pick the channel.</List.Item>
      </List>
      {links && <Group gap="xs">{open(links.channel, 'Add the bot to a channel')}</Group>}
    </Stack>
  )
}

function ChatList({
  found,
  kind,
  chatId,
  onPick,
}: {
  found: TelegramChats
  kind: TelegramChatKind
  chatId: string
  onPick: (chat: TelegramChat) => void
}) {
  if (found.updates_unavailable) {
    return (
      <Alert color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Recent chats are not available for this bot">
        {found.notice}
      </Alert>
    )
  }
  const shown = found.chats.filter((c) => c.kind === kind)
  if (shown.length === 0) {
    const others = (Object.keys(KIND_WORDS) as TelegramChatKind[])
      .filter((k) => k !== kind)
      .map((k) => {
        const n = found.chats.filter((c) => c.kind === k).length
        return n ? `${n} ${KIND_WORDS[k][n === 1 ? 0 : 1]}` : ''
      })
      .filter(Boolean)
    return (
      <Alert color="gray" variant="light" title={`No ${KIND_WORDS[kind][1]} found yet`}>
        <Text size="sm">
          Do the steps above in Telegram, then press Refresh chats. Telegram keeps a bot&apos;s messages for 24 hours, so a
          chat that has been quiet longer needs one new message.
        </Text>
        {others.length > 0 && (
          <Text size="sm" mt={4}>{`Found ${others.join(' and ')} under the other tabs.`}</Text>
        )}
      </Alert>
    )
  }
  return (
    <Radio.Group
      value={shown.some((c) => c.id === chatId) ? chatId : null}
      onChange={(id) => {
        const chat = shown.find((c) => c.id === id)
        if (chat) onPick(chat)
      }}
      label={`Pick a ${KIND_WORDS[kind][0]}`}
    >
      <Stack gap="xs" mt="xs">
        {shown.map((chat) => {
          const Icon = KIND_ICON[chat.kind]
          return (
            <Radio.Card key={chat.id} value={chat.id} radius="md" p="sm" aria-label={chat.name}>
              <Group wrap="nowrap" gap="sm">
                <Radio.Indicator />
                <ThemeIcon variant="light" radius="xl" size="md" aria-hidden>
                  <Icon size="1rem" />
                </ThemeIcon>
                <div style={{ minWidth: 0 }}>
                  <Text size="sm" fw={600} truncate>
                    {chat.name}
                  </Text>
                  <Text size="xs" c="dimmed" truncate>
                    {[chat.username ? `@${chat.username}` : '', `ID ${chat.id}`, chat.topics?.length ? `${chat.topics.length} topic${chat.topics.length === 1 ? '' : 's'}` : '']
                      .filter(Boolean)
                      .join(' · ')}
                  </Text>
                </div>
              </Group>
            </Radio.Card>
          )
        })}
      </Stack>
    </Radio.Group>
  )
}

function TopicPicker({ chat, topicId, onChange }: { chat?: TelegramChat; topicId: string; onChange: (v: string) => void }) {
  if (!chat?.topics?.length) return null
  return (
    <Stack gap={4}>
      <Text size="sm" fw={500}>
        Post in topic
      </Text>
      <Chip.Group value={topicId || 'general'} onChange={(v) => onChange(v === 'general' ? '' : String(v))}>
        <Group gap="xs">
          <Chip value="general" size="sm">
            General
          </Chip>
          {chat.topics.map((t) => (
            <Chip key={t.id} value={t.id} size="sm">
              {t.name}
            </Chip>
          ))}
        </Group>
      </Chip.Group>
    </Stack>
  )
}

function TestSend({
  vhost,
  token,
  chatId,
  topicId,
  onUseChatId,
  onSent,
}: {
  vhost?: string
  token: string
  chatId: string
  topicId: string
  onUseChatId: (id: string) => void
  onSent: () => void
}) {
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState<TelegramSent | null>(null)
  const [refusal, setRefusal] = useState<TelegramSetupError | null>(null)
  const ready = token.trim() !== '' && chatId.trim() !== ''

  const send = async () => {
    setSending(true)
    setSent(null)
    setRefusal(null)
    try {
      setSent(await sendTelegramTest(vhost, { bot_token: token, chat_id: chatId, message_thread_id: topicId }))
      onSent()
    } catch (err) {
      setRefusal(err as TelegramSetupError)
    } finally {
      setSending(false)
    }
  }

  return (
    <Stack gap="xs">
      <Group gap="sm">
        <Button size="xs" leftSection={<IconSend size="0.9rem" />} onClick={send} loading={sending} disabled={!ready}>
          Send test message
        </Button>
        {!ready && (
          <Text size="xs" c="dimmed">
            Needs a bot token and a chat.
          </Text>
        )}
      </Group>
      {sent && (
        <Alert color="teal" variant="light" icon={<IconCheck size="1rem" />} title="Test message sent">
          {`Telegram delivered it to ${sent.name} (${sent.kind}). Check Telegram to see it.`}
        </Alert>
      )}
      {refusal && (
        <Refusal
          refusal={refusal}
          action={
            refusal.migrate_to_chat_id ? (
              <Button size="xs" variant="light" color="red" onClick={() => onUseChatId(refusal.migrate_to_chat_id!)}>
                {`Use the new ID ${refusal.migrate_to_chat_id}`}
              </Button>
            ) : undefined
          }
        />
      )}
    </Stack>
  )
}

function Refusal({ refusal, action }: { refusal: TelegramSetupError; action?: ReactNode }) {
  return (
    <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} title="Telegram refused this">
      <Stack gap={6}>
        {refusal.hint && <Text size="sm">{refusal.hint}</Text>}
        <Text size="xs" c="dimmed">{`Telegram said: ${refusal.error}`}</Text>
        {action}
      </Stack>
    </Alert>
  )
}
