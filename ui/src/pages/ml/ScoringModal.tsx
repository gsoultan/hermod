import { useState } from 'react'
import { Alert, Button, Group, Loader, Modal, Radio, Stack, Text } from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getScoring, mlModelsKey, setScoring, type MLModel, type Scoring, type ScoringStatus } from '@/lib/mlModels'

/** The status in words: who scores the live version, and why. */
function StatusLine({ status }: { status: ScoringStatus }) {
  const version = status.version ? `Version ${status.version}` : 'The live version'
  if (status.scoring !== 'in_process') {
    return <Text size="sm">{version} is scored by the ML worker.</Text>
  }
  if (status.in_process) {
    return (
      <Stack gap={4}>
        <Text size="sm" c="teal">{version} is scored in-process.</Text>
        {!!status.ops?.length && <Text size="xs" c="dimmed">Operators: {status.ops.join(', ')}</Text>}
      </Stack>
    )
  }
  return (
    <Alert color="yellow" variant="light" icon={<IconAlertCircle size="1rem" />} title={`${version} falls back to the ML worker`}>
      {status.reason ?? 'Its graph could not be scored in-process.'}
    </Alert>
  )
}

/**
 * Sets where a trained model is scored: on the ML worker over HTTP, or inside
 * Hermod from the live version's graph when every operator in it is supported.
 * A graph that is not supported, or a row the scorer will not guess at, goes
 * to the worker as before.
 */
export function ScoringModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const queryClient = useQueryClient()
  const key = ['ml-scoring', vhost, model.name]
  const { data: status, isLoading, error } = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getScoring(vhost, model.name, signal),
    retry: false,
  })
  const [choice, setChoice] = useState<Scoring | null>(null)
  const selected = choice ?? status?.scoring ?? model.scoring ?? 'worker'

  const save = useMutation({
    mutationFn: () => setScoring(vhost, model.name, selected),
    onSuccess: (next) => {
      queryClient.setQueryData(key, next)
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      setChoice(null)
    },
  })
  const failure = (save.error ?? error) as Error | null

  return (
    <Modal opened onClose={onClose} title={`Scoring of ${model.name}`} centered>
      <Stack gap="md">
        <Radio.Group label="Where predictions are computed" value={selected} onChange={(v) => setChoice(v as Scoring)}>
          <Stack gap="xs" mt="xs">
            <Radio value="worker" label="ML worker" description="Every prediction is an HTTP call to the worker." />
            <Radio value="in_process" label="In-process"
              description="Linear and tree models are scored inside Hermod, in microseconds. Anything else falls back to the worker." />
          </Stack>
        </Radio.Group>
        {isLoading ? <Loader size="sm" /> : status && <StatusLine status={status} />}
        {failure && <Alert color="red" variant="light" icon={<IconAlertCircle size="1rem" />}>{failure.message}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Close</Button>
          <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!status || selected === status.scoring}>Save</Button>
        </Group>
      </Stack>
    </Modal>
  )
}
