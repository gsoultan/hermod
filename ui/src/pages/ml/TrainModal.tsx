import { useState } from 'react'
import {
  Alert, Badge, Button, Group, Modal, MultiSelect, Select, Stack, Table, Text, TextInput,
} from '@mantine/core'
import { IconAlertCircle, IconCircleCheck, IconInfoCircle } from '@tabler/icons-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  DEVICE_OPTIONS, MODEL_NAME_PATTERN, TASK_OPTIONS, algorithmOptions, isCustomAlgorithm, isDeepAlgorithm, mlModelsKey, modelNameRule,
  trainModel, useDataset, useVHostDatasets, useVHostScripts, useWorkerStatus, versionsKey, type GoLive, type TrainAlgorithm,
  type TrainDevice, type TrainParams, type TrainResult, type TrainTask,
} from '@/lib/mlModels'
import { DeepParamsFields, readDeepParams, type DeepParamsText } from './deepParams'
import { GoLiveField } from './goLive'

/** Shows a metric the way people read it: four significant digits. */
export const formatMetric = (v: number) => (Number.isInteger(v) ? String(v) : v.toPrecision(4))

/**
 * Trains a model on one of the vhost's datasets: a new model, or a new version
 * of `modelName`. The worker holds back a fifth of the rows to score it, and
 * the result says how it did and whether it went live.
 */
export function TrainModal({ vhost, modelName, onClose }: { vhost: string; modelName?: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(modelName ?? '')
  const [dataset, setDataset] = useState<string | null>(null)
  const [target, setTarget] = useState<string | null>(null)
  const [features, setFeatures] = useState<string[]>([])
  const [task, setTask] = useState<TrainTask>('auto')
  const [algorithm, setAlgorithm] = useState<TrainAlgorithm>('auto')
  const [device, setDevice] = useState<TrainDevice>('cpu')
  const [goLive, setGoLive] = useState<GoLive>({ mode: 'never' })
  const [nameError, setNameError] = useState<string | null>(null)
  const [deepText, setDeepText] = useState<DeepParamsText>({})
  const [deepErrors, setDeepErrors] = useState<ReturnType<typeof readDeepParams>['errors']>({})

  const { data: worker } = useWorkerStatus()
  const { data: datasets = [] } = useVHostDatasets(vhost)
  const { data: info } = useDataset(vhost, dataset)
  const columns = (info?.columns ?? []).map((c) => c.name)
  const caps = worker?.capabilities
  const { data: scripts = [] } = useVHostScripts(vhost, !!caps?.custom_scripts)
  const custom = isCustomAlgorithm(algorithm)
  // A script trains on its own pool; the device applies to built-in algorithms.
  const showDevice = !!caps?.gpu && !custom
  const deep = isDeepAlgorithm(algorithm)

  const train = useMutation({
    mutationFn: (params: TrainParams | undefined) =>
      trainModel(vhost, name.trim(), {
        dataset: dataset as string,
        target: target as string,
        features: features.length ? features : undefined,
        task,
        algorithm,
        device: showDevice && device === 'gpu' ? 'gpu' : undefined,
        params: deep ? params : undefined,
        go_live: goLive,
      }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) })
      queryClient.invalidateQueries({ queryKey: versionsKey(vhost, res.model?.name ?? name.trim()) })
    },
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const badName = !MODEL_NAME_PATTERN.test(name.trim())
    setNameError(badName ? `"${name.trim()}" is not a valid name.` : null)
    const { params, errors } = deep ? readDeepParams(deepText) : { params: undefined, errors: {} }
    setDeepErrors(errors)
    if (badName || !dataset || !target || Object.keys(errors).length) return
    train.mutate(params)
  }

  return (
    <Modal opened onClose={onClose} title={modelName ? `Train ${modelName} again` : 'Train a model'} centered size="lg">
      <form onSubmit={submit} noValidate>
        <Stack gap="md">
          {!modelName && (
            <TextInput label="Model name" description={modelNameRule} placeholder="churn" required value={name}
              onChange={(e) => { setName(e.currentTarget.value); setNameError(null) }} error={nameError} data-autofocus />
          )}
          <Select label="Dataset" placeholder={datasets.length ? 'Choose a dataset' : 'No datasets yet'} required searchable
            data={datasets.map((d) => ({ value: d.name, label: d.name }))} value={dataset}
            onChange={(v) => { setDataset(v); setTarget(null); setFeatures([]) }} nothingFoundMessage="No dataset by that name" />
          <Select label="Column to predict" placeholder="The target" required searchable disabled={!dataset}
            data={columns} value={target} onChange={(v) => { setTarget(v); setFeatures((f) => f.filter((x) => x !== v)) }} />
          <MultiSelect label="Learn from" description="Empty learns from every other column." searchable clearable
            disabled={!target} data={columns.filter((c) => c !== target)} value={features} onChange={setFeatures} />
          <Group grow align="flex-start">
            <Select label="Task" data={TASK_OPTIONS} value={task} allowDeselect={false} onChange={(v) => setTask((v ?? 'auto') as TrainTask)} />
            <Select label="Algorithm" data={algorithmOptions(worker, scripts)} value={algorithm} allowDeselect={false}
              onChange={(v) => setAlgorithm((v ?? 'auto') as TrainAlgorithm)} />
            {showDevice && (
              <Select label="Device" description="GPU trains on the GPU worker pool." data={DEVICE_OPTIONS} value={device}
                allowDeselect={false} onChange={(v) => setDevice((v ?? 'cpu') as TrainDevice)} />
            )}
          </Group>
          {custom && (
            <Text size="xs" c="dimmed">
              The script runs in a sandbox on the custom-script worker pool. Its ONNX export is checked and scored on the held-back rows
              like any other version.
            </Text>
          )}
          {deep && (
            <DeepParamsFields value={deepText} errors={deepErrors}
              onChange={(patch) => { setDeepText((t) => ({ ...t, ...patch })); setDeepErrors({}) }} />
          )}
          <GoLiveField value={goLive} onChange={setGoLive} />

          {train.error && (
            <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not trained">
              {(train.error as Error).message}
            </Alert>
          )}
          {train.data && <TrainOutcome result={train.data} />}

          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>{train.data ? 'Close' : 'Cancel'}</Button>
            <Button type="submit" loading={train.isPending} disabled={!dataset || !target}>Train</Button>
          </Group>
          {train.isPending && (
            <Text size="xs" c="dimmed" ta="right">Training runs on the ML worker; a large dataset takes a few minutes.</Text>
          )}
        </Stack>
      </form>
    </Modal>
  )
}

