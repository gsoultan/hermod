import { useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Badge, Button, Code, Group, JsonInput, Modal, Paper, Select, Stack, Text,
} from '@mantine/core'
import { IconAlertCircle, IconAlertTriangle, IconListDetails, IconPlayerPlay } from '@tabler/icons-react'
import { executionsKey, formatDuration, runWorkflow, statusMeta, type RunResult } from '@/lib/executions'

interface WorkflowLike {
  id: string
  nodes?: { id: string; type: string; ref_id?: string; config?: Record<string, unknown> }[]
}

interface RunWithInputModalProps {
  opened: boolean
  onClose: () => void
  workflow: WorkflowLike
  /** Shows a run in the Executions view. */
  onOpenRun: (runId: string) => void
}

/** The payload as typed, or why it cannot be sent. */
function parsePayload(text: string): { value: Record<string, unknown> | null; error: string | null } {
  if (!text.trim()) return { value: null, error: 'Enter a JSON object, for example {"order_id": "A-17"}.' }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return { value: null, error: 'Not valid JSON yet.' }
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { value: null, error: 'The input must be a JSON object: the message the source node emits.' }
  }
  return { value: parsed as Record<string, unknown>, error: null }
}

/**
 * Runs a workflow once with a payload the user writes, from a source node
 * they pick, and shows how it ended. The workflow does not need to be started.
 */
export function RunWithInputModal({ opened, onClose, workflow, onOpenRun }: RunWithInputModalProps) {
  const queryClient = useQueryClient()
  const sources = useMemo(
    () =>
      (workflow.nodes ?? [])
        .filter((n) => n.type === 'source')
        .map((n) => ({ value: n.id, label: (typeof n.config?.label === 'string' && n.config.label) || n.ref_id || n.id })),
    [workflow.nodes],
  )
  const [text, setText] = useState('{\n  \n}')
  // The server starts at the first source when none is named, so that is
  // what the picker shows until the user picks another.
  const [sourceId, setSourceId] = useState<string | null>(null)
  const chosenSource = sourceId ?? sources[0]?.value ?? null
  const { value, error } = parsePayload(text)

  const run = useMutation({
    mutationFn: (message: Record<string, unknown>) =>
      runWorkflow(workflow.id, message, sources.length > 1 ? chosenSource ?? undefined : undefined),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: executionsKey(workflow.id) })
    },
    // Shown in the modal below; no toast on top of it.
    onError: () => {},
  })

  const close = () => {
    run.reset()
    onClose()
  }

  return (
    <Modal opened={opened} onClose={close} title="Run with input" size="lg">
      <Stack gap="md">
        <Alert color="orange" variant="light" icon={<IconAlertTriangle size="1rem" />} title="This is a real run">
          The message goes through every node once, and the workflow&apos;s sinks write for real: rows are inserted,
          messages are sent, and AI nodes are called and billed. The workflow does not need to be started.
        </Alert>

        {sources.length === 0 && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size="1rem" />}>
            This workflow has no source node, so there is nowhere to start a run.
          </Alert>
        )}

        {sources.length > 1 && (
          <Select
            label="Start at source"
            description="The run begins at this source node as if it had emitted the message."
            data={sources}
            value={chosenSource}
            onChange={setSourceId}
            allowDeselect={false}
          />
        )}

        <JsonInput
          label="Input message"
          description="The message the source node emits, as a JSON object."
          value={text}
          onChange={setText}
          error={text.trim() && error ? error : undefined}
          autosize
          minRows={6}
          maxRows={18}
          spellCheck={false}
        />

        {run.isError && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The run did not start">
            {(run.error as Error).message}
          </Alert>
        )}

        {run.data && <RunOutcome result={run.data} onOpen={() => { onOpenRun(run.data.run_id); close() }} />}

        <Group justify="flex-end">
          <Button variant="default" onClick={close}>Close</Button>
          <Button
            leftSection={<IconPlayerPlay size="1rem" />}
            disabled={!value || sources.length === 0}
            loading={run.isPending}
            onClick={() => value && run.mutate(value)}
          >
            Run workflow
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function RunOutcome({ result, onOpen }: { result: RunResult; onOpen: () => void }) {
  const meta = statusMeta(result.status)
  const steps = result.steps ?? []
  const failures = steps.filter((s) => s.error)
  return (
    <Paper withBorder p="md" radius="md" aria-live="polite">
      <Stack gap="xs">
        <Group gap="xs">
          <Badge variant="light" color={meta.color}>{meta.label}</Badge>
          <Text size="sm">Run</Text>
          <Code>{result.run_id}</Code>
        </Group>
        <Text size="sm" c="dimmed">
          {steps.length} step{steps.length === 1 ? '' : 's'}
          {steps.length > 0 && `, ${formatDuration(steps.reduce((ms, s) => ms + (s.duration ?? 0), 0) / 1e6)} in nodes`}
          {result.status === 'waiting' && '. The message is held at an approval until someone decides.'}
        </Text>
        {failures.map((s, i) => (
          <Text key={`${s.node_id}-${i}`} size="sm" c="red">
            <Code>{s.node_id}</Code> {s.error}
          </Text>
        ))}
        <Group>
          <Button size="xs" variant="light" leftSection={<IconListDetails size="0.9rem" />} onClick={onOpen}>
            Open in Executions
          </Button>
        </Group>
      </Stack>
    </Paper>
  )
}
