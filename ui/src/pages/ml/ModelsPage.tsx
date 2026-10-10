import { useMemo, useState } from 'react'
import {
  ActionIcon, Alert, Badge, Box, Button, Group, Loader, Paper, Select, Stack, Table, Text, Title, Tooltip,
} from '@mantine/core'
import {
  IconAlertCircle, IconBrain, IconHistory, IconInfoCircle, IconKey, IconPencil, IconPlayerPlay, IconPlus, IconRepeat, IconSchool, IconTrash,
} from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVHost } from '@/context/VHostContext'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import { deleteModel, isModelVHost, isTrainedModel, listModels, mlModelsKey, useWorkerStatus, type MLModel } from '@/lib/mlModels'
import { DatasetsPanel } from './DatasetsPanel'
import { ModelFormModal } from './ModelFormModal'
import { ModelTestModal } from './ModelTestModal'
import { RetrainModal } from './RetrainModal'
import { RetrainStatusLine } from './retrainStatus'
import { ServingKeyModal } from './ServingKeyModal'
import { TrainModal } from './TrainModal'
import { VersionsModal } from './VersionsModal'

const BACKEND_LABEL: Record<string, string> = { oip: 'Open Inference', mlflow: 'MLflow', 'hermod-ml': 'Trained in Hermod' }

/** Where a model is served, in words. */
function servedFrom(m: MLModel): string {
  if (isTrainedModel(m)) return m.remote_version ? `hermod-ml, version ${m.remote_version} live` : 'hermod-ml, no version live yet'
  return `${m.url}${m.remote_model ? ` → ${m.remote_model}${m.remote_version ? `:${m.remote_version}` : ''}` : ''}`
}

type Dialog =
  | { kind: 'form'; model?: MLModel }
  | { kind: 'test'; model: MLModel }
  | { kind: 'key'; model: MLModel }
  | { kind: 'train'; modelName?: string }
  | { kind: 'versions'; model: MLModel }
  | { kind: 'retrain'; model: MLModel }

/**
 * The models a vhost can call. Each is the address of a model on a model
 * server; workflows call it from a Predict node, and applications through the
 * serving endpoint with the model's own key.
 */