function TrainOutcome({ result }: { result: TrainResult }) {
  const v = result.version
  return (
    <Alert color={result.live ? 'green' : 'yellow'} icon={result.live ? <IconCircleCheck size="1rem" /> : <IconInfoCircle size="1rem" />}
      title={`Version ${v.version} trained${result.live ? ' and live' : ', not live'}`}>
      <Stack gap="xs">
        <Text size="sm">{result.reason}.</Text>
        <Group gap="xs">
          <Badge variant="light" tt="none">{v.task}</Badge>
          <Badge variant="light" tt="none">{v.algorithm}</Badge>
          {v.script && <Badge variant="light" color="grape" tt="none" title={v.script.sha256}>script {v.script.sha256.slice(0, 12)}</Badge>}
          <Badge variant="light" tt="none">{v.rows.train} rows trained, {v.rows.test} held back</Badge>
        </Group>
        <MetricsTable metrics={v.metrics} />
      </Stack>
    </Alert>
  )
}

export function MetricsTable({ metrics }: { metrics: Record<string, number> }) {
  const entries = Object.entries(metrics).filter(([k]) => k !== 'score')
  return (
    <Table withRowBorders={false} verticalSpacing={2} fz="sm">
      <Table.Tbody>
        {entries.map(([k, val]) => (
          <Table.Tr key={k}>
            <Table.Td c="dimmed" w={140}>{k}</Table.Td>
            <Table.Td ff="var(--mantine-font-family-monospace)">{formatMetric(val)}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}
