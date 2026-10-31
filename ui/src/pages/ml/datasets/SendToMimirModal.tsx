import { useMemo, useState } from 'react'
import { Alert, Autocomplete, Badge, Button, Group, Modal, Paper, Select, Stack, Text, TextInput } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconSend } from '@tabler/icons-react'
import { useMutation } from '@tanstack/react-query'
import { sendDatasetToMimir, useVHostModels, type DatasetColumn, type MimirDataset } from '@/lib/mlModels'
import { SECRET_NAME_PATTERN, useVHostSecretNames } from '@/lib/vhostSecrets'

/** What the server's check of the data means for the next step. */
const VERDICT: Record<string, { color: string; text: string }> = {
  good: { color: 'teal', text: 'The server finds the data good to learn from. Train a model on it there, then connect that model here.' },
  warn: { color: 'yellow', text: 'The server can learn from it but has concerns; open the dataset on the server to read them before training.' },
  bad: { color: 'red', text: 'The server cannot learn from this data yet. Open the dataset on the server to see what it needs.' },
}

/**
 * Copies a dataset to a mimir server, where a model can be trained on it.
 * The rows go as CSV; the API key is a vhost secret the server reads, so it
 * never passes through the browser. The address and secret of a mimir model
 * already connected are offered, so a second trip to the server's page is
 * not needed. mimir has no append: each send makes a new dataset there.
 */
export function SendToMimirModal({ vhost, name, columns = [], onClose }: {
  vhost: string
  name: string
  columns?: DatasetColumn[]
  onClose: () => void
}) {
  const { data: models = [] } = useVHostModels(vhost)
  const secretNames = useVHostSecretNames(vhost)
  const servers = useMemo(() => {
    const seen = new Map<string, string>()
    for (const m of models) if (m.backend === 'mimir' && m.url && !seen.has(m.url)) seen.set(m.url, m.token_secret ?? '')
    return seen
  }, [models])
  // Until something is typed, the first connected server and its secret are
  // offered: the models arrive after the dialog opens, so they are derived.
  const [typedUrl, setTypedUrl] = useState<string | null>(null)
  const [typedSecret, setTypedSecret] = useState<string | null>(null)
  const url = typedUrl ?? (servers.keys().next().value as string | undefined) ?? ''
  const secret = typedSecret ?? servers.get(url) ?? ''
  const [remoteName, setRemoteName] = useState(name)
  const [target, setTarget] = useState('')
  const [touched, setTouched] = useState(false)

  const urlProblem = url.trim() ? '' : "Enter the mimir server's address."
  const secretProblem = !secret.trim()
    ? 'Name the secret that holds the API key, such as MIMIR_API_KEY.'
    : SECRET_NAME_PATTERN.test(secret.trim()) ? '' : 'A secret name is letters, digits and "_", not starting with a digit. Enter the name, not the key.'

  const send = useMutation({
    mutationFn: () => sendDatasetToMimir(vhost, name, {
      url: url.trim(),
      token_secret: secret.trim(),
      name: remoteName.trim() || undefined,
      target: target.trim() || undefined,
    }),
  })
  const sent: MimirDataset | undefined = send.data

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    setTouched(true)
    if (!urlProblem && !secretProblem) send.mutate()
  }
  const pickUrl = (next: string) => {
    setTypedUrl(next)
    const known = servers.get(next)
    if (known && !secret) setTypedSecret(known)
  }
  const verdict = sent?.verdict ? VERDICT[sent.verdict] : undefined

  return (
    <Modal opened onClose={onClose} title={`Send ${name} to mimir-server`} centered size="lg">
      {sent ? (
        <Stack gap="md">
          <Alert color="teal" icon={<IconCheck size="1rem" />} title="The dataset is on the server">
            <Text size="sm">
              {sent.rows.toLocaleString()} rows arrived as <Text span fw={600}>{sent.name}</Text>
              {sent.target ? <> with <Text span fw={600}>{sent.target}</Text> as the column to predict</> : null}.
            </Text>
          </Alert>
          {verdict && (
            <Paper withBorder radius="md" p="sm">
              <Group gap="sm" align="flex-start" wrap="nowrap">
                <Badge color={verdict.color} variant="light" style={{ flexShrink: 0 }}>{sent.verdict}</Badge>
                <Text size="sm">{verdict.text}</Text>
              </Group>
            </Paper>
          )}
          <Text size="xs" c="dimmed">
            The server keeps its own copy: send again after the dataset changes here, and a new dataset appears there.
            For data the server should refresh itself, make the dataset on the server from a database query instead.
          </Text>
          <Group justify="flex-end">
            <Button onClick={onClose}>Done</Button>
          </Group>
        </Stack>
      ) : (
        <form onSubmit={submit} noValidate>
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              Copies the rows to a mimir server so a model can be trained on them there, then connected here from the
              Models tab. The API key stays in the vhost secret you name.
            </Text>
            <Autocomplete
              label="mimir server"
              placeholder="http://mimir:8080"
              data={[...servers.keys()]}
              value={url}
              onChange={pickUrl}
              required
              error={touched ? urlProblem : undefined}
              description={servers.size ? 'A server a connected model already uses is offered.' : 'The address a model connected on the Models tab would use.'}
            />
            <Autocomplete
              label="API key secret"
              placeholder="MIMIR_API_KEY"
              data={secretNames}
              value={secret}
              onChange={setTypedSecret}
              required
              error={touched ? secretProblem : undefined}
              description="The name of the vhost secret that holds a mimir_… key. The key itself is never shown or sent by the browser."
            />
            <TextInput
              label="Name on the server"
              placeholder={name}
              value={remoteName}
              onChange={(e) => setRemoteName(e.currentTarget.value)}
              description="Empty keeps the name it has here."
            />
            <Select
              label="Column to predict"
              placeholder={columns.length ? 'The last column' : 'Type the column name'}
              data={columns.map((c) => c.name)}
              value={target || null}
              onChange={(v) => setTarget(v ?? '')}
              searchable
              clearable
              description="What a model trained on the server will learn to answer. Empty lets the server take the last column."
            />
            {send.error && (
              <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The dataset was not sent">
                {(send.error as Error).message}
              </Alert>
            )}
            <Group justify="flex-end">
              <Button variant="default" onClick={onClose}>Cancel</Button>
              <Button type="submit" leftSection={<IconSend size="1rem" />} loading={send.isPending}>Send</Button>
            </Group>
          </Stack>
        </form>
      )}
    </Modal>
  )
}
