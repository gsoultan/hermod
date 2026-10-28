import { useState } from 'react'
import {
  ActionIcon, Alert, Badge, Button, Card, CopyButton, Group, Loader, Modal, NumberInput, Paper, SegmentedControl, Select,
  Stack, Switch, Text, TextInput, Title, Tooltip,
} from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import {
  IconArrowRight, IconCheck, IconCopy, IconInfoCircle, IconKey, IconLinkOff, IconPlugConnected, IconPlus, IconRefresh,
  IconTrash,
} from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { formatDateTime } from '@/utils/dateUtils'
import {
  approvalLabel, deleteLink, environmentKeys, newPairingCode, putInstance, putRules, revokePeer, roleColor, roleLabel,
  testLink, useInstance, useLinks, usePeers, useRules, type ApprovalRule, type EnvironmentRole, type Link, type Rules,
} from '@/lib/environments'
import { ConnectionMatching } from './ConnectionMatching'
import { LinkWizard } from './LinkWizard'

/**
 * Settings › Environments: what this install is, where it promotes
 * workflows to, and who may promote workflows into it.
 */
export function EnvironmentsTab() {
  const { data: instance, isLoading } = useInstance()
  if (isLoading) return <Group justify="center" p="xl"><Loader size="sm" aria-label="Loading environment" /></Group>
  const role = instance?.role || ''

  return (
    <Stack gap="xl">
      <Stack gap={4}>
        <Title order={3}>Environments</Title>
        <Text size="sm" c="dimmed">
          Run one Hermod per environment, then promote a tested workflow from one to the next. Production gets the
          workflow with its own connections, and an administrator there approves it.
        </Text>
      </Stack>
      {/* Keyed, so a saved change starts the draft over from it. */}
      <InstanceCard key={`${instance?.name}|${role}`} name={instance?.name ?? ''} role={role} />
      {role === '' && (
        <Alert color="blue" icon={<IconInfoCircle size="1rem" />}>
          Pick what this instance is first. A staging instance sends workflows; a production instance receives them.
        </Alert>
      )}
      {(role === 'development' || role === 'staging') && <SendingSection />}
      {(role === 'staging' || role === 'production') && <ReceivingSection role={role} />}
    </Stack>
  )
}

function InstanceCard({ name, role }: { name: string; role: EnvironmentRole | '' }) {
  const queryClient = useQueryClient()
  const [draftName, setDraftName] = useState(name)
  const [draftRole, setDraftRole] = useState<EnvironmentRole | ''>(role)

  const save = useMutation({
    mutationFn: () => putInstance({ name: draftName.trim(), role: draftRole as EnvironmentRole }),
    onSuccess: (saved) => {
      queryClient.setQueryData(environmentKeys.self, saved)
      notifications.show({ color: 'green', message: `This instance is now ${roleLabel[saved.role as EnvironmentRole]}.` })
    },
  })
  const dirty = draftName.trim() !== name || draftRole !== role

  return (
    <Paper withBorder p="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Text fw={600}>This instance</Text>
          {role && <Badge color={roleColor[role]} variant="filled">{roleLabel[role]}</Badge>}
        </Group>
        <SegmentedControl
          aria-label="What this instance is"
          value={draftRole || ''}
          onChange={(v) => setDraftRole(v as EnvironmentRole)}
          data={[
            ...(draftRole === '' ? [{ value: '', label: 'Not set', disabled: true }] : []),
            { value: 'development', label: 'Development' },
            { value: 'staging', label: 'Staging' },
            { value: 'production', label: 'Production' },
          ]}
        />
        <TextInput
          label="Name"
          description="Shown to the instances linked to this one, and in the header."
          placeholder={draftRole ? roleLabel[draftRole] : 'e.g. Production EU'}
          value={draftName}
          maxLength={64}
          onChange={(e) => setDraftName(e.currentTarget.value)}
        />
        <Group justify="flex-end">
          <Button onClick={() => save.mutate()} disabled={!dirty || draftRole === ''} loading={save.isPending}>
            Save
          </Button>
        </Group>
      </Stack>
    </Paper>
  )
}

