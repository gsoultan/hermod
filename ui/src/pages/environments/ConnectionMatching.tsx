import { useMemo, useState } from 'react'
import { Alert, Anchor, Badge, Button, Group, Loader, Select, Stack, Table, Text } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { IconAlertCircle, IconArrowRight, IconExternalLink } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { environmentKeys, getMatching, putMapping, type Link, type MatchRow, type MatchStatus } from '@/lib/environments'

const statusBadge: Record<MatchStatus, { color: string; label: string }> = {
  matched: { color: 'green', label: 'Matched' },
  suggested: { color: 'yellow', label: 'Check this' },
  missing: { color: 'red', label: 'Missing' },
}

interface ConnectionMatchingProps {
  link: Link
  onDone?: () => void
}

/**
 * Which connection in the linked install each local source and sink becomes
 * when a workflow is promoted. Hermod matches by name and type; a person only
 * confirms the suggestions and picks the ones it could not match.
 */
export function ConnectionMatching({ link, onDone }: ConnectionMatchingProps) {
  const queryClient = useQueryClient()
  const { data, isLoading, error } = useQuery({
    queryKey: environmentKeys.matching(link.id, ''),
    queryFn: ({ signal }) => getMatching(link.id, '', signal),
  })
  const [edits, setEdits] = useState<Record<string, string>>({})

  const rows = useMemo(() => data ?? [], [data])
  const effective = useMemo(
    () => rows.map((r) => (r.id in edits ? { ...r, target_id: edits[r.id], status: edits[r.id] ? 'matched' : 'missing' } as MatchRow : r)),
    [rows, edits],
  )
  const matched = effective.filter((r) => r.status === 'matched').length

  const save = useMutation({
    mutationFn: () => putMapping(link.id, edits),
    onSuccess: async () => {
      setEdits({})
      await queryClient.invalidateQueries({ queryKey: ['environments', 'matching', link.id] })
      notifications.show({ color: 'green', message: 'Connection matches saved.' })
      onDone?.()
    },
  })

  if (isLoading) return <Group justify="center" p="lg"><Loader size="sm" aria-label="Loading connections" /></Group>
  if (error) {
    return (
      <Alert color="red" icon={<IconAlertCircle size="1rem" />} title={`Could not read ${link.name}'s connections`}>
        {(error as Error).message}
      </Alert>
    )
  }
  if (rows.length === 0) {
    return <Text size="sm" c="dimmed">This instance has no sources or sinks yet, so there is nothing to match.</Text>
  }

  const dirty = Object.keys(edits).length > 0
  return (
    <Stack gap="sm">
      <Text size="sm">
        <b>{matched} of {effective.length}</b> connections are matched. A workflow can only be promoted when all of
        the connections it uses are.
      </Text>
      <Table.ScrollContainer minWidth={560}>
        <Table verticalSpacing="xs" aria-label={`Connections matched in ${link.name}`}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Here</Table.Th>
              <Table.Th w={24} />
              <Table.Th>In {link.name}</Table.Th>
              <Table.Th w={110}>Status</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {effective.map((r) => {
              const options = (r.candidates ?? []).map((c) => ({ value: c.id, label: `${c.name} (${c.type}${c.vhost && c.vhost !== 'default' ? `, ${c.vhost}` : ''})` }))
              if (r.target_id && !options.some((o) => o.value === r.target_id)) {
                options.unshift({ value: r.target_id, label: r.target_name || r.target_id })
              }
              const badge = statusBadge[r.status]
              return (
                <Table.Tr key={`${r.kind}:${r.id}`}>
                  <Table.Td>
                    <Text size="sm" fw={500}>{r.name}</Text>
                    <Text size="xs" c="dimmed">{r.kind === 'source' ? 'Source' : 'Sink'} · {r.type}</Text>
                  </Table.Td>
                  <Table.Td><IconArrowRight size="0.9rem" aria-hidden /></Table.Td>
                  <Table.Td>
                    {options.length > 0 ? (
                      <Select
                        aria-label={`Match for ${r.name}`}
                        size="xs"
                        data={options}
                        value={r.target_id || null}
                        placeholder="Pick a connection"
                        clearable
                        onChange={(v) => setEdits((e) => ({ ...e, [r.id]: v ?? '' }))}
                      />
                    ) : (
                      <Anchor href={`${link.url}/${r.kind === 'source' ? 'sources' : 'sinks'}/new`} target="_blank" rel="noopener noreferrer" size="sm">
                        <Group gap={4} wrap="nowrap">
                          Create it in {link.name} <IconExternalLink size="0.8rem" aria-hidden />
                        </Group>
                      </Anchor>
                    )}
                  </Table.Td>
                  <Table.Td><Badge color={badge.color} variant="light">{badge.label}</Badge></Table.Td>
                </Table.Tr>
              )
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      <Group justify="flex-end">
        {onDone && !dirty && <Button variant="default" onClick={onDone}>Done</Button>}
        {dirty && (
          <>
            <Button variant="default" onClick={() => setEdits({})}>Undo changes</Button>
            <Button onClick={() => save.mutate()} loading={save.isPending}>Save matches</Button>
          </>
        )}
      </Group>
      {save.error && <Alert color="red">{(save.error as Error).message}</Alert>}
    </Stack>
  )
}
