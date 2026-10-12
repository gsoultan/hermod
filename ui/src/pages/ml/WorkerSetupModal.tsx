import { useId, useState, type ReactNode } from 'react'
import {
  ActionIcon, Alert, Button, Code, CopyButton, Group, List, Modal, PasswordInput, Stack, Switch, Tabs, Text, TextInput,
  Title, Tooltip,
} from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  IconBrandDocker, IconCheck, IconCopy, IconInfoCircle, IconKey, IconPlugConnected, IconRefresh, IconSteeringWheel,
  IconStack2,
} from '@tabler/icons-react'
import { apiFetch } from '@/api'
import { useSessionStore } from '@/auth/session'
import {
  generateWorkerToken, getWorkerConnection, hasDeepLearning, saveWorkerConnection, testWorkerConnection, useWorkerStatus,
  workerConnectionKey, workerImageTag, workerState, workerStatusKey, type ProbeReport,
} from '@/lib/mlModels'

/** Shown in a command until a token is generated or typed. */
const TOKEN_PLACEHOLDER = 'change-me'

// The commands of docs/ml.md and deploy/compose/compose.yaml; keep them in step.
const dockerRun = (token: string, tag: string) => [
  `docker run -d --name hermod-ml --restart unless-stopped -p 8090:8090 -e HERMOD_ML_TOKEN=${token} \\`,
  `  -v hermod-ml:/var/lib/hermod-ml ghcr.io/gsoultan/hermod-ml:${tag}`,
].join('\n')
const composeService = (tag: string) => [
  '  hermod-ml:',
  `    image: ghcr.io/gsoultan/hermod-ml:${tag}`,
  '    restart: unless-stopped',
  '    environment:',
  '      HERMOD_ML_TOKEN: ${HERMOD_ML_TOKEN}',
  '    volumes:',
  '      - hermod-ml:/var/lib/hermod-ml',
].join('\n')
const helmUpgrade = (token: string, deep: boolean) => [
  'helm upgrade hermod oci://ghcr.io/gsoultan/charts/hermod --reuse-values \\',
  `  --set mlWorker.enabled=true --set mlWorker.token=${token}${deep ? ' \\' : ''}`,
  ...(deep ? ['  --set mlWorker.image.variant=dl'] : []),
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

/** A numbered step: its heading, then what to do. */
function Step({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  const id = useId()
  return (
    <Stack component="section" gap="xs" aria-labelledby={id}>
      <Title order={4} size="h5" id={id}>{n}. {title}</Title>
      {children}
    </Stack>
  )
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
          : 'Training works. Deep learning (PyTorch, TensorFlow) needs the deep-learning image: see step 1.'}
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
              Hermod can reach its address. Inside a container, localhost is that container: use the worker&apos;s name on a
              network both share.
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

/** What a test found, in plain words, with what to do about it. */
function ProbeAnswer({ report }: { report: ProbeReport }) {
  const detail = <Text size="xs" c="dimmed">{report.message}</Text>
  switch (report.result) {
    case 'ok':
      return (
        <Alert color="green" variant="light" icon={<IconCheck size="1rem" />} title="Connected">
          <Stack gap={4}>
            <Text size="sm">Hermod reached the worker and it accepted the token.</Text>
            <Text size="sm">
              {report.deep_learning
                ? `Deep learning (${(report.frameworks ?? []).join(', ')}) is available.`
                : 'Deep learning (PyTorch, TensorFlow) is not available: the worker runs the slim image.'}
            </Text>
          </Stack>
        </Alert>
      )
    case 'unauthorized':
      return (
        <Alert color="red" variant="light" title="The worker refused the token">
          <Text size="sm">
            The worker is there, but the token does not match. Paste the exact token the worker was started with (its
            HERMOD_ML_TOKEN), or start the worker again with the token above.
          </Text>
        </Alert>
      )
    case 'unreachable':
      return (
        <Alert color="red" variant="light" title="Nothing answered at this address">
          <Stack gap={4}>
            <Text size="sm">
              Check the worker is running (<Code>docker ps</Code>) and the port is right (8090 unless you changed it). If
              Hermod runs in Docker, localhost is Hermod&apos;s own container: use the worker&apos;s container name on a
              network both share, such as http://hermod-ml:8090.
            </Text>
            {detail}
          </Stack>
        </Alert>
      )
    case 'not_a_worker':
      return (
        <Alert color="red" variant="light" title="This is not hermod-ml">
          <Stack gap={4}>
            <Text size="sm">Something answered at this address, but it is another program. Check the host and the port.</Text>
            {detail}
          </Stack>
        </Alert>
      )
    case 'bad_address':
      return (
        <Alert color="red" variant="light" title="Not an address Hermod can call">
          <Stack gap={4}>
            <Text size="sm">An address looks like http://hermod-ml:8090: http:// or https://, a host, and the port.</Text>
            {detail}
          </Stack>
        </Alert>
      )
    default:
      return (
        <Alert color="red" variant="light" title="The worker answered with an error">
          {detail}
        </Alert>
      )
  }
}

/** Step 1: how to run the worker, with a token to give it. */
function RunWorker({ token, onGenerate }: { token: string; onGenerate: () => void }) {
  const [deep, setDeep] = useState(false)
  const { data: versionData } = useQuery({
    queryKey: ['version'],
    queryFn: async () => (await (await apiFetch('/api/version', { silent: true })).json()) as { version?: string },
    staleTime: Infinity,
  })
  const tag = workerImageTag(versionData?.version, deep)
  const shown = token || TOKEN_PLACEHOLDER
  return (
    <Step n={1} title="Run the worker">
      <Text size="sm">
        hermod-ml runs beside Hermod, in a container. It needs a token, a password only Hermod knows: generate one, then
        run the command where Docker runs.
      </Text>
      <Group gap="md">
        <Button size="xs" variant="light" leftSection={<IconKey size="1rem" />} onClick={onGenerate}>Generate token</Button>
        <Switch checked={deep} onChange={(e) => setDeep(e.currentTarget.checked)}
          label="Deep learning (TensorFlow, PyTorch)"
          description="A larger image (about 2 GB more), amd64 only, wants about 1 GB more memory." />
      </Group>
      <Tabs defaultValue="docker">
        <Tabs.List>
          <Tabs.Tab value="docker" leftSection={<IconBrandDocker size="1rem" />}>Docker</Tabs.Tab>
          <Tabs.Tab value="compose" leftSection={<IconStack2 size="1rem" />}>Docker Compose</Tabs.Tab>
          <Tabs.Tab value="helm" leftSection={<IconSteeringWheel size="1rem" />}>Helm (Kubernetes)</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="docker" pt="sm">
          <Stack gap="xs">
            <CopyBlock label="the docker run command" value={dockerRun(shown, tag)} />
            <Text size="xs" c="dimmed">
              It restarts with Docker and keeps its datasets and models in the hermod-ml volume. From a checkout of Hermod,
              <Code>scripts/install-hermod-ml.sh</Code> does the same, waits for the worker, and prints what to paste below.
            </Text>
          </Stack>
        </Tabs.Panel>
        <Tabs.Panel value="compose" pt="sm">
          <Stack gap="xs">
            <Text size="sm">Add the worker to the services of your compose file, and the token to its .env:</Text>
            <CopyBlock label="the compose service" value={composeService(tag)} />
            <CopyBlock label="the .env line" value={`HERMOD_ML_TOKEN=${shown}`} />
            <Text size="xs" c="dimmed">
              Hermod reaches it as http://hermod-ml:8090. deploy/compose/compose.yaml runs Hermod and the worker together and
              connects them for you.
            </Text>
          </Stack>
        </Tabs.Panel>
        <Tabs.Panel value="helm" pt="sm">
          <Stack gap="xs">
            <CopyBlock label="the helm upgrade command" value={helmUpgrade(shown, deep)} />
            <Text size="xs" c="dimmed">
              The chart runs the worker and gives Hermod its address and token itself, so steps 2 and 3 are not needed. To
              keep the token in a Secret of your own, set mlWorker.existingSecret instead.
            </Text>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Step>
  )
}

/** Steps 2 and 3, for an administrator: the address and token, then test and save. */
function ConnectForm({ token, setToken, savedURL, tokenSaved }: {
  token: string
  setToken: (t: string) => void
  savedURL: string
  tokenSaved: boolean
}) {
  const queryClient = useQueryClient()
  const [url, setURL] = useState(savedURL)
  // The values the last test was for: changing either asks for a new test.
  const [tested, setTested] = useState<{ url: string; token: string; report: ProbeReport }>()
  const [saved, setSaved] = useState(false)
  // No token typed keeps the saved one.
  const tokenToSend = token === '' && tokenSaved ? undefined : token

  const test = useMutation({
    mutationFn: () => testWorkerConnection(url.trim(), tokenToSend),
    onSuccess: (report) => {
      setTested({ url, token, report })
      setSaved(false)
    },
  })
  const save = useMutation({
    mutationFn: () => saveWorkerConnection(url.trim(), tokenToSend),
    onSuccess: async () => {
      setSaved(true)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: workerStatusKey }),
        queryClient.invalidateQueries({ queryKey: workerConnectionKey }),
      ])
    },
  })
  const current = tested && tested.url === url && tested.token === token ? tested.report : undefined
  const failed = (e: unknown) => (e instanceof Error ? e.message : 'The request failed.')

  return (
    <>
      <Step n={2} title="Paste the address and token">
        <TextInput label="Address" placeholder="http://hermod-ml:8090" value={url}
          onChange={(e) => setURL(e.currentTarget.value)}
          description={'Where Hermod reaches the worker. If Hermod runs in Docker, "localhost" means the Hermod container ' +
            'itself, not this machine: use the worker\'s container name on a shared network, such as http://hermod-ml:8090.'} />
        <PasswordInput label="Token" value={token} onChange={(e) => setToken(e.currentTarget.value)}
          placeholder={tokenSaved ? 'Saved; leave empty to keep it' : 'The worker\'s HERMOD_ML_TOKEN'}
          description="The same token the worker was started with." />
      </Step>

      <Step n={3} title="Test and save">
        <Text size="sm" c="dimmed">Test first: Hermod calls the worker with this address and token and says what it found.</Text>
        <Group gap="xs">
          <Button variant="light" leftSection={<IconPlugConnected size="1rem" />} loading={test.isPending}
            disabled={url.trim() === ''} onClick={() => test.mutate()}>
            Test connection
          </Button>
          <Button loading={save.isPending} disabled={!current} color={current && current.result !== 'ok' ? 'orange' : undefined}
            onClick={() => save.mutate()}>
            {current && current.result !== 'ok' ? 'Save anyway' : 'Save'}
          </Button>
        </Group>
        {current && <ProbeAnswer report={current} />}
        {test.isError && <Alert color="red" variant="light" title="Could not test">{failed(test.error)}</Alert>}
        {save.isError && <Alert color="red" variant="light" title="Not saved">{failed(save.error)}</Alert>}
        {saved && !save.isPending && (
          <Alert color="green" variant="light" icon={<IconCheck size="1rem" />}>
            Saved. Hermod uses this worker now, for training, datasets and the Collect Dataset sink; no restart needed.
          </Alert>
        )}
      </Step>
    </>
  )
}

