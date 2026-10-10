import { useState } from 'react'
import { Alert, Badge, Button, Code, Group, Loader, Modal, Select, Stack, Table, Tabs, Text, TextInput } from '@mantine/core'
import { IconAlertCircle, IconCheck } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSessionRole } from '@/auth/session'
import {
  driftKey, getDrift, isTrainedModel, listPredictionLogs, MASK_TYPE_OPTIONS, mlModelsKey, predictionLogsKey, saveMonitoring,
  type DriftLevel, type MLModel, type MLMonitoring, type PredictionLog,
} from '@/lib/mlModels'

const LEVEL_COLOR: Record<DriftLevel, string> = { ok: 'green', warn: 'yellow', alert: 'red' }

const CALLER_LABEL: Record<string, string> = { workflow: 'Workflow', rest: 'REST', grpc: 'gRPC', ui: 'Console' }

/**
 * How a model is doing in use: the predictions it was asked for (when
 * logging is on), how far its live inputs moved from the data it was trained
 * on, and, for an Editor, the setting that controls both.
 */
export function MonitoringModal({ vhost, model, onClose }: { vhost: string; model: MLModel; onClose: () => void }) {
  const canEdit = getSessionRole() !== 'Viewer'
  return (
    <Modal opened onClose={onClose} title={`Monitoring of ${model.name}`} centered size="xl">
      <Tabs defaultValue="logs" keepMounted={false}>
        <Tabs.List mb="md">
          <Tabs.Tab value="logs">Prediction log</Tabs.Tab>
          <Tabs.Tab value="drift">Drift</Tabs.Tab>
          {canEdit && <Tabs.Tab value="settings">Settings</Tabs.Tab>}
        </Tabs.List>
        <Tabs.Panel value="logs"><PredictionLogPanel vhost={vhost} model={model} /></Tabs.Panel>
        <Tabs.Panel value="drift"><DriftPanel vhost={vhost} model={model} /></Tabs.Panel>
        {canEdit && <Tabs.Panel value="settings"><SettingsPanel vhost={vhost} model={model} /></Tabs.Panel>}
      </Tabs>
    </Modal>
  )
}

const compact = (v: Record<string, unknown>) => JSON.stringify(v)