export function ModelsPage() {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const { selectedVHost, availableVHosts } = useVHost()
  const [chosen, setChosen] = useState<string | null>(null)
  const vhost = isModelVHost(selectedVHost) ? selectedVHost : chosen
  const vhostOptions = useMemo(
    () => Array.from(new Set(['default', ...availableVHosts.filter((v) => isModelVHost(v))])),
    [availableVHosts],
  )
  const [dialog, setDialog] = useState<Dialog | undefined>(undefined)
  const { data: worker } = useWorkerStatus()
  const canTrain = !!worker?.ready

  const { data: models, isLoading, error } = useQuery({
    queryKey: mlModelsKey(vhost ?? ''),
    queryFn: ({ signal }) => listModels(vhost as string, signal),
    enabled: !!vhost,
    retry: false,
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteModel(vhost as string, name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: mlModelsKey(vhost as string) }),
  })

  const askDelete = async (name: string) => {
    const ok = await confirm({
      title: 'Delete model',
      message: `Delete ${name} from ${vhost}?`,
      consequence: 'Predict nodes that use it start failing, and its serving key stops working. A model trained here loses its versions; a model served elsewhere is not touched.',
      confirmLabel: 'Delete model',
      danger: true,
    })
    if (ok) remove.mutate(name)
  }

  const rows = (models ?? []).map((m) => (
    <Table.Tr key={m.name}>
      <Table.Td>
        <Text fw={500} ff="var(--mantine-font-family-monospace)">{m.name}</Text>
        {m.description && <Text size="xs" c="dimmed">{m.description}</Text>}
        <RetrainStatusLine model={m} />
      </Table.Td>
      <Table.Td><Badge variant="light" tt="none">{BACKEND_LABEL[m.backend] ?? m.backend}</Badge></Table.Td>
      <Table.Td><Text size="sm" ff="var(--mantine-font-family-monospace)">{servedFrom(m)}</Text></Table.Td>
      <Table.Td>{m.serving ? <Badge color="green" variant="light">On</Badge> : <Badge color="gray" variant="light">Off</Badge>}</Table.Td>
      <Table.Td>
        <Group justify="flex-end" gap="xs" wrap="nowrap">
          <Tooltip label="Test with one row">
            <ActionIcon variant="light" radius="md" aria-label={`Test ${m.name}`} onClick={() => setDialog({ kind: 'test', model: m })}>
              <IconPlayerPlay size="1.1rem" />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Serving key">
            <ActionIcon variant="light" color="teal" radius="md" aria-label={`Serving key for ${m.name}`} onClick={() => setDialog({ kind: 'key', model: m })}>
              <IconKey size="1.1rem" />
            </ActionIcon>
          </Tooltip>
          {isTrainedModel(m) ? (
            <>
              <Tooltip label="Versions">
                <ActionIcon variant="light" color="grape" radius="md" aria-label={`Versions of ${m.name}`} onClick={() => setDialog({ kind: 'versions', model: m })}>
                  <IconHistory size="1.1rem" />
                </ActionIcon>
              </Tooltip>
              {canTrain && (
                <Tooltip label="Train again">
                  <ActionIcon variant="light" color="blue" radius="md" aria-label={`Train ${m.name} again`} onClick={() => setDialog({ kind: 'train', modelName: m.name })}>
                    <IconSchool size="1.1rem" />
                  </ActionIcon>
                </Tooltip>
              )}
              {canTrain && (
                <Tooltip label="Retrain automatically">
                  <ActionIcon variant={m.retrain ? 'filled' : 'light'} color="indigo" radius="md" aria-label={`Retrain ${m.name} automatically`}
                    onClick={() => setDialog({ kind: 'retrain', model: m })}>
                    <IconRepeat size="1.1rem" />
                  </ActionIcon>
                </Tooltip>
              )}
            </>
          ) : (
            <Tooltip label="Edit">
              <ActionIcon variant="light" color="blue" radius="md" aria-label={`Edit ${m.name}`} onClick={() => setDialog({ kind: 'form', model: m })}>
                <IconPencil size="1.1rem" />
              </ActionIcon>
            </Tooltip>
          )}
          <Tooltip label="Delete">
            <ActionIcon variant="light" color="red" radius="md" aria-label={`Delete ${m.name}`} onClick={() => askDelete(m.name)}>
              <IconTrash size="1.1rem" />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Table.Td>
    </Table.Tr>
  ))

  return (
    <Box p="md" className="page-enter">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Stack gap="md">
            <Group gap="sm" align="flex-start">
              <IconBrain size="2rem" color="var(--mantine-color-grape-filled)" />
              <Box style={{ flex: 1 }}>
                <Group gap="sm" align="center">
                  <Title order={2} fw={800}>Models</Title>
                  {vhost && <Badge variant="light" size="lg" radius="sm" tt="none" aria-label="VHost shown">{vhost}</Badge>}
                </Group>
                <Text size="sm" c="dimmed">
                  Machine-learning models this vhost can call: trained here on a dataset, or running on a model server
                  (KServe, Triton, MLServer or MLflow). Workflows call them from the Predict node, and applications
                  over REST and gRPC.
                </Text>
              </Box>
              {vhost && (
                <Group gap="xs">
                  {canTrain && (
                    <Button leftSection={<IconSchool size="1.2rem" />} onClick={() => setDialog({ kind: 'train' })}>Train a model</Button>
                  )}
                  <Button variant={canTrain ? 'light' : 'filled'} leftSection={<IconPlus size="1.2rem" />} onClick={() => setDialog({ kind: 'form' })}>Add model</Button>
                </Group>
              )}
            </Group>
            {!isModelVHost(selectedVHost) && (
              <Select label="VHost" description="A model belongs to one vhost." placeholder="Select one"
                data={vhostOptions} value={chosen} onChange={setChosen} allowDeselect={false} maw={360} />
            )}
          </Stack>
        </Paper>

        {worker && !worker.ready && (
          <Alert color={worker.configured ? 'yellow' : 'gray'} variant="light" icon={<IconInfoCircle size="1rem" />}
            title={worker.configured ? 'The ML worker does not answer' : 'Training is off'}>
            {worker.configured
              ? 'Hermod has an ML worker configured, but it does not answer. Models served elsewhere still work.'
              : 'Training models needs the hermod-ml worker. Run it beside Hermod and set HERMOD_ML_WORKER_URL (and HERMOD_ML_WORKER_TOKEN). Models served elsewhere work without it.'}
          </Alert>
        )}

        {remove.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not deleted">
            {(remove.error as Error).message}
          </Alert>
        )}

        <Paper radius="md" withBorder style={{ overflow: 'hidden' }}>
          {!vhost ? (
            <EmptyState compact icon={<IconBrain size="1.3rem" />} title="Choose a vhost"
              description="Models are kept per vhost. Pick one above to see and manage its models." />
          ) : isLoading ? (
            <Group justify="center" p="xl"><Loader size="sm" /><Text size="sm" c="dimmed">Loading models…</Text></Group>
          ) : error ? (
            <Alert m="md" color="red" icon={<IconAlertCircle size="1rem" />} title={`The models of ${vhost} could not be loaded`}>
              {(error as Error).message}
            </Alert>
          ) : rows.length === 0 ? (
            <EmptyState compact icon={<IconBrain size="1.3rem" />} title={`No models in ${vhost} yet`}
              description="Add the address of a model server and the model's name on it. Then score records with a Predict node."
              action={{ label: 'Add model', onClick: () => setDialog({ kind: 'form' }) }} />
          ) : (
            <Table.ScrollContainer minWidth={760}>
              <Table verticalSpacing="sm" horizontalSpacing="lg">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Protocol</Table.Th>
                    <Table.Th>Served from</Table.Th>
                    <Table.Th>Serving</Table.Th>
                    <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>{rows}</Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Paper>
        {vhost && canTrain && <DatasetsPanel vhost={vhost} />}
      </Stack>

      {vhost && dialog?.kind === 'train' && <TrainModal vhost={vhost} modelName={dialog.modelName} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'retrain' && <RetrainModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'versions' && <VersionsModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'form' && <ModelFormModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'test' && <ModelTestModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'key' && <ServingKeyModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
    </Box>
  )
}
