import { useState } from 'react'
import {
  ActionIcon, Alert, Autocomplete, Badge, Button, CopyButton, Group, Loader, NumberInput, Paper,
  SegmentedControl, Select, Stack, Switch, Text, TextInput, Title,
} from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import {
  IconCheck, IconCopy, IconInfoCircle, IconKey, IconPlus,
  IconTrash, IconX,
} from '@tabler/icons-react'
import { useConfirm } from '@/components/common/ConfirmProvider'
import { formatDateTime } from '@/utils/dateUtils'
import {
  allowVHostRequest, approvalLabel, declineVHostRequest, environmentKeys, groupLinksByServer, groupPeers, newPairingCode,
  putInstance, putRules, revokePeer, revokePeerGroup, roleColor, roleLabel, useInstance, useLinks, usePeers, useRules,
  useVHostNames, useVHostRequests, vhostNameProblem, type ApprovalRule, type EnvironmentRole, type Link, type Peer,
  type Rules,
} from '@/lib/environments'
import { AddVHostsDialog } from './AddVHostsDialog'
import { ServerCard } from './ServerCard'
import { LinkWizard } from './LinkWizard'
import { PeerDecideSwitch } from './PeerDecideSwitch'

/**
 * Settings › Workspace › Environments: what this install is, where each of its vhosts
 * promotes workflows to, and who may promote workflows into it.
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
  const [via, setVia] = useState<Link | null>(null)
  const servers = groupLinksByServer(links ?? [])
  const taken = (links ?? []).map((l) => l.vhost).filter(Boolean)

  return (
    <Stack gap="sm">
      <Group justify="space-between">
        <Stack gap={0}>
          <Title order={4}>Promotes to</Title>
          <Text size="sm" c="dimmed">
            Each server is trusted once. Add the virtual hosts whose flows go there to it, without a new pairing code.
          </Text>
        </Stack>
        <Button leftSection={<IconPlus size="1rem" />} onClick={() => setWizard(true)}>Connect a server</Button>
      </Group>
      {isLoading && <Loader size="sm" />}
      {!isLoading && servers.length === 0 && (
        <Paper withBorder p="lg" radius="md">
          <Text size="sm" c="dimmed" ta="center">
            Not linked to anything yet. Connect your production server to promote workflows there.
          </Text>
        </Paper>
      )}
      {servers.map((sv) => <ServerCard key={sv.url} server={sv} onAddVHosts={setVia} />)}
      <LinkWizard
        opened={wizard}
        onClose={() => setWizard(false)}
        links={links ?? []}
        onAddVHosts={(l) => { setWizard(false); setVia(l) }}
      />
      <AddVHostsDialog opened={via !== null} via={via} taken={taken} onClose={() => setVia(null)} />
    </Stack>
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
          description="Within 10 minutes of a deploy, a release with a workflow that errors goes back, whole, to how it was."
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
        <VHostRulesEditor draft={draft} setDraft={setDraft} />
        <Group justify="flex-end">
          <Button disabled={!dirty} loading={save.isPending} onClick={() => save.mutate(draft)}>Save rules</Button>
        </Group>
      </Stack>
    </Paper>
  )
}

const approvalOptions = (Object.keys(approvalLabel) as ApprovalRule[]).map((v) => ({ value: v, label: approvalLabel[v] }))

/** Stricter (or looser) rules for promotions into one vhost, such as payroll. */
function VHostRulesEditor({ draft, setDraft }: { draft: Rules; setDraft: (r: Rules) => void }) {
  const { data: vhosts } = useVHostNames()
  const [name, setName] = useState('')
  const byVHost = draft.by_vhost ?? {}
  const entries = Object.entries(byVHost).sort(([a], [b]) => a.localeCompare(b))
  const trimmed = name.trim()
  const problem = trimmed ? vhostNameProblem(trimmed) ?? (trimmed in byVHost ? 'It already has rules of its own.' : null) : null

  const put = (next: NonNullable<Rules['by_vhost']>) => {
    const { by_vhost: _, ...rest } = draft
    setDraft(Object.keys(next).length > 0 ? { ...rest, by_vhost: next } : rest)
  }

  return (
    <Stack gap="xs">
      <Text fw={500} size="sm">Rules for one virtual host</Text>
      <Text size="xs" c="dimmed">A virtual host listed here uses its own approval and minimum time instead of the ones above.</Text>
      {entries.map(([v, r]) => (
        <Group key={v} gap="xs" wrap="nowrap">
          <Badge variant="light" tt="none" miw={90}>{v}</Badge>
          <Select
            aria-label={`${v}: who approves`}
            size="xs"
            style={{ flex: 2 }}
            data={approvalOptions}
            value={r.approval}
            allowDeselect={false}
            onChange={(a) => a && put({ ...byVHost, [v]: { ...r, approval: a as ApprovalRule } })}
          />
          <NumberInput
            aria-label={`${v}: minimum time in the sending environment`}
            size="xs"
            style={{ flex: 1 }}
            suffix=" min"
            min={0}
            max={43200}
            value={r.min_soak_minutes}
            onChange={(n) => put({ ...byVHost, [v]: { ...r, min_soak_minutes: Number(n) || 0 } })}
          />
          <ActionIcon
            variant="subtle"
            color="red"
            aria-label={`Remove the rules for ${v}`}
            onClick={() => {
              const { [v]: _, ...rest } = byVHost
              put(rest)
            }}
          >
            <IconX size="1rem" />
          </ActionIcon>
        </Group>
      ))}
      <Group gap="xs" align="flex-end">
        <Autocomplete
          label="Vhost with its own rules"
          size="xs"
          placeholder="e.g. payroll"
          data={(vhosts ?? []).filter((v) => !(v in byVHost))}
          value={name}
          error={problem}
          onChange={setName}
          style={{ flex: 1 }}
        />
        <Button
          size="xs"
          variant="light"
          disabled={!trimmed || !!problem}
          onClick={() => {
            put({ ...byVHost, [trimmed]: { approval: draft.approval, min_soak_minutes: draft.min_soak_minutes } })
            setName('')
          }}
        >
          Add vhost rules
        </Button>
      </Group>
    </Stack>
  )
}