function PredictionLogPanel({ vhost, model }: { vhost: string; model: MLModel }) {
  const { data: logs, isLoading, error } = useQuery({
    queryKey: predictionLogsKey(vhost, model.name),
    queryFn: ({ signal }) => listPredictionLogs(vhost, model.name, 100, signal),
    retry: false,
  })
  if (isLoading) return <Group justify="center" p="lg"><Loader size="sm" /></Group>
  if (error) {
    return <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The prediction log could not be read">{(error as Error).message}</Alert>
  }
  if (!logs?.length) {
    return (
      <Text size="sm" c="dimmed">
        {model.monitoring?.log_sample_rate
          ? 'Nothing logged yet. Logged predictions show here, newest first.'
          : 'Prediction logging is off for this model. An Editor can turn it on under Settings.'}
      </Text>
    )
  }
  return (
    <Table.ScrollContainer minWidth={720}>
      <Table verticalSpacing="xs" fz="sm">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>When</Table.Th>
            <Table.Th>Caller</Table.Th>
            <Table.Th>Inputs</Table.Th>
            <Table.Th>Outputs</Table.Th>
            <Table.Th>Latency</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {logs.map((l: PredictionLog, i) => (
            <Table.Tr key={`${l.timestamp}-${i}`}>
              <Table.Td>
                <Text size="xs">{new Date(l.timestamp).toLocaleString()}</Text>
                {l.version && <Text size="xs" c="dimmed">version {l.version}</Text>}
              </Table.Td>
              <Table.Td>
                <Badge variant="light" tt="none" size="sm">{CALLER_LABEL[l.caller_kind] ?? l.caller_kind}</Badge>
                {l.caller_id && <Text size="xs" ff="var(--mantine-font-family-monospace)">{l.caller_id}</Text>}
              </Table.Td>
              <Table.Td><Code block fz="xs">{compact(l.inputs)}</Code></Table.Td>
              <Table.Td><Code block fz="xs">{compact(l.outputs)}</Code></Table.Td>
              <Table.Td><Text size="xs">{l.latency_ms.toFixed(1)} ms</Text></Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  )
}

function DriftPanel({ vhost, model }: { vhost: string; model: MLModel }) {
  const { data, isLoading, error } = useQuery({
    queryKey: driftKey(vhost, model.name),
    queryFn: ({ signal }) => getDrift(vhost, model.name, signal),
    retry: false,
    refetchInterval: 30_000,
  })
  if (isLoading) return <Group justify="center" p="lg"><Loader size="sm" /></Group>
  if (error) {
    return <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The drift report could not be read">{(error as Error).message}</Alert>
  }
  const report = data?.report
  if (!report) {
    return (
      <Stack gap="xs">
        <Text size="sm" c="dimmed">No drift report: {data?.reason ?? 'none yet'}.</Text>
        {data?.window && (
          <Text size="xs" c="dimmed">
            Live inputs are compared with the training data every {data.window}, once a window holds at least {data.min_rows} predictions.
          </Text>
        )}
        {!isTrainedModel(model) && (
          <Text size="xs" c="dimmed">Drift is measured for models trained in Hermod, whose versions keep their training statistics.</Text>
        )}
      </Stack>
    )
  }
  return (
    <Stack gap="sm">
      <Group gap="sm">
        <Badge color={LEVEL_COLOR[report.status]} variant="light">{report.status}</Badge>
        <Text size="sm">
          Version {report.version}, {report.rows} predictions from {new Date(report.window_start).toLocaleTimeString()} to{' '}
          {new Date(report.window_end).toLocaleTimeString()}.
        </Text>
      </Group>
      <Text size="xs" c="dimmed">
        Population stability index of each feature against the training split: warn at {report.warn}, alert at {report.alert}.
      </Text>
      <Table verticalSpacing="xs" fz="sm">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Feature</Table.Th>
            <Table.Th>Kind</Table.Th>
            <Table.Th>PSI</Table.Th>
            <Table.Th>Missing (live / training)</Table.Th>
            <Table.Th>Status</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {report.features.map((f) => (
            <Table.Tr key={f.feature}>
              <Table.Td ff="var(--mantine-font-family-monospace)">{f.feature}</Table.Td>
              <Table.Td>{f.kind}</Table.Td>
              <Table.Td ff="var(--mantine-font-family-monospace)">{f.psi.toFixed(3)}</Table.Td>
              <Table.Td>{pct(f.null_fraction)} / {pct(f.training_null_fraction)}</Table.Td>
              <Table.Td><Badge color={LEVEL_COLOR[f.status]} variant="light">{f.status}</Badge></Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}

const pct = (f: number) => `${(f * 100).toFixed(1)}%`

/** A number field's text: empty for unset. */
const text = (n: number | undefined) => (n ? String(n) : '')

function SettingsPanel({ vhost, model }: { vhost: string; model: MLModel }) {
  const queryClient = useQueryClient()
  const mon = model.monitoring ?? {}
  const [rate, setRate] = useState(text(mon.log_sample_rate))
  const [fields, setFields] = useState((mon.log_mask_fields ?? []).join(', '))
  const [maskType, setMaskType] = useState<string | null>(mon.log_mask_type || 'all')
  const [retention, setRetention] = useState(mon.log_retention ?? '')
  const [warn, setWarn] = useState(text(mon.drift_warn))
  const [alert, setAlert] = useState(text(mon.drift_alert))

  const num = (s: string) => (s.trim() === '' ? undefined : Number(s))
  const rateValue = num(rate)
  const rateError = rateValue !== undefined && !(rateValue >= 0 && rateValue <= 1) ? 'A number from 0 to 1' : undefined

  const save = useMutation({
    mutationFn: () => {
      const next: MLMonitoring = {
        log_sample_rate: rateValue ?? 0,
        log_mask_fields: fields.split(',').map((f) => f.trim()).filter(Boolean),
        log_mask_type: (maskType ?? 'all') as MLMonitoring['log_mask_type'],
        log_retention: retention.trim() || undefined,
        drift_warn: num(warn),
        drift_alert: num(alert),
      }
      return saveMonitoring(vhost, model.name, next)
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost) }),
  })

  return (
    <Stack gap="sm">
      <TextInput label="Share of predictions logged" description="0 turns logging off; 1 logs every prediction; 0.1 one in ten."
        placeholder="0" value={rate} onChange={(e) => setRate(e.currentTarget.value)} error={rateError} />
      <TextInput label="Fields to mask" description='Comma-separated input or output fields, dotted for nested ones; "*" masks every text value.'
        placeholder="email, customer.phone" value={fields} onChange={(e) => setFields(e.currentTarget.value)} />
      <Select label="How to mask" data={MASK_TYPE_OPTIONS} value={maskType} onChange={setMaskType} allowDeselect={false} />
      <TextInput label="Keep for" description='How long a logged prediction is kept, as "7d" or "12h". Empty keeps it 7 days.'
        placeholder="7d" value={retention} onChange={(e) => setRetention(e.currentTarget.value)} />
      <Group grow>
        <TextInput label="Drift warning at" description="PSI; empty means 0.1" placeholder="0.1" value={warn} onChange={(e) => setWarn(e.currentTarget.value)} />
        <TextInput label="Drift alert at" description="PSI; empty means 0.25" placeholder="0.25" value={alert} onChange={(e) => setAlert(e.currentTarget.value)} />
      </Group>
      {save.error && (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The setting was not saved">{(save.error as Error).message}</Alert>
      )}
      <Group justify="flex-end">
        {save.isSuccess && <Group gap={4}><IconCheck size="1rem" color="var(--mantine-color-green-filled)" /><Text size="sm">Saved</Text></Group>}
        <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!!rateError}>Save monitoring</Button>
      </Group>
    </Stack>
  )
}
