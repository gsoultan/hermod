import type { ReactNode } from 'react'
import {
  ActionIcon, Alert, Button, Code, CopyButton, Group, List, Modal, Stack, Tabs, Text, Title, Tooltip,
} from '@mantine/core'
import { IconBrandDocker, IconCheck, IconCopy, IconInfoCircle, IconRefresh, IconSteeringWheel } from '@tabler/icons-react'
import { hasDeepLearning, useWorkerStatus, workerState } from '@/lib/mlModels'

// The commands of docs/ml.md ("Train a model" and "Image variants"); keep them in step.
const DOCKER_RUN = [
  'docker run -d --name hermod-ml -p 8090:8090 -e HERMOD_ML_TOKEN=change-me \\',
  '  -v hermod-ml:/var/lib/hermod-ml ghcr.io/gsoultan/hermod-ml:latest',
].join('\n')
const HERMOD_ENV = ['HERMOD_ML_WORKER_URL=http://hermod-ml:8090', 'HERMOD_ML_WORKER_TOKEN=change-me'].join('\n')
const HELM_UPGRADE = [
  'helm upgrade hermod oci://ghcr.io/gsoultan/charts/hermod --reuse-values \\',
  '  --set mlWorker.enabled=true --set mlWorker.token=change-me',
].join('\n')

/** A command to copy: shown as is, with a button that copies it. */
function CopyBlock({ label, value }: { label: string; value: string }) {
  return (
    <Group gap="xs" align="flex-start" wrap="nowrap">
      <Code block style={{ flex: 1, minWidth: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{value}</Code>
      <CopyButton value={value}>
        {({ copied, copy }) => (
          <Tooltip label={copied ? 'Copied' : 'Copy'}>
            <ActionIcon variant="light" color={copied ? 'teal' : 'gray'} radius="md" aria-label={`Copy ${label}`} onClick={copy}>
              {copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />}
            </ActionIcon>
          </Tooltip>
        )}
      </CopyButton>
    </Group>
  )
}

function Step({ n, children }: { n: number; children: ReactNode }) {
  return <Text size="sm" fw={600}>{n}. {children}</Text>
}

/** Where the worker stands now, with what to do about it. */
function WorkerNow() {
  const { data: worker } = useWorkerStatus()
  const state = workerState(worker)
  if (state === 'checking') return null
  if (state === 'ready') {
    return (
      <Alert color="green" variant="light" icon={<IconCheck size="1rem" />} title="The worker is connected">
        {hasDeepLearning(worker)
          ? 'Training works, with deep learning (PyTorch, TensorFlow) too.'
          : 'Training works. Deep learning (PyTorch, TensorFlow) needs the -dl image: see below.'}
      </Alert>
    )
  }
  if (state === 'down') {
    return (
      <Alert color="yellow" variant="light" icon={<IconInfoCircle size="1rem" />} title="Set up, but not answering">
        <Stack gap="xs">
          <Text size="sm">Hermod has a worker address but the worker does not answer. Check, in order:</Text>
          <List size="sm" spacing={4}>
            <List.Item>
              The worker is running: <Code>docker ps</Code> lists hermod-ml, or with Helm, <Code>kubectl get pods</Code> shows
              its pod Running.
            </List.Item>
            <List.Item>
              Hermod can reach the address in HERMOD_ML_WORKER_URL. Inside a container, localhost is that container: use the
              worker&apos;s name on a network both share.
            </List.Item>
            <List.Item>The worker&apos;s log says why it stopped: <Code>docker logs hermod-ml</Code>.</List.Item>
          </List>
          <Text size="sm">Then press Check again.</Text>
        </Stack>
      </Alert>
    )
  }
  return (
    <Alert color="gray" variant="light" icon={<IconInfoCircle size="1rem" />} title="Not set up yet">
      Hermod has no training worker. Models served elsewhere (KServe, Triton, MLServer, MLflow) work without one.
    </Alert>
  )
}

/**
 * How to run the hermod-ml worker and point Hermod at it, in plain words,
 * with the commands to copy and a way to check whether it worked.
 */
export function WorkerSetupModal({ onClose }: { onClose: () => void }) {
  const { refetch, isFetching } = useWorkerStatus()
  return (
    <Modal opened onClose={onClose} title="Set up the training worker" centered size="lg">
      <Stack gap="md">
        <WorkerNow />

        <Stack gap={4}>
          <Title order={4}>What is hermod-ml?</Title>
          <Text size="sm">
            A separate helper program that trains models. You give it your data, it learns from it, and hands the finished
            model to Hermod. Hermod itself serves the predictions, to workflows and applications. You run it once, beside
            Hermod; it is needed to train, not to use a model served elsewhere.
          </Text>
        </Stack>

        <Tabs defaultValue="docker">
          <Tabs.List>
            <Tabs.Tab value="docker" leftSection={<IconBrandDocker size="1rem" />}>Docker</Tabs.Tab>
            <Tabs.Tab value="helm" leftSection={<IconSteeringWheel size="1rem" />}>Helm (Kubernetes)</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="docker" pt="sm">
            <Stack gap="sm">
              <Step n={1}>Run the worker</Step>
              <CopyBlock label="the docker run command" value={DOCKER_RUN} />
              <Step n={2}>Tell Hermod where it is, then restart Hermod</Step>
              <CopyBlock label="Hermod's settings" value={HERMOD_ENV} />
              <Text size="xs" c="dimmed">
                Hermod reads these when it starts. HERMOD_ML_WORKER_TOKEN must equal the worker&apos;s HERMOD_ML_TOKEN; pick
                your own in place of change-me. If Hermod runs in Docker too, start both with the same <Code>--network</Code>
                so the name hermod-ml is found; if Hermod runs directly on this machine, use http://localhost:8090.
              </Text>
            </Stack>
          </Tabs.Panel>
          <Tabs.Panel value="helm" pt="sm">
            <Stack gap="sm">
              <Step n={1}>Turn the worker on in the chart</Step>
              <CopyBlock label="the helm upgrade command" value={HELM_UPGRADE} />
              <Text size="xs" c="dimmed">
                The chart runs the worker and gives Hermod its address and token; there is nothing else to set. It refuses
                to run the worker without a token; to keep it in a Secret of your own, set mlWorker.existingSecret instead.
              </Text>
            </Stack>
          </Tabs.Panel>
        </Tabs>

        <Stack gap={4}>
          <Title order={5}>Deep learning (TensorFlow, PyTorch)</Title>
          <Text size="sm">
            The neural networks, Keras MLP (TensorFlow) and PyTorch MLP, need the worker&apos;s deep-learning image. It is
            about 2 GB larger, runs on amd64 only, and wants about 1 GB more memory.
          </Text>
          <List size="sm" spacing={4}>
            <List.Item>Docker: run <Code>ghcr.io/gsoultan/hermod-ml:latest-dl</Code> in place of hermod-ml:latest.</List.Item>
            <List.Item>Helm: add <Code>--set mlWorker.image.variant=dl</Code>.</List.Item>
          </List>
        </Stack>

        <Group justify="space-between">
          <Text size="xs" c="dimmed">Started it? Check whether Hermod can reach it now.</Text>
          <Group gap="xs">
            <Button variant="default" onClick={onClose}>Close</Button>
            <Button leftSection={<IconRefresh size="1rem" />} loading={isFetching} onClick={() => refetch()}>Check again</Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