/** What an administrator sees: the three steps, or a worker the environment sets. */
function AdminSetup() {
  const { data: conn, isLoading, isError } = useQuery({
    queryKey: workerConnectionKey,
    queryFn: ({ signal }) => getWorkerConnection(signal),
    retry: false,
  })
  const [token, setToken] = useState('')

  if (isLoading) return <Text size="sm" c="dimmed">Reading the saved connection…</Text>
  if (conn && !conn.editable) {
    return (
      <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />} title="Set by the server's environment">
        <Stack gap={4}>
          <Text size="sm">
            Hermod reaches the worker at <Code>{conn.url}</Code>
            {conn.token_set ? ', with a token' : ', without a token'}, as HERMOD_ML_WORKER_URL and HERMOD_ML_WORKER_TOKEN set
            it where Hermod is deployed. Those win over the console: change them there, and restart Hermod.
          </Text>
          <Text size="sm">
            For deep learning, run the worker&apos;s -dl image (with Helm: --set mlWorker.image.variant=dl).
          </Text>
        </Stack>
      </Alert>
    )
  }
  return (
    <Stack gap="lg">
      {isError && <Alert color="yellow" variant="light">The saved connection could not be read; you can still save a new one.</Alert>}
      <RunWorker token={token} onGenerate={() => setToken(generateWorkerToken())} />
      <ConnectForm token={token} setToken={setToken} savedURL={conn?.url ?? ''} tokenSaved={!!conn?.token_set} />
    </Stack>
  )
}

