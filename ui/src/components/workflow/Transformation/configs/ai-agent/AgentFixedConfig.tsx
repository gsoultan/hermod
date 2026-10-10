import { useState } from 'react'
import { Button, Code, Group, Stack, Text, Textarea } from '@mantine/core'
import { TOOL_RESULT_FIELD } from './agentTools'

interface AgentFixedConfigProps {
  kind: string
  value: Record<string, unknown> | undefined
  onChange: (config: Record<string, unknown>) => void
}

/**
 * Starting points for each read kind, with the keys its transformer reads.
 * The tool's arguments are the fields of the record the lookup runs on.
 */
const EXAMPLES: Record<string, Record<string, unknown>> = {
  db_lookup: { sourceId: 'SOURCE_ID', table: 'customers', keyColumn: 'email', keyField: 'email' },
  api_lookup: { url: 'https://api.example.com/orders/{{.order_id}}', method: 'GET' },
  ai_retrieve: {
    provider: 'openai',
    model: 'EMBEDDING_MODEL',
    apiKey: '{{secret("OPENAI_API_KEY")}}',
    store: 'pgvector',
    connectionString: '{{secret("PGVECTOR_URL")}}',
    table: 'documents',
    queryField: 'question',
  },
}

const pretty = (v: unknown) => JSON.stringify(v ?? {}, null, 2)

/**
 * A read tool's fixed transformer config (config.go `config`), edited as a
 * JSON object. The model cannot change any of it; only a valid object is
 * saved, and targetField is always overridden by the node.
 */
export function AgentFixedConfig({ kind, value, onChange }: AgentFixedConfigProps) {
  const shown = (v: Record<string, unknown> | undefined) => (v && Object.keys(v).length ? pretty(v) : '')
  const serial = JSON.stringify(value ?? {})
  const [text, setText] = useState(() => shown(value))
  const [error, setError] = useState('')
  // The saved config changed under this editor (another tool moved into its
  // place, or an undo): show it. The editor's own saves are recorded first,
  // so typing is never overwritten.
  const [synced, setSynced] = useState(serial)
  if (serial !== synced) {
    setSynced(serial)
    setText(shown(value))
    setError('')
  }

  const save = (config: Record<string, unknown>) => {
    setSynced(JSON.stringify(config))
    onChange(config)
  }

  const edit = (next: string) => {
    setText(next)
    if (next.trim() === '') {
      setError('')
      save({})
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(next)
    } catch (e) {
      setError(`Not valid JSON: ${(e as Error).message}`)
      return
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setError('The settings must be a JSON object, such as {"table": "customers"}.')
      return
    }
    setError('')
    save(parsed as Record<string, unknown>)
  }

  return (
    <Stack gap={4}>
      <Textarea
        label="Fixed settings"
        placeholder={pretty(EXAMPLES[kind] ?? {})}
        value={text}
        onChange={(e) => edit(e.currentTarget.value)}
        autosize
        minRows={3}
        styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }}
        error={error || undefined}
        description={
          <>
            The {kind} settings, as JSON; the model cannot change them. Refer to an argument as a field of the record,
            e.g. <Code>{'{{.email}}'}</Code>. The result always goes to <Code>{TOOL_RESULT_FIELD}</Code>. Use{' '}
            <Code>{'{{secret("NAME")}}'}</Code> for credentials.
          </>
        }
      />
      {EXAMPLES[kind] && (
        <Group>
          <Button size="xs" variant="subtle" onClick={() => edit(pretty(EXAMPLES[kind]))}>
            Insert example
          </Button>
          <Text size="xs" c="dimmed">
            Replaces what is here.
          </Text>
        </Group>
      )}
    </Stack>
  )
}
