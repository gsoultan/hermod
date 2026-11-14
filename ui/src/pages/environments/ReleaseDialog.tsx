import { useState } from 'react'
import {
  Alert, Anchor, Badge, Button, Collapse, Group, Loader, Modal, Paper, Select, Stack, Switch, Text, Textarea, ThemeIcon,
  UnstyledButton,
} from '@mantine/core'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link as RouterLink } from '@tanstack/react-router'
import {
  IconAlertTriangle, IconArrowRight, IconCheck, IconChevronDown, IconPlus, IconRocket, IconX,
} from '@tabler/icons-react'
import { getSessionRole } from '@/auth/session'
import { filledValues, neededSecrets, SecretValues } from './SecretValues'
import { releaseConnections, ReleaseConnections, type ConnectionSettings } from './ReleaseConnections'
import { formatDateTime } from '@/utils/dateUtils'
import {
  environmentKeys, getMatching, previewRelease, putMapping, sendRelease, useInstance, useLinks, useReady,
  type ApprovalRule, type MatchRow, type Preview, type ReleaseGroup, type ReleaseItem, type SentRelease,
} from '@/lib/environments'

interface Version {
  version: number
  created_at?: string
}

interface ReleaseDialogProps {
  opened: boolean
  onClose: () => void
  /** What to promote; each workflow goes to the environment its vhost is connected to. */
  items: ReleaseItem[]
  /** Offered when there is one workflow, so an older version can go instead of the newest. */
  versions?: Version[]
  title?: string
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? '' : 's'}`
const isAdmin = () => getSessionRole() === 'Administrator'

/** Who approves there, as the person sending reads it. */
export const approvalLine: Record<ApprovalRule, (where: string) => string> = {
  admin_except_sender: (where) => `An administrator other than you approves it for ${where}`,
  any_admin: (where) => `An administrator approves it for ${where}`,
  none: (where) => `Goes live in ${where} without approval`,
}

/** The things that keep a workflow from being sent, each said once. */
export function problems(p: Preview): string[] {
  const out: string[] = []
  for (const u of p.unmatched ?? []) out.push(`Pick the ${p.link_name} connection for “${u.name}”`)
  const blockedConns = (p.connections ?? []).filter((c) => c.status === 'blocked')
  for (const c of blockedConns) out.push(`Move the plain-text ${(c.plain_keys ?? []).join(', ')} of “${c.name}” to a secret`)
  for (const e of p.missing_envs ?? []) out.push(`${e} is not set on the ${p.link_name} server`)
  for (const c of p.checks) {
    if (c.ok || !c.blocking) continue
    // Shown as the rows above, with a way to fix them.
    if (c.name === 'Connections matched' && ((p.unmatched ?? []).length > 0 || blockedConns.length > 0)) continue
    out.push(c.detail || c.name)
  }
  return out
}

/**
 * Promotes workflows as one release, on one screen: where it goes, what
 * changes, what to fix, and the button. Each workflow goes to the
 * environment its vhost is connected to; that environment checks them all
 * first. A connection with no match is matched right here, and anything
 * that still cannot land keeps the button off until it is fixed or left out.
 * Whatever is sent together is approved, undone and rolled back together.
 */
export function ReleaseDialog({ opened, onClose, items, versions = [], title = 'Promote' }: ReleaseDialogProps) {
  return (
    <Modal opened={opened} onClose={onClose} title={title} size="lg">
      {/* Remounted each time it opens, so a new release starts from the items it was given. */}
      {opened && <ReleaseForm items={items} versions={versions} onClose={onClose} />}
    </Modal>
  )
}

function ReleaseForm({ items, versions, onClose }: { items: ReleaseItem[]; versions: Version[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const { data: self } = useInstance()
  const { data: lanes } = useReady()
  const [kept, setKept] = useState<ReleaseItem[]>(items)
  const [note, setNote] = useState('')
  const [start, setStart] = useState(true)
  const [autoRollback, setAutoRollback] = useState<boolean | null>(null)
  const [more, setMore] = useState(false)
  const [showChecks, setShowChecks] = useState(false)
  const [sent, setSent] = useState<SentRelease[] | null>(null)
  const [secretValues, setSecretValues] = useState<Record<string, string>>({})
  const [settings, setSettings] = useState<ConnectionSettings>({})

  const preview = useQuery({
    queryKey: ['environments', 'release-preview', kept],
    queryFn: () => previewRelease(kept),
    enabled: kept.length > 0 && !sent,
    placeholderData: keepPreviousData,
    staleTime: 0,
  })
  const groups = (preview.data?.groups ?? []).filter((g) => g.previews.length > 0)
  const all = groups.flatMap((g) => g.previews)
  const unlinked = preview.data?.unlinked ?? []
  const rules = all[0]?.rules
  const rollback = autoRollback ?? rules?.auto_rollback ?? true
  const settled = !preview.isFetching && !preview.isPlaceholderData
  const blocked = all.filter((p) => !p.ready)
  const toFix = blocked.reduce((n, p) => n + problems(p).length, 0) + unlinked.length
  const where = [...new Set(groups.map((g) => g.link_name))].join(' and ') || 'the next environment'
  const goesLive = rules?.approval === 'none'
  const here = self?.name || 'Here'
  const needed = neededSecrets(all)
  const typed = filledValues(Object.fromEntries(needed.map((n) => [n.name, secretValues[n.name] ?? ''])))
  const leftForApprover = needed.length - Object.keys(typed).length

  const send = useMutation({
    mutationFn: () => sendRelease(kept, {
      note: note.trim(), start, auto_rollback: rollback,
      ...(Object.keys(typed).length ? { secrets: typed } : {}),
      ...(Object.keys(settings).length ? { settings } : {}),
    }),
    onSuccess: setSent,
    onSettled: () => Promise.all([
      queryClient.invalidateQueries({ queryKey: environmentKeys.sent }),
      queryClient.invalidateQueries({ queryKey: environmentKeys.ready }),
    ]),
  })

  const leaveOut = (workflowId: string) => setKept((k) => k.filter((i) => i.workflow_id !== workflowId))
  const add = (workflowId: string | null) => {
    if (workflowId) setKept((k) => [...k, { workflow_id: workflowId, version: 0 }])
  }
  const setVersion = (v: string | null) => setKept((k) => k.map((i) => ({ ...i, version: Number(v ?? 0) })))
  const recheck = () => queryClient.invalidateQueries({ queryKey: ['environments', 'release-preview'] })

  // Workflows ready to promote that are not in this release yet.
  const addable = (lanes ?? []).flatMap((l) => l.workflows ?? []).filter((w) => !kept.some((k) => k.workflow_id === w.id))

  if (sent) return <Sent releases={sent} groups={groups} onClose={onClose} />
  if (kept.length === 0) {
    return (
      <Stack>
        <Text size="sm" c="dimmed">Everything was left out, so there is nothing to promote.</Text>
        <Group justify="flex-end"><Button variant="default" onClick={onClose}>Close</Button></Group>
      </Stack>
    )
  }

  const single = kept.length === 1 && all.length === 1 ? all[0] : null
  const sendLabel = goesLive
    ? (kept.length === 1 ? 'Send and go live' : `Send ${kept.length} and go live`)
    : (kept.length === 1 ? 'Send for approval' : `Send ${kept.length} for approval`)

  return (
    <Stack gap="md">
      {rules && <Text size="sm" c="dimmed">{approvalLine[rules.approval](where)}.</Text>}
      <Route here={here} kept={kept.length} previews={all} groups={groups} />

      {preview.isLoading && <Group gap="xs"><Loader size="xs" /><Text size="sm">Checking with each environment…</Text></Group>}
      {preview.error && <Alert color="red" title="Could not run the checks">{(preview.error as Error).message}</Alert>}

      {preview.data && (single ? (
        <SingleSummary p={single} showChecks={showChecks} onToggleChecks={() => setShowChecks((s) => !s)} onFixed={recheck} />
      ) : (
        <Stack gap="sm">
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm" fw={600} c={blocked.length ? 'orange.8' : 'green.8'}>
              {blocked.length || unlinked.length
                ? `${all.length - blocked.length} of ${all.length + unlinked.length} flows are ready`
                : `All ${plural(all.length, 'flow')} are ready`}
            </Text>
          </Group>
          {groups.map((g) => <GroupCard key={g.link_id} group={g} onLeaveOut={leaveOut} onFixed={recheck} />)}
        </Stack>
      ))}

      {unlinked.map((u) => (
        <Alert key={u.workflow_id} color="orange" icon={<IconAlertTriangle size="1rem" />}>
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm">
              Nothing is connected for {u.vhost}, so <b>{u.workflow_name}</b> has nowhere to go. An administrator
              connects it in Settings › Workspace › Environments.
            </Text>
            <Button size="xs" variant="default" aria-label={`Leave out ${u.workflow_name}`} onClick={() => leaveOut(u.workflow_id)}>
              Leave out
            </Button>
          </Group>
        </Alert>
      ))}

      {addable.length > 0 && (
        <Select
          aria-label="Add another flow"
          placeholder="+ Add another flow"
          leftSection={<IconPlus size="0.9rem" />}
          data={addable.map((w) => ({ value: w.id, label: w.name }))}
          value={null}
          onChange={add}
          searchable
          clearable={false}
        />
      )}

      <ReleaseConnections rows={releaseConnections(all)} where={where} settings={settings} onSettings={setSettings} onMoved={recheck} />

      <SecretValues needed={needed} values={secretValues} onChange={setSecretValues} where={where} mode="send" />

      <Textarea
        label="Note for the approver"
        description="Optional"
        placeholder="What changed and why"
        autosize
        minRows={2}
        maxLength={1000}
        value={note}
        onChange={(e) => setNote(e.currentTarget.value)}
      />

      <Paper withBorder radius="md">
        <UnstyledButton w="100%" p="sm" onClick={() => setMore((m) => !m)} aria-expanded={more}>
          <Group justify="space-between" wrap="nowrap">
            <Stack gap={0}>
              <Text size="sm" fw={500}>More options</Text>
              <Text size="xs" c="dimmed">
                {kept.length === 1 && kept[0].version ? `v${kept[0].version}` : 'Newest version'}
                {' · '}{start ? 'start after deploy' : 'stay stopped'}
                {' · '}{rollback ? 'undo if it fails' : 'no automatic undo'}
              </Text>
            </Stack>
            <IconChevronDown size="1rem" aria-hidden style={{ transform: more ? 'rotate(180deg)' : undefined }} />
          </Group>
        </UnstyledButton>
        <Collapse expanded={more}>
          <Stack gap="sm" p="sm" pt={0}>
            {kept.length === 1 && versions.length > 0 && (
              <Select
                label="Version"
                data={[
                  { value: '0', label: 'Newest' },
                  ...versions.map((v) => ({
                    value: String(v.version),
                    label: `v${v.version}${v.created_at ? ` · ${formatDateTime(v.created_at)}` : ''}`,
                  })),
                ]}
                value={String(kept[0].version)}
                allowDeselect={false}
                onChange={setVersion}
              />
            )}
            <Switch label="Start them once deployed" checked={start} onChange={(e) => setStart(e.currentTarget.checked)} />
            <Switch
              label="Undo the whole release if one of them starts failing"
              checked={rollback}
              onChange={(e) => setAutoRollback(e.currentTarget.checked)}
            />
          </Stack>
        </Collapse>
      </Paper>

      {send.error && <Alert color="red" title="Not everything was sent">{(send.error as Error).message}</Alert>}
      <Group justify="space-between" wrap="wrap" gap="sm">
        <Text size="sm" c={toFix ? 'orange.8' : 'dimmed'}>
          {!preview.data ? '' : toFix
            ? (single ? `Fix ${plural(toFix, 'thing')} to send.` : `Fix or leave out ${[...blocked.map((p) => p.workflow_name), ...unlinked.map((u) => u.workflow_name)].join(', ')} to send.`)
            : goesLive ? `Goes live in ${where} at once.` : `Goes live in ${where} after approval.`}
          {!toFix && leftForApprover > 0 && (
            <>{' '}<Text span inherit>{plural(leftForApprover, 'secret value')} left for the approver.</Text></>
          )}
        </Text>
        <Group gap="sm">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button
            leftSection={<IconRocket size="1rem" />}
            disabled={!settled || !preview.data?.ready}
            loading={send.isPending}
            onClick={() => send.mutate()}
          >
            {sendLabel}
          </Button>
        </Group>
      </Group>
    </Stack>
  )
}

/** Where it goes: this instance and version on the left, what runs there now on the right. */
function Route({ here, kept, previews, groups }: { here: string; kept: number; previews: Preview[]; groups: ReleaseGroup[] }) {
  if (previews.length === 0) return null
  const one = kept === 1 && previews.length === 1 ? previews[0] : null
  const fresh = previews.filter((p) => p.current_version === 0).length
  const there = [...new Set(groups.map((g) => `${g.link_name} · ${g.target_vhost || g.vhost}`))].join(', ')
  return (
    <Paper withBorder radius="md" p="sm">
      <Group gap="sm" wrap="nowrap" justify="space-between">
        <Stack gap={0}>
          <Text size="xs" c="dimmed">{here}</Text>
          <Text size="sm" fw={600}>{one ? `v${one.from_version}` : plural(kept, 'flow')}</Text>
          {one?.from_version_at && <Text size="xs" c="dimmed">saved {formatDateTime(one.from_version_at)}</Text>}
        </Stack>
        <IconArrowRight size="1.1rem" aria-hidden />
        <Stack gap={0} ta="right">
          <Text size="xs" c="dimmed">{there}</Text>
          <Text size="sm" fw={600}>
            {one
              ? (one.current_version > 0 ? `now runs v${one.current_version}` : 'new there')
              : `${fresh} new, ${previews.length - fresh} updated`}
          </Text>
        </Stack>
      </Group>
    </Paper>
  )
}

function SingleSummary({ p, showChecks, onToggleChecks, onFixed }: {
  p: Preview; showChecks: boolean; onToggleChecks: () => void; onFixed: () => void
}) {
  const passed = p.checks.filter((c) => c.ok)
  const warnings = p.checks.filter((c) => !c.ok && !c.blocking)
  const changes = p.changes ?? []
  return (
    <Stack gap="sm">
      {p.ready ? (
        <Alert color="green" icon={<IconCheck size="1rem" />} title={`Ready to send. ${passed.length === p.checks.length ? `All ${passed.length} checks passed.` : `${passed.length} of ${p.checks.length} checks passed.`}`}>
          <Anchor component="button" type="button" size="sm" onClick={onToggleChecks}>{showChecks ? 'Hide checks' : 'Show checks'}</Anchor>
          <Collapse expanded={showChecks}><Checks p={p} /></Collapse>
        </Alert>
      ) : (
        <Fixes p={p} onFixed={onFixed} />
      )}
      {warnings.map((c) => (
        <Alert key={c.name} color="yellow" variant="light" icon={<IconAlertTriangle size="1rem" />}>
          <Text size="sm">{c.detail || c.name}</Text>
        </Alert>
      ))}
      {changes.length > 0 && (
        <Stack gap={2}>
          <Text size="sm" fw={600}>What changes in {p.link_name}</Text>
          {changes.map((c, i) => (
            <Text key={i} size="sm" c={c.kind === 'removed' ? 'red.8' : c.kind === 'added' ? 'green.8' : undefined}>
              {c.kind === 'added' ? '+ ' : c.kind === 'removed' ? '− ' : '~ '}{c.what}
            </Text>
          ))}
        </Stack>
      )}
    </Stack>
  )
}

function Checks({ p }: { p: Preview }) {
  return (
    <Stack gap={2} mt="xs">
      {p.checks.map((c) => (
        <Text key={c.name} size="xs" c={c.ok ? 'dimmed' : c.blocking ? 'red' : 'yellow.8'}>
          {c.ok ? '✓' : '!'} {c.name}{c.detail ? `: ${c.detail}` : ''}
        </Text>
      ))}
    </Stack>
  )
}

/** Each thing to fix, numbered, with the fix right under it where there is one. */
function Fixes({ p, onFixed }: { p: Preview; onFixed: () => void }) {
  const list = problems(p)
  const passed = p.checks.filter((c) => c.ok).length
  const others = p.checks.filter((c) => !c.ok && c.blocking && !(c.name === 'Connections matched' && (p.unmatched ?? []).length > 0))
  let n = 0
  return (
    <Paper withBorder radius="md" p="md" style={{ borderColor: 'var(--mantine-color-orange-5)' }}>
      <Stack gap="md">
        <Group justify="space-between" wrap="nowrap">
          <Text fw={600} c="orange.8">{plural(list.length, 'thing')} to fix before sending</Text>
          <Text size="xs" c="dimmed">{plural(passed, 'check')} passed</Text>
        </Group>
        {(p.unmatched ?? []).map((u) => (
          <FixConnection key={`${u.kind}-${u.id}`} n={++n} p={p} row={u} onFixed={onFixed} />
        ))}
        {(p.missing_envs ?? []).map((e) => (
          <Stack key={e} gap={2}>
            <Text size="sm" fw={600}>{++n}. {e} is not set on the {p.link_name} server</Text>
            <Text size="sm" c="dimmed">
              The flow reads <code>{`{{env.${e}}}`}</code>. Server variables live in the server&apos;s own settings, so
              whoever runs {p.link_name} sets <code>HERMOD_ENV_{e}</code>.
            </Text>
          </Stack>
        ))}
        {others.map((c) => (
          <Text key={c.name} size="sm"><b>{++n}.</b> {c.detail || c.name}</Text>
        ))}
      </Stack>
    </Paper>
  )
}

/**
 * Matches one connection with no match there, right where the problem is
 * shown. Production's connections come from the matching screen, which is
 * for administrators; an editor is told who can do it.
 */
function FixConnection({ n, p, row, onFixed }: { n: number; p: Preview; row: MatchRow; onFixed: () => void }) {
  const admin = isAdmin()
  const queryClient = useQueryClient()
  const matching = useQuery({
    queryKey: environmentKeys.matching(p.link_id, p.vhost),
    queryFn: ({ signal }) => getMatching(p.link_id, p.vhost, signal),
    enabled: admin,
  })
  const candidates = (matching.data ?? []).find((m) => m.kind === row.kind && m.id === row.id)?.candidates ?? []
  const save = useMutation({
    mutationFn: (target: string) => putMapping(p.link_id, { [row.id]: target }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['environments', 'matching', p.link_id] })
      onFixed()
    },
  })
  const label = `${p.link_name} connection for ${row.name}`
  return (
    <Stack gap={4}>
      <Text size="sm" fw={600}>{n}. Pick the {p.link_name} connection for “{row.name}”</Text>
      <Text size="sm" c="dimmed">Hermod could not find a match by name and type.</Text>
      {admin ? (
        <>
          <Group gap="xs" wrap="nowrap" align="center">
            <Badge variant="light" tt="none" style={{ flex: 'none' }}>{row.name}{row.type ? ` · ${row.type}` : ''}</Badge>
            <IconArrowRight size="0.9rem" aria-hidden style={{ flex: 'none' }} />
            <Select
              aria-label={label}
              placeholder={matching.isLoading ? 'Loading…' : `Choose in ${p.link_name}…`}
              data={candidates.map((c) => ({ value: c.id, label: `${c.name} · ${c.type}` }))}
              value={null}
              onChange={(v) => v && save.mutate(v)}
              disabled={save.isPending || matching.isLoading}
              nothingFoundMessage={`No ${row.type || 'matching'} connection in ${p.link_name} yet`}
              searchable
              style={{ flex: 1 }}
            />
          </Group>
          <Text size="xs" c="dimmed">
            Saved for next time.{candidates.length === 0 && !matching.isLoading ? ` Not there yet? Create it in ${p.link_name} first.` : ''}
          </Text>
          {save.error && <Text size="xs" c="red">{(save.error as Error).message}</Text>}
        </>
      ) : (
        <Text size="xs" c="dimmed">An administrator matches it here, or in Settings › Workspace › Environments.</Text>
      )}
    </Stack>
  )
}

function GroupCard({ group, onLeaveOut, onFixed }: { group: ReleaseGroup; onLeaveOut: (workflowId: string) => void; onFixed: () => void }) {
  return (
    <Paper withBorder radius="md" p="md">
      <Stack gap="sm">
        <Group gap={6} wrap="nowrap">
          <Badge variant="light" color="yellow" tt="none">{group.vhost}</Badge>
          <IconArrowRight size="0.9rem" aria-hidden />
          <Text fw={600}>{group.link_name} · {group.target_vhost || group.vhost}</Text>
        </Group>
        {group.previews.map((p) => <PreviewRow key={p.workflow_id} p={p} onLeaveOut={onLeaveOut} onFixed={onFixed} />)}
      </Stack>
    </Paper>
  )
}

function PreviewRow({ p, onLeaveOut, onFixed }: { p: Preview; onLeaveOut: (workflowId: string) => void; onFixed: () => void }) {
  const [open, setOpen] = useState<'fix' | 'changes' | null>(null)
  const Icon = p.ready ? IconCheck : IconX
  const changes = p.changes ?? []
  const first = problems(p)[0]
  const toggle = (what: 'fix' | 'changes') => setOpen((o) => (o === what ? null : what))
  return (
    <Stack gap={4}>
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <ThemeIcon size={20} radius="xl" variant="light" color={p.ready ? 'green' : 'orange'}><Icon size="0.8rem" /></ThemeIcon>
          <Stack gap={0} style={{ minWidth: 0 }}>
            <Text size="sm" fw={500}>{p.workflow_name}</Text>
            <Text size="xs" c={p.ready ? 'dimmed' : 'orange.8'}>
              {p.current_version > 0 ? `v${p.current_version} → v${p.from_version}` : `New in ${p.link_name} · v${p.from_version}`}
              {p.ready ? (changes.length ? ` · ${plural(changes.length, 'change')}` : '') : first ? ` · ${first}` : ''}
            </Text>
          </Stack>
        </Group>
        <Group gap="xs" wrap="nowrap">
          {p.ready ? (
            changes.length > 0 && (
              <Button size="xs" variant="subtle" aria-label={`Changes in ${p.workflow_name}`} onClick={() => toggle('changes')}>Changes</Button>
            )
          ) : (
            <>
              <Button size="xs" variant="light" aria-label={`Fix ${p.workflow_name}`} onClick={() => toggle('fix')}>Fix</Button>
              <Button size="xs" variant="default" aria-label={`Leave out ${p.workflow_name}`} onClick={() => onLeaveOut(p.workflow_id)}>
                Leave out
              </Button>
            </>
          )}
        </Group>
      </Group>
      <Collapse expanded={open === 'fix'}>{open === 'fix' && <Fixes p={p} onFixed={onFixed} />}</Collapse>
      <Collapse expanded={open === 'changes'}>
        <Stack gap={0} pl={28}>
          {changes.map((c, i) => <Text key={i} size="xs" c="dimmed">{c.what}</Text>)}
        </Stack>
      </Collapse>
      {p.ready && p.checks.filter((c) => !c.ok && !c.blocking).map((c) => (
        <Text key={c.name} size="xs" c="yellow.8" pl={28}>{c.detail || c.name}</Text>
      ))}
    </Stack>
  )
}

/**
 * What happened to each release, and where to follow it. A release that waits
 * says who approves it and where they find it; an administrator who may decide
 * it from here is taken straight to it.
 */
function Sent({ releases, groups, onClose }: { releases: SentRelease[]; groups: ReleaseGroup[]; onClose: () => void }) {
  const isAdmin = getSessionRole() === 'Administrator'
  const { data: links } = useLinks()
  const decidesHere = (linkId: string) => isAdmin && (links ?? []).some((l) => l.id === linkId && l.decide)
  return (
    <Stack>
      {releases.map((r) => {
        const g = groups.find((x) => x.link_id === r.link_id)
        const where = `${r.link_name}${g ? ` · ${g.target_vhost || g.vhost}` : ''}`
        const live = r.promotions.every((p) => p.status === 'deployed')
        return (
          <Alert key={r.link_id} color={live ? 'green' : 'blue'} icon={<IconCheck size="1rem" />}>
            {plural(r.promotions.length, 'workflow')}: the release {live ? 'is live' : 'waits for approval'} in {where}.
            {!live && (decidesHere(r.link_id) ? (
              <>{' '}<Anchor component={RouterLink} to="/approvals" search={{ tab: 'releases' } as never} size="sm" fw={600}>Approve it now</Anchor></>
            ) : (
              <>{' '}An administrator of {r.link_name} approves it under Approvals there.</>
            ))}
          </Alert>
        )
      })}
      <Group justify="space-between">
        <Anchor component={RouterLink} to="/environments" size="sm">Follow it on Releases</Anchor>
        <Button onClick={onClose}>Close</Button>
      </Group>
    </Stack>
  )
}