function PeersCard() {
  const queryClient = useQueryClient()
  const { data: peers } = usePeers()
  const { data: vhosts } = useVHostNames()
  const [into, setInto] = useState('')
  const [code, setCode] = useState<{ code: string; expires_at: string; vhost: string } | null>(null)
  const intoProblem = into ? vhostNameProblem(into.trim()) : null

  const issue = useMutation({ mutationFn: () => newPairingCode('Created in Settings', into.trim()), onSuccess: setCode })
  const revoke = useMutation({
    mutationFn: (id: string) => revokePeer(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: environmentKeys.peers }),
  })

  return (
    <Paper withBorder p="lg" radius="md">
      <Stack gap="md">
        <Text fw={600}>Linked instances</Text>
        <Group gap="xs" align="flex-end">
          <Autocomplete
            label="Into virtual host"
            description="Where its workflows land. Created when the code is used, if it is not here yet."
            size="xs"
            placeholder="e.g. hr"
            data={vhosts ?? []}
            value={into}
            error={intoProblem}
            onChange={setInto}
            style={{ flex: 1 }}
          />
          <Button
            size="xs"
            variant="light"
            leftSection={<IconKey size="0.9rem" />}
            disabled={!into.trim() || !!intoProblem}
            onClick={() => issue.mutate()}
            loading={issue.isPending}
          >
            Create pairing code
          </Button>
        </Group>
        {issue.error && <Alert color="red">{(issue.error as Error).message}</Alert>}
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
              Enter it in the other instance under Settings › Workspace › Environments › Connect a server. Its workflows land
              in {code.vhost}. It works once, until {formatDateTime(code.expires_at)}.
            </Text>
          </Alert>
        )}
        <VHostRequests />
        {(peers ?? []).length === 0 ? (
          <Text size="sm" c="dimmed">Nothing is linked to this instance yet.</Text>
        ) : (
          groupPeers(peers ?? []).map((g) => (
            <PeerConnection key={g.group} group={g} onRevoke={(id) => revoke.mutate(id)} />
          ))
        )}
      </Stack>
    </Paper>
  )
}