function SendingSection() {
  const { data: links, isLoading } = useLinks()
  const [wizard, setWizard] = useState(false)

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Stack gap={0}>
          <Title order={4}>Promotes to</Title>
          <Text size="sm" c="dimmed">Where workflows from here go when someone clicks Promote.</Text>
        </Stack>
        <Button leftSection={<IconPlus size="1rem" />} onClick={() => setWizard(true)}>Link an environment</Button>
      </Group>
      {isLoading && <Loader size="sm" />}
      {!isLoading && (links ?? []).length === 0 && (
        <Paper withBorder p="lg" radius="md">
          <Text size="sm" c="dimmed" ta="center">
            Not linked to anything yet. Link your production instance to promote workflows to it.
          </Text>
        </Paper>
      )}
      {(links ?? []).map((l) => <LinkCard key={l.id} link={l} />)}
      <LinkWizard opened={wizard} onClose={() => setWizard(false)} />
    </Stack>
  )
}

function LinkCard({ link }: { link: Link }) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const [matching, setMatching] = useState(false)

  const test = useMutation({
    mutationFn: () => testLink(link.id),
    onSuccess: async (res) => {
      await queryClient.invalidateQueries({ queryKey: environmentKeys.links })
      notifications.show(res.error
        ? { color: 'red', title: `${link.name} did not answer`, message: res.error }
        : { color: 'green', message: `${link.name} answered: Hermod ${res.info?.version ?? ''}` })
    },
  })
  const unlink = useMutation({
    mutationFn: () => deleteLink(link.id),
    onSuccess: async (res) => {
      await queryClient.invalidateQueries({ queryKey: environmentKeys.links })
      notifications.show({ color: res.note ? 'yellow' : 'green', message: res.note || `Unlinked ${link.name}.` })
    },
  })

  const onUnlink = async () => {
    if (await confirm({
      title: `Unlink ${link.name}`,
      message: `Workflows can no longer be promoted from here to ${link.name}. What was already promoted stays there. You can link it again later with a new code.`,
      confirmLabel: 'Unlink',
      danger: true,
    })) unlink.mutate()
  }

  return (
    <Card withBorder radius="md" padding="lg">
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <Stack gap={4}>
          <Group gap="xs">
            <IconArrowRight size="1rem" aria-hidden />
            <Text fw={600}>{link.name}</Text>
            {link.role && <Badge color={roleColor[link.role]} variant="light">{roleLabel[link.role]}</Badge>}
            <Badge color={link.reachable ? 'green' : 'red'} variant="dot">{link.reachable ? 'Reachable' : 'Not reachable'}</Badge>
          </Group>
          <Text size="sm" c="dimmed">{link.url}</Text>
          <Text size="xs" c="dimmed">
            Linked by {link.created_by} on {formatDateTime(link.created_at)}
            {link.version ? ` · Hermod ${link.version}` : ''}
            {link.checked_at ? ` · checked ${formatDateTime(link.checked_at)}` : ''}
          </Text>
          {link.check_note && !link.reachable && <Text size="xs" c="red">{link.check_note}</Text>}
        </Stack>
        <Group gap="xs" wrap="nowrap">
          <Button size="xs" variant="light" leftSection={<IconPlugConnected size="0.9rem" />} onClick={() => setMatching(true)}>
            Match connections
          </Button>
          <Tooltip label="Test the connection">
            <ActionIcon variant="light" aria-label={`Test the connection to ${link.name}`} onClick={() => test.mutate()} loading={test.isPending}>
              <IconRefresh size="1rem" />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Unlink">
            <ActionIcon variant="light" color="red" aria-label={`Unlink ${link.name}`} onClick={onUnlink} loading={unlink.isPending}>
              <IconLinkOff size="1rem" />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      <Modal opened={matching} onClose={() => setMatching(false)} title={`Connections in ${link.name}`} size="xl">
        <ConnectionMatching link={link} onDone={() => setMatching(false)} />
      </Modal>
    </Card>
  )
}

function ReceivingSection({ role }: { role: EnvironmentRole }) {
  return (
    <Stack gap="sm">
      <Stack gap={0}>
        <Title order={4}>Receives from</Title>
        <Text size="sm" c="dimmed">
          The instances allowed to promote workflows into this {roleLabel[role].toLowerCase()} instance, and the rules
          their workflows go through.
        </Text>
      </Stack>
      <RulesCard />
      <PeersCard />
    </Stack>
  )
}

function RulesCard() {
  const { data: rules } = useRules()
  return rules ? <RulesForm key={JSON.stringify(rules)} rules={rules} /> : null
}

