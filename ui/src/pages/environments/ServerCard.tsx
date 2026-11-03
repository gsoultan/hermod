import { useState } from 'react'
import { ActionIcon, Badge, Button, Card, Group, Modal, Stack, Text, Tooltip } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import { IconArrowRight, IconLinkOff, IconPlugConnected, IconPlus, IconRefresh } from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { formatDateTime } from '@/utils/dateUtils'
import { deleteLink, environmentKeys, roleColor, roleLabel, testLink, type Link, type Server } from '@/lib/environments'
import { ConnectionMatching } from './ConnectionMatching'

/**
 * One server this instance promotes to, with every vhost connected to it:
 * the server was trusted once, and each vhost under it is added from here.
 */
export function ServerCard({ server, onAddVHosts }: { server: Server; onAddVHosts: (via: Link) => void }) {
  const via = server.links.find((l) => !l.pending && l.vhost)
  const reachable = server.links.some((l) => !l.pending && l.reachable)
  const first = server.links.reduce((a, b) => (a.created_at <= b.created_at ? a : b))
  const titleId = `server-${first.id}`
  return (
    <Card component="section" withBorder radius="md" padding="lg" aria-labelledby={titleId}>
      <Stack gap="md">
        <Group justify="space-between" wrap="wrap" align="flex-start">
          <Stack gap={2}>
            <Group gap="xs">
              <Text fw={700} id={titleId}>{server.name}</Text>
              {server.role && <Badge color={roleColor[server.role]} variant="light">{roleLabel[server.role]}</Badge>}
              <Badge color={reachable ? 'green' : 'red'} variant="dot">{reachable ? 'Reachable' : 'Not reachable'}</Badge>
            </Group>
            <Text size="sm" c="dimmed">{server.url}</Text>
            <Text size="xs" c="dimmed">
              Connected by {first.created_by} on {formatDateTime(first.created_at)}
              {first.version ? ` · Hermod ${first.version}` : ''}
            </Text>
          </Stack>
          {via && (
            <Button
              size="xs"
              leftSection={<IconPlus size="0.9rem" />}
              aria-label={`Add vhosts to ${server.name}`}
              onClick={() => onAddVHosts(via)}
            >
              Add vhosts
            </Button>
          )}
        </Group>
        <Stack gap={0} style={{ border: '1px solid var(--mantine-color-default-border)', borderRadius: 'var(--mantine-radius-md)' }}>
          {server.links.map((l, i) => <VHostRow key={l.id} link={l} first={i === 0} />)}
        </Stack>
      </Stack>
    </Card>
  )
}

function VHostRow({ link, first }: { link: Link; first: boolean }) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const [matching, setMatching] = useState(false)
  const refresh = () => queryClient.invalidateQueries({ queryKey: environmentKeys.links })

  const test = useMutation({
    mutationFn: () => testLink(link.id),
    onSuccess: async (res) => {
      await refresh()
      notifications.show(res.error
        ? { color: link.pending ? 'yellow' : 'red', title: link.pending ? 'Still waiting' : `${link.name} did not answer`, message: res.error }
        : { color: 'green', message: link.pending ? `${link.vhost} is connected now.` : `${link.name} answered: Hermod ${res.info?.version ?? ''}` })
    },
  })
  const unlink = useMutation({
    mutationFn: () => deleteLink(link.id),
    onSuccess: async (res) => {
      await refresh()
      notifications.show({ color: res.note ? 'yellow' : 'green', message: res.note || `Removed ${link.vhost || 'the older link'}.` })
    },
  })
  const route = link.vhost ? `${link.vhost} → ${link.target_vhost || link.vhost}` : 'Every vhost (older link)'
  const status = link.pending
    ? <Badge color="yellow" variant="light">Waiting for approval</Badge>
    : link.reachable
      ? <Badge color="green" variant="light">Connected</Badge>
      : <Badge color="red" variant="light">Not reachable</Badge>

  return (
    <Group justify="space-between" wrap="wrap" p="sm" gap="xs" style={first ? undefined : { borderTop: '1px solid var(--mantine-color-default-border)' }}>
      <Stack gap={2} style={{ minWidth: 0 }}>
        <Group gap={6} wrap="nowrap">
          <IconArrowRight size="0.9rem" aria-hidden />
          <Text size="sm" fw={600}>{route}</Text>
          {status}
        </Group>
        {link.vhost_created && <Text size="xs" c="dimmed">Created there by this link</Text>}
        {link.check_note && (link.pending || !link.reachable) && (
          <Text size="xs" c={link.pending ? 'dimmed' : 'red'}>{link.check_note}</Text>
        )}
      </Stack>
      <Group gap="xs" wrap="nowrap">
        {!link.pending && (
          <Button size="xs" variant="light" leftSection={<IconPlugConnected size="0.9rem" />} onClick={() => setMatching(true)}>
            Match connections
          </Button>
        )}
        <Tooltip label={link.pending ? 'Ask again' : 'Test the connection'}>
          <ActionIcon
            variant="light"
            aria-label={link.pending ? `Ask ${link.name} again about ${link.vhost}` : `Test ${route}`}
            onClick={() => test.mutate()}
            loading={test.isPending}
          >
            <IconRefresh size="1rem" />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Remove">
          <ActionIcon
            variant="light"
            color="red"
            aria-label={`Remove ${route}`}
            loading={unlink.isPending}
            onClick={async () => {
              if (await confirm({
                title: `Remove ${link.vhost || 'the older link'}`,
                message: `Workflows in ${link.vhost || 'any vhost'} can no longer be promoted to ${link.name}. What was already promoted stays there, and the other vhosts of this server keep working.`,
                confirmLabel: 'Remove',
                danger: true,
              })) unlink.mutate()
            }}
          >
            <IconLinkOff size="1rem" />
          </ActionIcon>
        </Tooltip>
      </Group>
      <Modal opened={matching} onClose={() => setMatching(false)} title={`Connections in ${link.name}`} size="xl">
        <ConnectionMatching link={link} onDone={() => setMatching(false)} />
      </Modal>
    </Group>
  )
}