function PeerConnection({ group, onRevoke }: {
  group: ReturnType<typeof groupPeers>[number]
  onRevoke: (peerId: string) => void
}) {
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const cut = useMutation({
    mutationFn: () => revokePeerGroup(group.group),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: environmentKeys.peers }),
  })
  const titleId = `peer-${group.group}`
  return (
    <Paper component="section" withBorder radius="md" p="md" aria-labelledby={titleId}>
      <Stack gap="xs">
        <Group justify="space-between" wrap="wrap">
          <Stack gap={0}>
            <Text size="sm" fw={600} id={titleId}>{group.name}</Text>
            <Text size="xs" c="dimmed">
              {group.url} · {group.addVHosts ? 'may add vhosts on its own' : 'asks before adding a vhost'}
            </Text>
          </Stack>
          <Button
            size="xs"
            variant="subtle"
            color="red"
            leftSection={<IconTrash size="0.9rem" />}
            aria-label={`Cut ${group.name} off`}
            loading={cut.isPending}
            onClick={async () => {
              if (await confirm({
                title: `Cut ${group.name} off`,
                message: `${group.name} can no longer promote workflows into any vhost here. Workflows it already promoted stay.`,
                confirmLabel: 'Cut off',
                danger: true,
              })) cut.mutate()
            }}
          >
            Cut off
          </Button>
        </Group>
        {group.peers.map((p: Peer) => (
          <Group key={p.id} justify="space-between" wrap="nowrap" pl="sm">
            <Stack gap={0}>
              <Badge size="sm" variant="light" tt="none">{p.vhost ? `into ${p.vhost}` : 'into any vhost (older link)'}</Badge>
              <Text size="xs" c="dimmed">
                {p.from_vhost ? `from ${p.from_vhost} · ` : ''}linked {formatDateTime(p.created_at)}
                {p.last_used_at ? ` · last used ${formatDateTime(p.last_used_at)}` : ''}
              </Text>
            </Stack>
            <Group gap="sm" wrap="nowrap">
              <PeerDecideSwitch peer={p} />
              {group.peers.length > 1 && (
              <Button
                size="xs"
                variant="subtle"
                color="red"
                aria-label={`Revoke ${p.vhost || 'the older link'} for ${p.name}`}
                onClick={async () => {
                  if (await confirm({
                    title: `Revoke ${p.vhost} for ${p.name}`,
                    message: `${p.name} can no longer promote into ${p.vhost}. Its other vhosts keep working.`,
                    confirmLabel: 'Revoke',
                    danger: true,
                  })) onRevoke(p.id)
                }}
              >
                Revoke
              </Button>
              )}
            </Group>
          </Group>
        ))}
      </Stack>
    </Paper>
  )
}

/** Vhosts a connected instance asked to add, for an administrator to allow; also on the Approvals page. */
export function VHostRequests() {
  const queryClient = useQueryClient()
  const { data: reqs } = useVHostRequests()
  const refresh = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: environmentKeys.vhostRequests }),
    queryClient.invalidateQueries({ queryKey: environmentKeys.peers }),
  ])
  const allow = useMutation({ mutationFn: allowVHostRequest, onSuccess: refresh })
  const decline = useMutation({ mutationFn: declineVHostRequest, onSuccess: refresh })
  if (!reqs || reqs.length === 0) return null
  return (
    <Stack gap="xs">
      {reqs.map((r) => (
        <Alert key={r.id} color="yellow" variant="light">
          <Group justify="space-between" wrap="wrap">
            <Text size="sm">
              {r.peer_name} asks to add <b>{r.vhost}</b>{r.from_vhost ? ` (from its ${r.from_vhost})` : ''}. It is created here
              if missing.
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" aria-label={`Decline ${r.vhost}`} onClick={() => decline.mutate(r.id)}>Decline</Button>
              <Button size="xs" aria-label={`Allow ${r.vhost}`} loading={allow.isPending} onClick={() => allow.mutate(r.id)}>Allow</Button>
            </Group>
          </Group>
        </Alert>
      ))}
    </Stack>
  )
}