function RulesForm({ rules }: { rules: Rules }) {
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<Rules>(rules)

  const save = useMutation({
    mutationFn: (r: Rules) => putRules(r),
    onSuccess: (saved) => {
      queryClient.setQueryData(environmentKeys.rules, saved)
      notifications.show({ color: 'green', message: 'Promotion rules saved.' })
    },
  })
  const dirty = JSON.stringify(draft) !== JSON.stringify(rules)

  return (
    <Paper withBorder p="lg" radius="md">
      <Stack gap="md">
        <Text fw={600}>Rules</Text>
        <Select
          label="Who approves a promoted workflow"
          data={(Object.keys(approvalLabel) as ApprovalRule[]).map((v) => ({ value: v, label: approvalLabel[v] }))}
          value={draft.approval}
          allowDeselect={false}
          onChange={(v) => v && setDraft({ ...draft, approval: v as ApprovalRule })}
        />
        {draft.approval === 'none' && (
          <Alert color="yellow" variant="light">Every workflow a linked instance sends goes live here at once.</Alert>
        )}
        <Switch
          label="Roll back automatically when a deploy starts failing"
          description="Within 10 minutes of a deploy, a workflow that errors goes back to the version before it."
          checked={draft.auto_rollback}
          onChange={(e) => setDraft({ ...draft, auto_rollback: e.currentTarget.checked })}
        />
        <NumberInput
          label="Minimum time in the sending environment"
          description="A version must have been there this long before it can be promoted. 0 means no minimum."
          suffix=" min"
          min={0}
          max={43200}
          value={draft.min_soak_minutes}
          onChange={(v) => setDraft({ ...draft, min_soak_minutes: Number(v) || 0 })}
        />
        <Group justify="flex-end">
          <Button disabled={!dirty} loading={save.isPending} onClick={() => save.mutate(draft)}>Save rules</Button>
        </Group>
      </Stack>
    </Paper>
  )
}

function PeersCard() {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const { data: peers } = usePeers()
  const [code, setCode] = useState<{ code: string; expires_at: string } | null>(null)

  const issue = useMutation({ mutationFn: () => newPairingCode('Created in Settings'), onSuccess: setCode })
  const revoke = useMutation({
    mutationFn: (id: string) => revokePeer(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: environmentKeys.peers }),
  })

  return (
    <Paper withBorder p="lg" radius="md">
      <Stack gap="md">
        <Group justify="space-between">
          <Text fw={600}>Linked instances</Text>
          <Button size="xs" variant="light" leftSection={<IconKey size="0.9rem" />} onClick={() => issue.mutate()} loading={issue.isPending}>
            Create pairing code
          </Button>
        </Group>
        {code && (
          <Alert color="blue" title="Pairing code" withCloseButton onClose={() => setCode(null)}>
            <Group gap="xs">
              <Text ff="monospace" fz="xl" fw={700} style={{ letterSpacing: 3 }}>{code.code}</Text>
              <CopyButton value={code.code}>
                {({ copied, copy }) => (
                  <ActionIcon variant="subtle" aria-label="Copy the pairing code" onClick={copy}>
                    {copied ? <IconCheck size="1rem" /> : <IconCopy size="1rem" />}
                  </ActionIcon>
                )}
              </CopyButton>
            </Group>
            <Text size="sm">
              Enter it in the other instance under Settings › Environments › Link an environment. It works once, until{' '}
              {formatDateTime(code.expires_at)}.
            </Text>
          </Alert>
        )}
        {(peers ?? []).length === 0 ? (
          <Text size="sm" c="dimmed">Nothing is linked to this instance yet.</Text>
        ) : (
          (peers ?? []).map((p) => (
            <Group key={p.id} justify="space-between" wrap="nowrap">
              <Stack gap={0}>
                <Text size="sm" fw={500}>{p.name}</Text>
                <Text size="xs" c="dimmed">
                  {p.url} · linked {formatDateTime(p.created_at)}
                  {p.last_used_at ? ` · last used ${formatDateTime(p.last_used_at)}` : ''}
                </Text>
              </Stack>
              <Button
                size="xs"
                variant="subtle"
                color="red"
                leftSection={<IconTrash size="0.9rem" />}
                aria-label={`Revoke ${p.name}`}
                onClick={async () => {
                  if (await confirm({
                    title: `Revoke ${p.name}`,
                    message: `${p.name} can no longer promote workflows here. Workflows it already promoted stay.`,
                    confirmLabel: 'Revoke',
                    danger: true,
                  })) revoke.mutate(p.id)
                }}
              >
                Revoke
              </Button>
            </Group>
          ))
        )}
      </Stack>
    </Paper>
  )
}
