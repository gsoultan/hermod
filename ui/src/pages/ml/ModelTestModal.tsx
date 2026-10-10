import { useState } from 'react'
import { Alert, Button, Code, Group, Modal, Stack, Text, Textarea } from '@mantine/core'
import { IconAlertCircle, IconPlayerPlay } from '@tabler/icons-react'
import { useMutation } from '@tanstack/react-query'
import { predict, type MLModel } from '@/lib/mlModels'

/** A starting row: the model's declared features, each set to 0. */
function sampleRow(model: MLModel): string {
  const row = Object.fromEntries((model.features ?? []).map((f) => [f, 0]))
  return JSON.stringify(row, null, 2)
}

/** Sends one row to the model and shows what it answered. */
export function ModelTestModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const [row, setRow] = useState(() => sampleRow(model))
  const [parseError, setParseError] = useState<string | null>(null)
  const run = useMutation({ mutationFn: (instance: Record<string, unknown>) => predict(vhost, model.name, [instance]) })

  const submit = () => {
    let parsed: unknown
    try {
      parsed = JSON.parse(row)
    } catch {
      setParseError('The row must be a JSON object.')
      return
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      setParseError('The row must be a JSON object.')
      return
    }
    setParseError(null)
    run.mutate(parsed as Record<string, unknown>)
  }

  return (
    <Modal opened onClose={onClose} title={`Test ${model.name}`} centered size="lg">
      <Stack gap="md">
        <Textarea label="Row" description="One record as JSON, with the fields the model takes." autosize minRows={4} maxRows={14}
          value={row} onChange={(e) => setRow(e.currentTarget.value)} error={parseError}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)' } }} />
        <Group justify="flex-end">
          <Button leftSection={<IconPlayerPlay size="1rem" />} onClick={submit} loading={run.isPending}>Run</Button>
        </Group>
        {run.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model did not answer">
            {(run.error as Error).message}
          </Alert>
        )}
        {run.data && (
          <Stack gap={4}>
            <Text size="sm" fw={500}>Prediction</Text>
            <Code block>{JSON.stringify(run.data[0], null, 2)}</Code>
          </Stack>
        )}
      </Stack>
    </Modal>
  )
}
