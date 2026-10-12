import { useEffect, useMemo, useState } from 'react'
import {
  Alert, Badge, Box, Button, Group, Loader, Paper, Select, Stack, Switch, Table, Tabs, Text, Title, Tooltip, VisuallyHidden,
} from '@mantine/core'
import {
  IconAlertCircle, IconBrain, IconCode, IconDatabase, IconGauge, IconListCheck, IconPlus,
} from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVHost } from '@/context/VHostContext'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import { useSessionStore } from '@/auth/session'
import {
  deleteModel, isModelVHost, isTrainedModel, listModels, mlModelsKey, setMCPExposure, trainBlockedReason, useVHostDatasets, useWorkerStatus,
  workerState, type MLModel,
} from '@/lib/mlModels'
import { AddModelModal } from './AddModelModal'
import { DATASETS_HEADING_ID, DatasetsPanel } from './DatasetsPanel'
import { GettingStarted } from './GettingStarted'
import { JobsPanel } from './JobsPanel'
import { ModelActionsMenu, type ModelAction } from './ModelActionsMenu'
import { ScriptsPanel } from './ScriptsPanel'
import { ModelFormModal } from './ModelFormModal'
import { ModelTestModal } from './ModelTestModal'
import { MonitoringModal } from './MonitoringModal'
import { QuotasPanel } from './QuotasPanel'
import { RetrainModal } from './RetrainModal'
import { RetrainStatusLine } from './retrainStatus'
import { ScoringModal } from './ScoringModal'
import { ServingKeyModal } from './ServingKeyModal'
import { TrainModal } from './TrainModal'
import { VersionsModal } from './VersionsModal'
import { WorkerSetupModal } from './WorkerSetupModal'

const SERVER_LABEL: Record<string, string> = { oip: 'On an Open Inference server', mlflow: 'On an MLflow server' }

/** Where a model runs, in words, and the address of one served elsewhere. */
function WhereItRuns({ m }: { m: MLModel }) {
  if (isTrainedModel(m)) return <Text size="sm">Trained in Hermod</Text>
  const target = `${m.url}${m.remote_model ? ` → ${m.remote_model}${m.remote_version ? `:${m.remote_version}` : ''}` : ''}`
  return (
    <>
      <Text size="sm">{SERVER_LABEL[m.backend] ?? m.backend}</Text>
      <Text size="xs" c="dimmed" ff="var(--mantine-font-family-monospace)" style={{ wordBreak: 'break-all' }}>{target}</Text>
    </>
  )
}

/** Whether a model answers predictions now, in one badge. */
function StatusBadge({ m }: { m: MLModel }) {
  if (!isTrainedModel(m)) return <Badge color="blue" variant="light" tt="none">Connected</Badge>
  if (m.remote_version) return <Badge color="green" variant="light" tt="none">Version {m.remote_version} live</Badge>
  return (
    <Tooltip label="Open … → Versions and going live to put a version live.">
      <Badge color="yellow" variant="light" tt="none">Not live yet</Badge>
    </Tooltip>
  )
}

type Dialog =
  | { kind: 'add' }
  | { kind: 'form'; model?: MLModel }
  | { kind: 'test'; model: MLModel }
  | { kind: 'key'; model: MLModel }
  | { kind: 'train'; modelName?: string; task?: string }
  | { kind: 'versions'; model: MLModel }
  | { kind: 'monitoring'; model: MLModel }
  | { kind: 'retrain'; model: MLModel }
  | { kind: 'scoring'; model: MLModel }
  | { kind: 'setup' }

/** The page's tabs, each with the one line that says what it holds. */
const TABS = [
  { value: 'models', label: 'Models', icon: IconBrain, help: 'The models this vhost can use, and what you can do with each.' },
  { value: 'data', label: 'Data', icon: IconDatabase, help: 'The rows models learn from: upload a spreadsheet or read them from a database.' },
  { value: 'jobs', label: 'Training jobs', icon: IconListCheck, help: 'Trainings and other long tasks, running or finished, with their progress.' },
  { value: 'scripts', label: 'Scripts', icon: IconCode, help: 'Your own Python training code, for experts. Most people never need it.' },
  { value: 'limits', label: 'Limits', icon: IconGauge, help: 'How much this vhost may store and run at once.' },
] as const

const tabHelpId = (value: string) => `ml-tab-help-${value}`

