import { useState } from 'react'
import {
  ActionIcon, Alert, Badge, Button, Group, Loader, Modal, Paper, Stack, Table, Text, TextInput, Textarea, Title, Tooltip,
} from '@mantine/core'
import { IconAlertCircle, IconCode, IconPlus, IconShieldLock, IconTrash } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSessionRole } from '@/auth/session'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import {
  MAX_SCRIPT_BYTES, MODEL_NAME_PATTERN, SCRIPT_TEMPLATE, deleteScript, getScript, listScripts, modelNameRule, saveScript,
  scriptKey, scriptsKey, type MLScript, type WorkerCapabilities,
} from '@/lib/mlModels'

const shortHash = (sha: string) => sha.slice(0, 12)

/**
 * The vhost's custom training scripts: Python that trains a model and exports
 * it to ONNX, run in a sandbox on the custom-script worker pool. A training
 * picks one as the algorithm custom:<name>. An Administrator saves and deletes
 * them; an Editor reads them; saving new source adds a version.
 */
export function ScriptsPanel({ vhost, capabilities }: { vhost: string; capabilities?: WorkerCapabilities }) {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const role = getSessionRole()
  const isAdmin = role === 'Administrator'
  const canRead = isAdmin || role === 'Editor'
  const [open, setOpen] = useState<{ name?: string } | undefined>(undefined)

  const { data: scripts = [], isLoading, error } = useQuery({
    queryKey: scriptsKey(vhost),
    queryFn: ({ signal }) => listScripts(vhost, signal),
    retry: false,
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteScript(vhost, name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey(vhost) }),
  })
  const askDelete = async (name: string) => {
    const ok = await confirm({
      title: 'Delete script',
      message: `Delete ${name} and all its versions from ${vhost}?`,
      consequence: 'Trainings that name it start failing. Model versions it already trained keep working and keep its name and SHA-256.',
      confirmLabel: 'Delete script',
      danger: true,
    })
    if (ok) remove.mutate(name)
  }

  return (
    <Paper p="md" withBorder radius="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={3} size="h4">Training scripts</Title>
            <Text size="sm" c="dimmed">
              Python that defines <code>train(df, spec)</code> and <code>export_onnx(model, spec)</code>. Train with one by
              picking it as the algorithm. Scripts run in a sandbox with no network, on their own worker pool.
            </Text>
          </div>
          {isAdmin && (
            <Button variant="light" leftSection={<IconPlus size="1rem" />} onClick={() => setOpen({})}>New script</Button>
          )}
        </Group>
        {capabilities && !capabilities.custom_scripts && (
          <Alert color="yellow" variant="light" icon={<IconShieldLock size="1rem" />} title="Scripts cannot train yet">
            Custom scripts are on, but no custom-script worker pool is configured, so trainings that use one are refused.
            Set HERMOD_ML_CUSTOM_WORKER_URL and HERMOD_ML_CUSTOM_WORKER_TOKEN (the Helm chart's mlWorker.customPool does).
          </Alert>
        )}
        {remove.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The script was not deleted">{(remove.error as Error).message}</Alert>
        )}
        {isLoading ? (
          <Group justify="center" p="md"><Loader size="sm" /></Group>
        ) : error ? (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The scripts could not be loaded">{(error as Error).message}</Alert>
        ) : scripts.length === 0 ? (
          <EmptyState compact icon={<IconCode size="1.3rem" />} title={`No training scripts in ${vhost}`}
            description={isAdmin ? 'Add one to train a model your own way.' : 'An Administrator adds them.'} />
        ) : (
          <Table.ScrollContainer minWidth={640}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Version</Table.Th>
                  <Table.Th>SHA-256</Table.Th>
                  <Table.Th>Saved</Table.Th>
                  <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {scripts.map((s) => (
                  <Table.Tr key={s.name}>
                    <Table.Td>
                      <Text fw={500} ff="var(--mantine-font-family-monospace)">{s.name}</Text>
                      {s.description && <Text size="xs" c="dimmed">{s.description}</Text>}
                    </Table.Td>
                    <Table.Td><Badge variant="light" tt="none">v{s.version}</Badge></Table.Td>
                    <Table.Td><Text size="sm" ff="var(--mantine-font-family-monospace)" title={s.sha256}>{shortHash(s.sha256)}</Text></Table.Td>
                    <Table.Td><Text size="sm" c="dimmed">{new Date(s.created_at).toLocaleString()}{s.created_by ? ` by ${s.created_by}` : ''}</Text></Table.Td>
                    <Table.Td>
                      <Group justify="flex-end" gap="xs" wrap="nowrap">
                        {canRead && (
                          <Tooltip label={isAdmin ? 'Edit' : 'View'}>
                            <ActionIcon variant="light" radius="md" aria-label={`Open script ${s.name}`} onClick={() => setOpen({ name: s.name })}>
                              <IconCode size="1.1rem" />
                            </ActionIcon>
                          </Tooltip>
                        )}
                        {isAdmin && (
                          <Tooltip label="Delete">
                            <ActionIcon variant="light" color="red" radius="md" aria-label={`Delete script ${s.name}`} onClick={() => askDelete(s.name)}>
                              <IconTrash size="1.1rem" />
                            </ActionIcon>
                          </Tooltip>
                        )}
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
      {open && <ScriptModal vhost={vhost} name={open.name} readOnly={!isAdmin} onClose={() => setOpen(undefined)} />}
    </Paper>
  )
}

/**
 * One script: a new one, or an existing one's latest source with its
 * versions. The editor is a monospace textarea, as for Lua scripts.
 */
function ScriptModal({ vhost, name, readOnly, onClose }: { vhost: string; name?: string; readOnly: boolean; onClose: () => void }) {
  const { data, isLoading, error } = useQuery({
    queryKey: scriptKey(vhost, name ?? ''),
    queryFn: ({ signal }) => getScript(vhost, name as string, signal),
    enabled: !!name,
    retry: false,
  })

  return (
    <Modal opened onClose={onClose} title={name ? `Script ${name}` : 'New training script'} centered size="xl">
      {name && isLoading ? (
        <Group justify="center" p="md"><Loader size="sm" /></Group>
      ) : name && (error || !data) ? (
        <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The script could not be loaded">
          {error ? (error as Error).message : 'It was not found.'}
        </Alert>
      ) : (
        <ScriptForm vhost={vhost} name={name} readOnly={readOnly} onClose={onClose}
          initial={data?.script} versions={data?.versions ?? []} />
      )}
    </Modal>
  )
}

/** The script's editor, filled from the version loaded before it mounts. */
function ScriptForm({ vhost, name, readOnly, onClose, initial, versions }: {
  vhost: string; name?: string; readOnly: boolean; onClose: () => void; initial?: MLScript; versions: MLScript[]
}) {
  const queryClient = useQueryClient()
  const [scriptName, setScriptName] = useState(name ?? '')
  const [nameError, setNameError] = useState<string | null>(null)
  const [source, setSource] = useState(initial ? initial.source ?? '' : SCRIPT_TEMPLATE)
  const [description, setDescription] = useState(initial?.description ?? '')

  const save = useMutation({
    mutationFn: () => saveScript(vhost, scriptName.trim(), source, description),
    onSuccess: (saved: MLScript) => {
      queryClient.invalidateQueries({ queryKey: scriptsKey(vhost) })
      queryClient.invalidateQueries({ queryKey: scriptKey(vhost, saved.name ?? scriptName.trim()) })
      onClose()
    },
  })

  const bytes = new TextEncoder().encode(source).length
  const tooBig = bytes > MAX_SCRIPT_BYTES
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const badName = !MODEL_NAME_PATTERN.test(scriptName.trim())
    setNameError(badName ? `"${scriptName.trim()}" is not a valid name.` : null)
    if (badName || tooBig || !source.trim()) return
    save.mutate()
  }

  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        {!name && (
          <TextInput label="Script name" description={`${modelNameRule} Trainings pick it as custom:<name>.`} required
            value={scriptName} onChange={(e) => { setScriptName(e.currentTarget.value); setNameError(null) }} error={nameError} data-autofocus />
        )}
        <TextInput label="Description" value={description} readOnly={readOnly} maxLength={1024}
          onChange={(e) => setDescription(e.currentTarget.value)} />
        <Textarea label="Source" value={source} readOnly={readOnly} autosize minRows={16} maxRows={32} spellCheck={false}
          description={readOnly ? 'Only an Administrator can change a script.' : `${Math.ceil(bytes / 1024)} of ${MAX_SCRIPT_BYTES / 1024} KB`}
          error={tooBig ? 'The script is larger than 256 KB.' : undefined}
          onChange={(e) => setSource(e.currentTarget.value)}
          styles={{ input: { fontFamily: 'var(--mantine-font-family-monospace)', fontSize: 'var(--mantine-font-size-sm)' } }} />
        {versions.length > 0 && (
          <Stack gap={4}>
            <Text size="sm" fw={500}>Versions</Text>
            <Table withRowBorders={false} verticalSpacing={2} fz="sm">
              <Table.Tbody>
                {versions.map((v) => (
                  <Table.Tr key={v.version}>
                    <Table.Td w={110}>Version {v.version}</Table.Td>
                    <Table.Td ff="var(--mantine-font-family-monospace)" title={v.sha256}>{shortHash(v.sha256)}</Table.Td>
                    <Table.Td c="dimmed">{new Date(v.created_at).toLocaleString()}{v.created_by ? ` by ${v.created_by}` : ''}</Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Stack>
        )}
        {save.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The script was not saved">{(save.error as Error).message}</Alert>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>{readOnly ? 'Close' : 'Cancel'}</Button>
          {!readOnly && (
            <Button type="submit" loading={save.isPending} disabled={tooBig || !source.trim()}>Save</Button>
          )}
        </Group>
      </Stack>
    </form>
  )
}
