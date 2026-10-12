import { useId, useState, type ReactNode } from 'react'
import { Badge, Box, Button, Group, Paper, Stack, Text, ThemeIcon, Title } from '@mantine/core'
import { IconCheck, IconDatabase, IconPlayerPlay, IconPlugConnected, IconRocket, IconSchool, IconTool } from '@tabler/icons-react'
import { hasDeepLearning, isTrainedModel, workerState, type MLModel, type WorkerStatus } from '@/lib/mlModels'

const storageKey = (username: string) => `hermod.ml.guide.hidden.${username}`

function readHidden(username: string): boolean {
  try {
    return localStorage.getItem(storageKey(username)) === '1'
  } catch {
    return false
  }
}

function writeHidden(username: string, hidden: boolean) {
  try {
    if (hidden) localStorage.setItem(storageKey(username), '1')
    else localStorage.removeItem(storageKey(username))
  } catch {
    // Without storage the choice lasts until the page is left.
  }
}

/** One step: its number or a tick, what it is, where it stands, and the one thing to do next. */
function Step({ n, title, done, children, action, blocked }: {
  n: number
  title: string
  done: boolean
  children: ReactNode
  /** The step's button; absent when there is nothing left to press. */
  action?: { label: string; icon: ReactNode; onClick: () => void; disabled?: boolean }
  /** Why the button is off, shown beside it. */
  blocked?: string
}) {
  const titleId = useId()
  const blockedId = useId()
  return (
    <Box component="li" aria-labelledby={titleId} style={{ listStyle: 'none' }}>
      <Group align="flex-start" gap="sm" wrap="nowrap">
        <ThemeIcon radius="xl" size="md" variant={done ? 'filled' : 'light'} color={done ? 'green' : 'gray'} aria-hidden>
          {done ? <IconCheck size="1rem" /> : <Text size="sm" fw={700}>{n}</Text>}
        </ThemeIcon>
        <Stack gap={4} style={{ flex: 1, minWidth: 0 }}>
          <Group gap="xs">
            <Text fw={600} id={titleId}>{title}</Text>
            <Badge size="sm" variant="light" color={done ? 'green' : 'gray'} tt="none">{done ? 'Done' : 'To do'}</Badge>
          </Group>
          {children}
          {action && (
            <Group gap="xs" mt={2}>
              <Button size="xs" variant={done ? 'default' : 'light'} leftSection={action.icon} onClick={action.onClick}
                disabled={action.disabled} aria-describedby={action.disabled && blocked ? blockedId : undefined}>
                {action.label}
              </Button>
              {action.disabled && blocked && <Text size="xs" c="dimmed" id={blockedId}>{blocked}</Text>}
            </Group>
          )}
        </Stack>
      </Group>
    </Box>
  )
}

/**
 * Getting started with machine learning, at the top of the page: the four
 * steps from nothing to a model in use, each with where it stands now and the
 * one button that moves it on. Once every step is done the person can hide
 * it; that is remembered for them in this browser.
 */