/**
 * Machine Learning: the models a vhost can use. Each is trained here on a
 * dataset, or is the address of a model on a model server; workflows call it
 * from a Predict node, and applications through the serving endpoint with the
 * model's own key. Getting started at the top walks a newcomer through the
 * worker, data, training and use. The page has one primary action, Add model,
 * which asks whether to train or connect; each model's other actions sit in
 * its "…" menu, and datasets, jobs, scripts and limits have their own tabs.
 * An action that cannot run yet stays offered, off, and says why.
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
  const blockedReason = trainBlockedReason(worker)
  const { data: datasets } = useVHostDatasets(vhost ?? undefined, canTrain)
  // The Scripts tab exists only where the server has custom scripts on.
  const showScripts = canTrain && !!worker?.capabilities?.scripts_enabled
  const tabs = TABS.filter((t) => t.value !== 'scripts' || showScripts)
  const [tab, setTab] = useState<string | null>('models')
  const role = useSessionStore((s) => s.user?.role)
  const username = useSessionStore((s) => s.user?.username) ?? ''
  // Getting started's "Go to Datasets": show the Data tab, then move focus to
  // the Datasets heading once it is on the page.
  const [toDatasets, setToDatasets] = useState(0)
  useEffect(() => {
    if (!toDatasets) return
    const heading = document.getElementById(DATASETS_HEADING_ID)
    heading?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    heading?.focus({ preventScroll: true })
  }, [toDatasets])
  const goToDatasets = () => {
    setTab('data')
    setToDatasets((n) => n + 1)
  }
  const openSetup = () => setDialog({ kind: 'setup' })
  const canEdit = role === 'Administrator' || role === 'Editor'
  // While the worker status is still arriving there is nothing to set up yet.
  const setupFromAdd = blockedReason && workerState(worker) !== 'checking' ? openSetup : undefined

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

  const expose = useMutation({
    mutationFn: ({ name, exposed }: { name: string; exposed: boolean }) => setMCPExposure(vhost as string, name, exposed),
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

  const act = (m: MLModel, action: ModelAction) => {
    switch (action) {
      case 'delete': return void askDelete(m.name)
      case 'edit': return setDialog({ kind: 'form', model: m })
      case 'train': return setDialog({ kind: 'train', modelName: m.name, task: m.task })
      case 'test': case 'key': case 'versions': case 'monitoring': case 'retrain': case 'scoring':
        return setDialog({ kind: action, model: m })
    }
  }

  const rows = (models ?? []).map((m) => (
    <Table.Tr key={m.name}>
      <Table.Td>
        <Text fw={500} ff="var(--mantine-font-family-monospace)">{m.name}</Text>
        {m.description && <Text size="xs" c="dimmed">{m.description}</Text>}
        <RetrainStatusLine model={m} />
      </Table.Td>
      <Table.Td><WhereItRuns m={m} /></Table.Td>
      <Table.Td><StatusBadge m={m} /></Table.Td>
      <Table.Td>{m.serving ? <Badge color="green" variant="light" tt="none">Yes</Badge> : <Text size="sm" c="dimmed">No key yet</Text>}</Table.Td>
      <Table.Td>
        {canEdit ? (
          <Tooltip label={`Offer it to AI assistants as the predict_${m.name} tool on Hermod's MCP server`}>
            <Switch aria-label={`Expose ${m.name} to MCP`} checked={!!m.mcp_exposed}
              disabled={expose.isPending && expose.variables?.name === m.name}
              onChange={(e) => expose.mutate({ name: m.name, exposed: e.currentTarget.checked })} />
          </Tooltip>
        ) : m.mcp_exposed ? <Badge color="grape" variant="light" tt="none">Offered</Badge> : <Text size="sm" c="dimmed">No</Text>}
      </Table.Td>
      <Table.Td style={{ textAlign: 'right' }}>
        <ModelActionsMenu model={m} trainBlocked={blockedReason} onAction={(a) => act(m, a)} />
      </Table.Td>
    </Table.Tr>
  ))

  const modelsTable = (
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
          description="A model learns from past examples, such as old orders, to predict something new, such as whether a customer will order again. Press Add model to train one or to connect one you already have."
          action={{ label: 'Add model', onClick: () => setDialog({ kind: 'add' }) }} />
      ) : (
        <Table.ScrollContainer minWidth={760}>
          <Table verticalSpacing="sm" horizontalSpacing="lg" aria-label="Models">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Model</Table.Th>
                <Table.Th>Where it runs</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Apps can call it</Table.Th>
                <Table.Th>AI assistants (MCP)</Table.Th>
                <Table.Th><VisuallyHidden>Actions</VisuallyHidden></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>{rows}</Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </Paper>
  )

  return (
    <Box p="md" className="page-enter">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Stack gap="md">
            <Group gap="sm" align="flex-start">
              <IconBrain size="2rem" color="var(--mantine-color-grape-filled)" />
              <Box style={{ flex: 1 }}>
                <Group gap="sm" align="center">
                  <Title order={2} fw={800}>Machine Learning</Title>
                  {vhost && <Badge variant="light" size="lg" radius="sm" tt="none" aria-label="VHost shown">{vhost}</Badge>}
                </Group>
                <Text size="sm" c="dimmed">
                  Models predict things from your data, such as which customers may leave. Workflows use them in a
                  Predict node, and applications call them with a key.
                </Text>
              </Box>
              {vhost && (
                <Button leftSection={<IconPlus size="1.2rem" />} onClick={() => setDialog({ kind: 'add' })}>Add model</Button>
              )}
            </Group>
            {!isModelVHost(selectedVHost) && (
              <Select label="VHost" description="A model belongs to one vhost." placeholder="Select one"
                data={vhostOptions} value={chosen} onChange={setChosen} allowDeselect={false} maw={360} />
            )}
          </Stack>
        </Paper>

        {vhost && (
          <GettingStarted key={username} worker={worker} datasets={canTrain ? datasets?.length : undefined} models={models}
            username={username} onSetup={openSetup} onDatasets={goToDatasets} onTrain={() => setDialog({ kind: 'train' })}
            onTry={(model) => setDialog({ kind: 'test', model })} onMakeLive={(model) => setDialog({ kind: 'versions', model })} />
        )}

        {expose.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="MCP exposure was not changed">
            {(expose.error as Error).message}
          </Alert>
        )}

        {remove.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The model was not deleted">
            {(remove.error as Error).message}
          </Alert>
        )}

        {vhost ? (
          <Tabs value={tab} onChange={setTab} keepMounted={false}>
            <Tabs.List>
              {tabs.map((t) => (
                <Tabs.Tab key={t.value} value={t.value} leftSection={<t.icon size="1rem" />} aria-describedby={tabHelpId(t.value)}>
                  {t.label}
                </Tabs.Tab>
              ))}
            </Tabs.List>
            {tabs.map((t) => (
              <Text key={t.value} id={tabHelpId(t.value)} size="sm" c="dimmed" mt="xs" mb="md" hidden={tab !== t.value}>
                {t.help}
              </Text>
            ))}
            <Tabs.Panel value="models">{modelsTable}</Tabs.Panel>
            <Tabs.Panel value="data">
              <DatasetsPanel vhost={vhost} blockedReason={blockedReason} onSetup={openSetup} />
            </Tabs.Panel>
            <Tabs.Panel value="jobs">
              <JobsPanel vhost={vhost} canEdit={canEdit} />
            </Tabs.Panel>
            {showScripts && (
              <Tabs.Panel value="scripts">
                <ScriptsPanel vhost={vhost} capabilities={worker?.capabilities} />
              </Tabs.Panel>
            )}
            <Tabs.Panel value="limits">
              <QuotasPanel vhost={vhost} canEdit={role === 'Administrator'} />
            </Tabs.Panel>
          </Tabs>
        ) : modelsTable}
      </Stack>

      {vhost && dialog?.kind === 'add' && (
        <AddModelModal trainBlocked={blockedReason} onSetup={setupFromAdd} onTrain={() => setDialog({ kind: 'train' })}
          onConnect={() => setDialog({ kind: 'form' })} onClose={() => setDialog(undefined)} />
      )}
      {vhost && dialog?.kind === 'train' && <TrainModal vhost={vhost} modelName={dialog.modelName} task={dialog.task} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'retrain' && <RetrainModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'scoring' && <ScoringModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'versions' && <VersionsModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'form' && <ModelFormModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'test' && <ModelTestModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'monitoring' && <MonitoringModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
      {dialog?.kind === 'setup' && <WorkerSetupModal onClose={() => setDialog(undefined)} />}
      {vhost && dialog?.kind === 'key' && <ServingKeyModal vhost={vhost} model={dialog.model} onClose={() => setDialog(undefined)} />}
    </Box>
  )
}
