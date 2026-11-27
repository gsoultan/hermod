# Telegram setup (sink and alert settings)

The Telegram sink form and the Telegram block in Settings → Connectivity share one component,
`ui/src/components/workflow/Sink/TelegramChatSetup.tsx` (API helpers and t.me links in
`telegramSetup.ts`). The caller renders the token field (`tokenField`): the sink uses the
ReferenceInputs `TextInput` (secrets), Settings a plain `PasswordInput`.

## Backend

`pkg/comm/sink/telegram`: `callAPI` (api.go) is the one Bot API caller; the sink's `Write`/`Ping`,
`FindChats` (chats.go) and `SendTestMessage` (test_send.go) all use it, so every refusal is an
`*APIError` with a `Hint` (the fix) and, for an upgraded group, `MigrateToChatID`. Hints match
Telegram's English descriptions (not localised, unlike database servers), status 401/404 for a
bad token.

Routes (`internal/sink/transport/http/telegram_setup.go`), both `EditorOnly`, both take
`{vhost, config}`, check `AllowConnectorVHost` and resolve secrets with `Registry.ResolveSecrets`
like Test connection:
- `POST /api/sinks/telegram/chats` → `{bot, chats[{id,kind,name,username,topics}], updates_unavailable, notice}`
- `POST /api/sinks/telegram/test-send` → the chat reached, or 400 `{error, hint, migrate_to_chat_id}`

Errors are answered 400, never 401: `apiFetch` treats a 401 as an expired session and logs out,
and Telegram answers 401 for a wrong token. Settings uses the same routes with vhost "" (admins
pass `AllowConnectorVHost`).

## Rules FindChats keeps

- **Never call getUpdates with an offset.** An offset confirms (deletes) every earlier update,
  which would take messages from anything else reading the bot. Telegram keeps unconfirmed
  updates 24 hours, which is why the empty state says "a chat quiet longer needs one new message".
- 409 Conflict (a webhook is set — a Hermod Telegram chat source sets one — or another poller)
  is not an error: the bot is still reported, with `updates_unavailable` and a notice.
- `my_chat_member` (delivered by default, even with privacy mode on) is what finds a group the
  bot was just added to. Status `left`/`kicked` drops the chat; `migrate_to_chat_id` drops the old
  group ID in favour of the supergroup's.
- Topics come from `is_topic_message` + `message_thread_id`; the name from `forum_topic_created`
  on the message or on `reply_to_message`. Unknown name → "Topic N".

## Topics

`message_thread_id` sink config key (`TelegramSink.SetMessageThreadID`, refused at build when not
a positive integer) and `telegram_message_thread_id` in notification settings. Empty means
General.
