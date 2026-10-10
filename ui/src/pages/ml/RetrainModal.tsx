import { useState } from 'react'
import {
  Alert, Button, Group, Modal, MultiSelect, NumberInput, Select, Stack, Text, TextInput,
} from '@mantine/core'
import { IconAlertCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ALGORITHM_OPTIONS, TASK_OPTIONS, clearRetrainPolicy, mlModelsKey, setRetrainPolicy, useDataset, useVHostDatasets,
  type GoLive, type MLModel, type TrainAlgorithm, type TrainTask,
} from '@/lib/mlModels'
import { GoLiveField } from './goLive'
import { RetrainStatusLine } from './retrainStatus'

/**
 * Makes a trained model train again by itself: on a cron schedule, once its
 * dataset has grown by a number of rows, or both. Each retraining is a new
 * version; the go-live rule decides whether it serves.
 */
export function RetrainModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const queryClient = useQueryClient()
  const current = model.retrain
  const [schedule, setSchedule] = useState(current?.schedule ?? '')
  const [newRows, setNewRows] = useState<number | ''>(current?.new_rows || '')
  const [dataset, setDataset] = useState<string | null>(current?.spec.dataset ?? null)
  const [target, setTarget] = useState<string | null>(current?.spec.target ?? null)
  const [features, setFeatures] = useState<string[]>(current?.spec.features ?? [])
  const [task, setTask] = useState<TrainTask>(current?.spec.task ?? 'auto')
  const [algorithm, setAlgorithm] = useState<TrainAlgorithm>(current?.spec.algorithm ?? 'auto')
  const [goLive, setGoLive] = useState<GoLive>(current?.go_live ?? { mode: 'never' })

  const { data: datasets = [] } = useVHostDatasets(vhost)
  const { data: info } = useDataset(vhost, dataset)
  const columns = (info?.columns ?? []).map((c) => c.name)

  const done = () => {
    queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
    onClose()
  }
  const save = useMutation({
    mutationFn: () =>
      setRetrainPolicy(vhost, model.name, {
        schedule: schedule.trim() || undefined,
        new_rows: newRows || undefined,
        spec: {
          dataset: dataset as string,
          target: target as string,
          features: features.length ? features : undefined,
          task,
          algorithm,
        },
        go_live: goLive,
      }),
    onSuccess: done,
  })
  const stop = useMutation({ mutationFn: () => clearRetrainPolicy(vhost, model.name), onSuccess: done })

  const hasTrigger = !!schedule.trim() || (typeof newRows === 'number' && newRows > 0)
  const ready = hasTrigger && !!dataset && !!target
  const failure = (save.error ?? stop.error) as Error | null

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (ready) save.mutate()
  }

  return (
    <Modal opened onClose={onClose} title={`Retrain ${model.name} automatically`} centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            Trains a new version by itself, at most one at a time across every Hermod server. Set a schedule, a row
            count, or both; whichever comes first starts the next training.
          </Text>
          <RetrainStatusLine model={model} />
          <Group grow align="flex-start">
            <TextInput label="Schedule" description='Cron, e.g. "0 3 * * *" or "@daily". Empty: no schedule.'
              placeholder="@daily" value={schedule} onChange={(e) => setSchedule(e.currentTarget.value)} />
            <NumberInput label="After this many new rows" description="Rows added to the dataset since it last trained."
              placeholder="1000" min={1} allowDecimal={false} value={newRows}
              onChange={(n) => setNewRows(typeof n === 'number' && n > 0 ? n : '')} />
          </Group>
          <Select label="Dataset" placeholder={datasets.length ? 'Choose a dataset' : 'No datasets yet'} required searchable
            data={datasets.map((d) => ({ value: d.name, label: d.name }))} value={dataset}
            onChange={(v) => { setDataset(v); setTarget(null); setFeatures([]) }} nothingFoundMessage="No dataset by that name" />
          <Select label="Column to predict" placeholder="The target" required searchable disabled={!dataset}
            data={target && !columns.includes(target) ? [target, ...columns] : columns} value={target}
            onChange={(v) => { setTarget(v); setFeatures((f) => f.filter((x) => x !== v)) }} />
          <MultiSelect label="Learn from" description="Empty learns from every other column." searchable clearable
            disabled={!target} data={Array.from(new Set([...features, ...columns.filter((c) => c !== target)]))}
            value={features} onChange={setFeatures} />
          <Group grow align="flex-start">
            <Select label="Task" data={TASK_OPTIONS} value={task} allowDeselect={false} onChange={(v) => setTask((v ?? 'auto') as TrainTask)} />
            <Select label="Algorithm" data={ALGORITHM_OPTIONS} value={algorithm} allowDeselect={false}
              onChange={(v) => setAlgorithm((v ?? 'auto') as TrainAlgorithm)} />
          </Group>
          <GoLiveField value={goLive} onChange={setGoLive} />

          {failure && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The retrain policy was not changed">
              {failure.message}
            </Alert>
          )}

          <Group justify="space-between">
            {current ? (
              <Button variant="subtle" color="red" loading={stop.isPending} onClick={() => stop.mutate()}>Stop retraining</Button>
            ) : <span />}
            <Group gap="xs">
              <Button variant="default" onClick={onClose}>Cancel</Button>
              <Button type="submit" loading={save.isPending} disabled={!ready}>Save</Button>
            </Group>
          </Group>
          {!hasTrigger && (
            <Text size="xs" c="dimmed" ta="right">Set a schedule or a number of new rows.</Text>
          )}
        </Stack>
      </form>
    </Modal>
  )
}
