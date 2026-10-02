import { useMemo, useState } from 'react'
import {
  ActionIcon, Alert, Box, Button, Code, CopyButton, Group, Loader, Paper, Select, Stack, Table, Text, Title,
  Tooltip,
} from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCopy, IconKey, IconPlus, IconRefresh, IconTrash } from '@tabler/icons-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVHost } from '@/context/VHostContext'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { EmptyState } from '@/components/common/EmptyState'
import {
  deleteVHostSecret, isSecretVHost, listVHostSecrets, vhostSecretsKey, type VHostSecretEntry,
} from '@/lib/vhostSecrets'
import { SecretFormModal } from './SecretFormModal'

/** A timestamp as the table shows it; storage's zero time means "not recorded". */
function when(iso: string | undefined): string {
  if (!iso || iso.startsWith('0001-')) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

/**
 * The secrets a vhost holds for its own workflows.
 *
 * Names only: the API never returns a value, so there is nothing here to
 * reveal. A secret is used by writing secret("NAME") in a workflow of the same
 * vhost; a name the vhost does not hold falls back to the global secret manager.
 */
export function SecretsPage() {
  const confirm = useConfirm()
  const queryClient = useQueryClient()
  const { selectedVHost, availableVHosts } = useVHost()

  // "All vhosts" is a filter, not a place a secret can live, so the page asks.
  const [chosen, setChosen] = useState<string | null>(null)
  const vhost = isSecretVHost(selectedVHost) ? selectedVHost : chosen
  const vhostOptions = useMemo(
    () => Array.from(new Set(['default', ...availableVHosts.filter((v) => isSecretVHost(v))])),
    [availableVHosts],
  )

  // undefined: closed. { name } rotates that secret; {} adds one.
  const [editing, setEditing] = useState<{ name?: string } | undefined>(undefined)

  const { data: secrets, isLoading, error } = useQuery({
    queryKey: vhostSecretsKey(vhost ?? ''),
    queryFn: ({ signal }) => listVHostSecrets(vhost as string, signal),
    enabled: !!vhost,
    retry: false,
  })

  const deleteMutation = useMutation({
    mutationFn: (name: string) => deleteVHostSecret(vhost as string, name),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: vhostSecretsKey(vhost as string) }),
  })

  const askDelete = async (name: string) => {
    const ok = await confirm({
      title: 'Delete secret',
      message: `Delete ${name} from ${vhost}?`,
      consequence: `Workflows in ${vhost} that read it will get the global secret of that name, or nothing.`,
      confirmLabel: 'Delete secret',
      danger: true,
    })
    if (ok) deleteMutation.mutate(name)
  }

  const rows = (secrets ?? []).map((s: VHostSecretEntry) => {
    const usage = `secret("${s.name}")`
    return (
      <Table.Tr key={s.name}>
        <Table.Td fw={500} ff="var(--mantine-font-family-monospace)">{s.name}</Table.Td>
        <Table.Td>
          <Group gap={4} wrap="nowrap">
            <Code>{usage}</Code>
            <CopyButton value={usage}>
              {({ copied, copy }) => (
                <Tooltip label={copied ? 'Copied' : 'Copy'}>
                  <ActionIcon variant="subtle" color={copied ? 'teal' : 'gray'} size="sm" onClick={copy} aria-label={`Copy ${usage}`}>
                    {copied ? <IconCheck size="0.9rem" /> : <IconCopy size="0.9rem" />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Group>
        </Table.Td>
        <Table.Td>{when(s.updated_at)}</Table.Td>
        <Table.Td>{s.updated_by || '—'}</Table.Td>
        <Table.Td>
          <Group justify="flex-end" gap="xs" wrap="nowrap">
            <Tooltip label="Replace the value">
              <ActionIcon variant="light" color="blue" radius="md" aria-label={`Rotate ${s.name}`} onClick={() => setEditing({ name: s.name })}>
                <IconRefresh size="1.1rem" stroke={1.5} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Delete">
              <ActionIcon variant="light" color="red" radius="md" aria-label={`Delete ${s.name}`} onClick={() => askDelete(s.name)}>
                <IconTrash size="1.1rem" />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Table.Td>
      </Table.Tr>
    )
  })

  return (
    <Box p="md" className="page-enter">
      <Stack gap="lg">
        <Paper p="md" withBorder radius="md" bg="var(--mantine-color-body)">
          <Stack gap="md">
            <Group gap="sm" align="flex-start">
              <IconKey size="2rem" color="var(--mantine-color-blue-filled)" />
              <Box style={{ flex: 1 }}>
                <Title order={2} fw={800}>Secrets</Title>
                <Text size="sm" c="dimmed">
                  API keys, passwords and tokens a vhost keeps for its own workflows. A value is encrypted,
                  and is never shown again once saved. Use one as <Code>{'secret("NAME")'}</Code> in an
                  expression, or <Code>secret:NAME</Code> in a source or sink field.
                </Text>
              </Box>
              {vhost && (
                <Button leftSection={<IconPlus size="1.2rem" />} onClick={() => setEditing({})}>
                  Add secret
                </Button>
              )}
            </Group>
            {!isSecretVHost(selectedVHost) && (
              <Select
                label="VHost"
                description="A secret belongs to one vhost."
                placeholder="Select one"
                data={vhostOptions}
                value={chosen}
                onChange={setChosen}
                allowDeselect={false}
                maw={360}
              />
            )}
          </Stack>
        </Paper>

        {deleteMutation.error && (
          <Alert color="red" icon={<IconAlertCircle size="1rem" />} title="The secret was not deleted">
            {(deleteMutation.error as Error).message}
          </Alert>
        )}

        <Paper radius="md" withBorder style={{ overflow: 'hidden' }}>
          {!vhost ? (
            <EmptyState compact icon={<IconKey size="1.3rem" />} title="Choose a vhost"
              description="Secrets are kept per vhost. Pick one above to see and manage its secrets." />
          ) : isLoading ? (
            <Group justify="center" p="xl"><Loader size="sm" /><Text size="sm" c="dimmed">Loading secrets…</Text></Group>
          ) : error ? (
            <Alert m="md" color="red" icon={<IconAlertCircle size="1rem" />} title={`The secrets of ${vhost} could not be loaded`}>
              {(error as Error).message}
            </Alert>
          ) : rows.length === 0 ? (
            <EmptyState compact icon={<IconKey size="1.3rem" />} title={`No secrets in ${vhost} yet`}
              description={<>Add one, then read it in a workflow of this vhost with <Code>{'secret("NAME")'}</Code>. A name this vhost does not hold is read from the global secret manager.</>}
              action={{ label: 'Add secret', onClick: () => setEditing({}) }} />
          ) : (
            <Table.ScrollContainer minWidth={700}>
              <Table verticalSpacing="sm" horizontalSpacing="lg">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>Name</Table.Th>
                    <Table.Th>Use in a workflow</Table.Th>
                    <Table.Th>Last updated</Table.Th>
                    <Table.Th>By</Table.Th>
                    <Table.Th style={{ textAlign: 'right' }}>Actions</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>{rows}</Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
        </Paper>
      </Stack>

      {vhost && editing && (
        <SecretFormModal vhost={vhost} name={editing.name} onClose={() => setEditing(undefined)} />
      )}
    </Box>
  )
}