export function GettingStarted({ worker, datasets, models, username, onSetup, onDatasets, onTrain, onTry, onMakeLive }: {
  worker: WorkerStatus | undefined
  /** How many datasets the vhost has; undefined while unknown. */
  datasets: number | undefined
  models: MLModel[] | undefined
  username: string
  onSetup: () => void
  onDatasets: () => void
  onTrain: () => void
  onTry: (model: MLModel) => void
  onMakeLive: (model: MLModel) => void
}) {
  const headingId = useId()
  const [hidden, setHidden] = useState(() => readHidden(username))
  const state = workerState(worker)
  const ready = state === 'ready'
  const deep = hasDeepLearning(worker)
  const trained = (models ?? []).filter(isTrainedModel)
  const live = trained.find((m) => !!m.remote_version)
  const hasData = (datasets ?? 0) > 0
  const done = [ready, hasData, trained.length > 0, !!live]
  const allDone = done.every(Boolean)
  const connectFirst = 'Connect the worker first (step 1).'

  const hide = (h: boolean) => {
    writeHidden(username, h)
    setHidden(h)
  }

  if (hidden && allDone) {
    return (
      <Paper component="section" aria-labelledby={headingId} p="sm" withBorder radius="md">
        <Group justify="space-between" gap="xs">
          <Group gap="xs">
            <Title order={3} size="h5" id={headingId}>Getting started</Title>
            <Text size="sm" c="dimmed">All four steps are done.</Text>
          </Group>
          <Button size="xs" variant="subtle" onClick={() => hide(false)}>Show the guide</Button>
        </Group>
      </Paper>
    )
  }

  return (
    <Paper component="section" aria-labelledby={headingId} p="md" withBorder radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start" gap="xs">
          <Box style={{ flex: 1, minWidth: 0 }}>
            <Title order={3} size="h4" id={headingId}>Getting started</Title>
            <Text size="sm" c="dimmed">
              Four steps from your data to predictions. Each shows where you are, and the button that moves you on.
            </Text>
          </Box>
          {allDone && <Button size="xs" variant="subtle" onClick={() => hide(true)}>Hide the guide</Button>}
        </Group>

        <Stack component="ol" gap="md" p={0} m={0}>
          <Step n={1} title="Connect the training worker" done={ready}
            action={state === 'off' ? { label: 'Set up the worker', icon: <IconPlugConnected size="1rem" />, onClick: onSetup }
              : state === 'down' ? { label: 'Fix the connection', icon: <IconTool size="1rem" />, onClick: onSetup }
              : ready && !deep ? { label: 'How to add deep learning', icon: <IconTool size="1rem" />, onClick: onSetup }
              : undefined}>
            {state === 'checking' && <Text size="sm" c="dimmed">Checking whether Hermod can reach the training worker…</Text>}
            {state === 'off' && (
              <Text size="sm" c="dimmed">
                Not set up yet. Training runs in hermod-ml, a helper program beside Hermod: run it with one command,
                then connect it here. No restart needed.
              </Text>
            )}
            {state === 'down' && (
              <Text size="sm" c="dimmed">The worker is set up, but it does not answer. Hermod cannot train until it does.</Text>
            )}
            {ready && (
              <>
                <Text size="sm" c="dimmed">Connected. Hermod can train models.</Text>
                <Text size="sm" c="dimmed">
                  {deep
                    ? 'Deep learning (PyTorch, TensorFlow) is available.'
                    : 'Deep learning (PyTorch, TensorFlow) is not available: the worker runs the slim image.'}
                </Text>
              </>
            )}
          </Step>

          <Step n={2} title="Add your data" done={hasData} blocked={connectFirst}
            action={{ label: 'Go to Datasets', icon: <IconDatabase size="1rem" />, onClick: onDatasets, disabled: !ready }}>
            <Text size="sm" c="dimmed">
              Upload a CSV or Excel file, or read rows from one of this vhost&apos;s databases. One row is one example, such as
              one customer.
            </Text>
          </Step>

          <Step n={3} title="Train a model" done={trained.length > 0} blocked={!ready ? connectFirst : 'Add a dataset first (step 2).'}
            action={{ label: 'Start training', icon: <IconSchool size="1rem" />, onClick: onTrain, disabled: !ready || !hasData }}>
            <Text size="sm" c="dimmed">
              Pick a dataset and the column to predict. Hermod tries several methods and keeps the best.
            </Text>
          </Step>

          <Step n={4} title="Use it" done={!!live} blocked="Train a model first (step 3)."
            action={live
              ? { label: 'Try your model', icon: <IconPlayerPlay size="1rem" />, onClick: () => onTry(live) }
              : trained.length > 0
                ? { label: 'Make it live', icon: <IconRocket size="1rem" />, onClick: () => onMakeLive(trained[0]) }
                : { label: 'Try your model', icon: <IconPlayerPlay size="1rem" />, onClick: () => {}, disabled: true }}>
            <Text size="sm" c="dimmed">
              Make a version live, try it with one row, then score records with a Predict node in a workflow, or call it
              from an application with its serving key.
            </Text>
          </Step>
        </Stack>
      </Stack>
    </Paper>
  )
}
