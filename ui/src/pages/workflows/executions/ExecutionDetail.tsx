import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert, Badge, Button, Code, Group, Loader, Paper, ScrollArea, Stack, Text, Title,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconAlertCircle, IconCircleCheck, IconCircleX, IconPlayerPlay, IconSparkles } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { formatDateTime } from '@/utils/dateUtils'
import {
  ApiRequestError, executionsKey, formatDuration, formatTokens, getExecution, replayExecution, statusMeta,
  type ExecutionStep,
} from '@/lib/executions'

interface ExecutionDetailProps {
  workflowId: string
  runId: string
  canReplay: boolean
  /** Opens another run: the one a replay created. */
  onOpenRun: (runId: string) => void
}

/** One run: its totals, every step with its result and output, and Replay. */
export function ExecutionDetail({ workflowId, runId, canReplay, onOpenRun }: ExecutionDetailProps) {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const [replayError, setReplayError] = useState<{ status: number; message: string } | null>(null)

  const { data: run, isLoading, error } = useQuery({
    queryKey: [...executionsKey(workflowId), 'run', runId],
    queryFn: ({ signal }) => getExecution(workflowId, runId, signal),
  })

  const replay = useMutation({
    mutationFn: () => replayExecution(workflowId, runId),
    onSuccess: (result) => {
      setReplayError(null)
      queryClient.invalidateQueries({ queryKey: executionsKey(workflowId) })
      notifications.show({
        title: 'Run replayed',
        message: `The replay ended ${statusMeta(result.status).label.toLowerCase()}. It is open now.`,
        color: statusMeta(result.status).color,
      })
      onOpenRun(result.run_id)
    },
    onError: (err) => {
      setReplayError({
        status: err instanceof ApiRequestError ? err.status : 0,
        message: (err as Error).message,
      })
    },
  })

  // Asked before the mutation: a cancelled confirmation must send nothing.
  const askReplay = async () => {
    const ok = await confirm({
      title: 'Replay this run?',
      message: 'The workflow runs again from the same source node with the input this run started with, as a new run.',
      consequence: 'This is a real run: its sinks write again and any AI nodes are called (and billed) again.',
      confirmLabel: 'Replay run',
    })
    if (ok) replay.mutate()
  }

  if (isLoading) {
    return <Group justify="center" py={80}><Loader size="sm" aria-label="Loading run" /></Group>
  }
  if (error || !run) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="Could not load this run">
        {(error as Error | null)?.message || 'The run was not found. Its trace may have been removed by retention.'}
      </Alert>
    )
  }

  const meta = statusMeta(run.status)
  const steps = run.steps ?? []

  return (
    <Stack gap="md">
      <Paper withBorder p="md" radius="md">
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap={4}>
            <Group gap="xs">
              <Title order={4}>Run</Title>
              <Code>{run.run_id}</Code>
              <Badge variant="light" color={meta.color}>{meta.label}</Badge>
            </Group>
            <Text size="sm" c="dimmed">Started {formatDateTime(run.started_at)}</Text>
            <Group gap="lg">
              <Text size="sm"><Text span c="dimmed">Duration </Text>{formatDuration(run.duration_ms)}</Text>
              <Text size="sm"><Text span c="dimmed">Steps </Text>{run.step_count}</Text>
              <Text size="sm"><Text span c="dimmed">Errors </Text>{run.error_count}</Text>
              <Text size="sm">
                <Text span c="dimmed">AI </Text>
                {run.ai?.calls ? `${run.ai.calls} call${run.ai.calls === 1 ? '' : 's'}, ${formatTokens(run.ai)}` : 'none'}
              </Text>
            </Group>
          </Stack>
          {canReplay && (
            <Button
              variant="light"
              leftSection={<IconPlayerPlay size="1rem" />}
              onClick={askReplay}
              loading={replay.isPending}
            >
              Replay
            </Button>
          )}
        </Group>
        {replayError && (
          <Alert
            mt="sm"
            color={replayError.status === 409 ? 'yellow' : 'red'}
            icon={<IconAlertCircle size="1rem" />}
            title={replayError.status === 409 ? 'This run cannot be replayed' : 'Replay failed'}
            withCloseButton
            closeButtonLabel="Dismiss"
            onClose={() => setReplayError(null)}
          >
            {replayError.message}
          </Alert>
        )}
        {run.status === 'waiting' && (
          <Text size="xs" c="dimmed" mt="sm">
            Waiting: the message is held at an approval step until someone decides on it.
          </Text>
        )}
      </Paper>

      <Stack gap="sm" component="ol" style={{ listStyle: 'none', padding: 0, margin: 0 }} aria-label="Steps">
        {steps.map((step, i) => (
          <StepCard key={`${step.node_id}-${i}`} step={step} />
        ))}
      </Stack>
    </Stack>
  )
}

function StepCard({ step }: { step: ExecutionStep }) {
  const failed = !!step.error
  return (
    <Paper
      component="li"
      withBorder
      p="sm"
      radius="md"
      style={{ borderLeft: `4px solid var(--mantine-color-${failed ? 'red' : 'green'}-filled)` }}
    >
      <Stack gap={6}>
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            {failed ? (
              <IconCircleX size="1.1rem" color="var(--mantine-color-red-filled)" aria-label="Failed" />
            ) : (
              <IconCircleCheck size="1.1rem" color="var(--mantine-color-green-filled)" aria-label="Succeeded" />
            )}
            <Stack gap={0} style={{ minWidth: 0 }}>
              <Text fw={600} size="sm" truncate>{step.label || step.node_id}</Text>
              {step.label && <Text size="xs" c="dimmed" ff="monospace" truncate>{step.node_id}</Text>}
            </Stack>
            {step.node_type && <Badge size="sm" variant="outline" color="gray">{step.node_type}</Badge>}
          </Group>
          <Group gap="xs" wrap="nowrap">
            {!!step.ai?.calls && (
              <Badge size="sm" variant="light" color="grape" leftSection={<IconSparkles size="0.7rem" />}>
                {formatTokens(step.ai)}
              </Badge>
            )}
            <Badge size="sm" variant="light" color="gray">{formatDuration(step.duration_ms)}</Badge>
          </Group>
        </Group>
        {failed && (
          <Alert color="red" variant="light" p="xs" icon={<IconCircleX size="1rem" />}>
            {step.error}
          </Alert>
        )}
        {step.output && (
          <ScrollArea.Autosize mah={240} type="auto">
            <Code block>{JSON.stringify(step.output, null, 2)}</Code>
          </ScrollArea.Autosize>
        )}
      </Stack>
    </Paper>
  )
}