/**
 * How to run the hermod-ml worker and connect Hermod to it, in three steps:
 * run it (one command, with a generated token), paste its address and token,
 * test and save. Saving takes effect at once. Only an administrator connects
 * it; anyone else is told whom to ask.
 */
export function WorkerSetupModal({ onClose }: { onClose: () => void }) {
  const { refetch, isFetching } = useWorkerStatus()
  const isAdmin = useSessionStore((s) => s.user?.role) === 'Administrator'
  return (
    <Modal opened onClose={onClose} title="Set up the training worker" centered size="lg">
      <Stack gap="md">
        <WorkerNow />

        <Stack gap={4}>
          <Title order={4} size="h5">What is hermod-ml?</Title>
          <Text size="sm">
            A separate helper program that trains models. You give it your data, it learns from it, and hands the finished
            model to Hermod. Hermod itself serves the predictions, to workflows and applications. You run it once, beside
            Hermod; it is needed to train, not to use a model served elsewhere.
          </Text>
        </Stack>

        {isAdmin ? <AdminSetup /> : (
          <Alert color="blue" variant="light" icon={<IconInfoCircle size="1rem" />} title="Ask an administrator">
            Connecting the training worker needs the Administrator role. An administrator of this Hermod opens Machine
            Learning, then Set up the worker; it takes a few minutes and no restart.
          </Alert>
        )}

        <Group justify="space-between">
          <Text size="xs" c="dimmed">Check whether Hermod can reach the worker now.</Text>
          <Group gap="xs">
            <Button variant="default" onClick={onClose}>Close</Button>
            <Button variant="default" leftSection={<IconRefresh size="1rem" />} loading={isFetching} onClick={() => refetch()}>
              Check again
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
