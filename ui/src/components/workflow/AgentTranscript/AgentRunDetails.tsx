import { Alert, Badge, Box, Code, Group, Paper, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconHandStop, IconRobot } from '@tabler/icons-react'
import {
  findTranscripts,
  pendingToolCall,
  type AgentTranscript,
  type PendingToolCall,
  type TranscriptEntry,
} from './agentTranscript'

const STATUS: Record<string, { label: string; color: string }> = {
  completed: { label: 'Completed', color: 'green' },
  failed: { label: 'Failed', color: 'red' },
  awaiting_approval: { label: 'Awaiting approval', color: 'yellow' },
}

const KIND: Record<string, { label: string; color: string }> = {
  model: { label: 'Model', color: 'grape' },
  tool_call: { label: 'Tool call', color: 'blue' },
  tool_result: { label: 'Tool result', color: 'teal' },
  approval_requested: { label: 'Approval requested', color: 'yellow' },
  approval_decision: { label: 'Decision', color: 'orange' },
}

const fmt = (n: number | undefined) => (n ?? 0).toLocaleString('en-US')

const mono = { fontFamily: 'var(--mantine-font-family-monospace)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' } as const

function PendingCall({ call }: { call: PendingToolCall }) {
  return (
    <Alert color="yellow" variant="light" icon={<IconHandStop size="1rem" />} title="The AI agent wants to call a tool">
      <Stack gap={6}>
        <Group gap="xs">
          <Text size="sm" fw={600}>
            Tool:
          </Text>
          <Code>{call.tool}</Code>
        </Group>
        {call.description && <Text size="sm">{call.description}</Text>}
        <Text size="xs" fw={700} c="dimmed">
          Arguments
        </Text>
        <Code block>{JSON.stringify(call.arguments ?? {}, null, 2)}</Code>
        <Text size="xs" c="dimmed">
          Approving runs this call with exactly these arguments; rejecting tells the agent it was refused, with your
          notes.
        </Text>
      </Stack>
    </Alert>
  )
}

function Entry({ e }: { e: TranscriptEntry }) {
  const k = KIND[e.kind] ?? { label: e.kind, color: 'gray' }
  const tokens = e.input_tokens || e.output_tokens ? `${fmt(e.input_tokens)} in / ${fmt(e.output_tokens)} out` : ''
  return (
    <Box pl="xs" style={{ borderLeft: `2px solid var(--mantine-color-${e.is_error ? 'red' : k.color}-outline)` }}>
      <Group gap={6} wrap="wrap">
        <Text size="xs" c="dimmed">
          Step {e.step}
        </Text>
        <Badge size="xs" variant="light" color={e.is_error ? 'red' : k.color}>
          {k.label}
        </Badge>
        {e.tool && <Code>{e.tool}</Code>}
        {tokens && (
          <Text size="xs" c="dimmed">
            {tokens}
          </Text>
        )}
      </Group>
      {e.input && (
        <Text size="xs" style={mono}>
          {e.input}
        </Text>
      )}
      {e.text && (
        <Text size="sm" style={mono} c={e.is_error ? 'var(--mantine-color-error)' : undefined}>
          {e.text}
        </Text>
      )}
    </Box>
  )
}

function Transcript({ field, t }: { field: string; t: AgentTranscript }) {
  const s = STATUS[t.status] ?? { label: t.status, color: 'gray' }
  return (
    <Paper withBorder p="sm" radius="md">
      <Stack gap="xs">
        <Group gap="xs" wrap="wrap">
          <IconRobot size="1rem" />
          <Text size="sm" fw={700}>
            AI agent run
          </Text>
          <Badge size="sm" variant="light" color={s.color}>
            {s.label}
          </Badge>
          <Text size="xs" c="dimmed">
            {t.steps ?? 0} {t.steps === 1 ? 'step' : 'steps'} · {fmt(t.usage?.input_tokens)} in /{' '}
            {fmt(t.usage?.output_tokens)} out tokens · <Code>{field}</Code>
          </Text>
        </Group>
        {t.error && (
          <Alert color="red" variant="light" icon={<IconAlertTriangle size="1rem" />} p="xs">
            {t.error}
          </Alert>
        )}
        {t.truncated && (
          <Text size="xs" c="dimmed">
            Older steps were dropped; only the most recent are kept.
          </Text>
        )}
        <Stack gap={6}>
          {t.entries.map((e, i) => (
            <Entry key={i} e={e} />
          ))}
        </Stack>
      </Stack>
    </Paper>
  )
}

/**
 * An ai_agent run found in a message's data: the call awaiting a decision,
 * if any, and each transcript step by step. Renders nothing otherwise.
 */
export function AgentRunDetails({ data }: { data: unknown }) {
  const call = pendingToolCall(data)
  const transcripts = findTranscripts(data)
  if (!call && transcripts.length === 0) return null
  return (
    <Stack gap="sm">
      {call && <PendingCall call={call} />}
      {transcripts.map(({ field, transcript }) => (
        <Transcript key={field} field={field} t={transcript} />
      ))}
    </Stack>
  )
}
