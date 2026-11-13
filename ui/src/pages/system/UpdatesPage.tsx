import { Alert, Anchor, Badge, Button, Group, Loader, Modal, Paper, Radio, Select, SimpleGrid, Spoiler, Stack, Switch, Text, Title } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconAlertTriangle, IconArrowBackUp, IconCircleCheck, IconDownload, IconExternalLink, IconRefresh } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { apiFetch } from '@/api';
import { getSessionRole } from '@/auth/session';
import { PageHeader } from '@/components/rows';

export type UpdateMode = 'automatic' | 'manual' | 'off';

export interface UpdateSettings {
  mode: UpdateMode;
  include_prereleases: boolean;
  check_interval_hours: number;
}

export interface UpdateStatus {
  current: string;
  build: string;
  latest?: { tag: string; name?: string; notes?: string; url?: string; published_at?: string; prerelease: boolean };
  update_available: boolean;
  checked_at?: string;
  check_error?: string;
  can_install: boolean;
  install_blocked?: string;
  auto_install_held?: string;
  platform: string;
  phase: 'idle' | 'downloading' | 'verifying' | 'installing' | 'restarting' | 'failed';
  phase_error?: string;
  installing?: string;
  last_outcome?: { result: 'updated' | 'rolled_back'; from: string; to: string; detail?: string; at: string };
  settings: UpdateSettings;
}

const STATUS_KEY = ['system', 'updates'];
const BUSY = new Set(['downloading', 'verifying', 'installing', 'restarting']);

const MODES: { value: UpdateMode; label: string; description: string }[] = [
  { value: 'automatic', label: 'Automatic', description: 'Installs new releases by itself and restarts. A new major version waits for you.' },
  { value: 'manual', label: 'Manual', description: 'Tells you here when a release is out. You choose when to install it.' },
  { value: 'off', label: 'Off', description: 'Never checks on its own. Use Check now when you want to know.' },
];

const INTERVALS = [
  { value: '6', label: 'Every 6 hours' },
  { value: '12', label: 'Every 12 hours' },
  { value: '24', label: 'Every day' },
  { value: '168', label: 'Every week' },
];

/** Whether an install this page started is still on its way to running. */
function waiting(pending: string | null, s?: UpdateStatus): boolean {
  return !!pending && s?.current !== pending && s?.phase !== 'failed';
}

function when(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function phaseText(phase: string, tag: string): string {
  switch (phase) {
    case 'downloading': return `Downloading ${tag}…`;
    case 'verifying': return `Checking ${tag} matches the published checksum…`;
    case 'installing': return `Installing ${tag}…`;
    default: return `Restarting into ${tag}…`;
  }
}

/**
 * Which version is running, whether a newer release is out, and how updates
 * are installed. Administrators only: installing restarts the whole instance.
 */
export function UpdatesPage() {
  const isAdmin = getSessionRole() === 'Administrator';
  const qc = useQueryClient();
  // The release being installed. Set when the install is accepted and kept
  // through the restart, when the status route is briefly unreachable.
  const [pending, setPending] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);

  const { data: status, isLoading, isError } = useQuery<UpdateStatus>({
    queryKey: STATUS_KEY,
    // Silent: while the server restarts this fails on purpose, and every
    // failed poll would otherwise be a red toast.
    queryFn: async () => (await apiFetch('/api/system/updates', { silent: true })).json(),
    enabled: isAdmin,
    refetchInterval: (q) => (waiting(pending, q.state.data) || BUSY.has(q.state.data?.phase ?? '') ? 2000 : false),
    retry: (_: number, err: Error): boolean =>
      waiting(pending, qc.getQueryData<UpdateStatus>(STATUS_KEY)) && (err as { status?: number }).status !== 403,
  });
  const justUpdated = !!pending && status?.current === pending;

  const check = useMutation({
    mutationFn: async () => (await apiFetch('/api/system/updates/check', { method: 'POST' })).json() as Promise<UpdateStatus>,
    onSuccess: (s) => qc.setQueryData(STATUS_KEY, s),
  });

  const install = useMutation({
    mutationFn: async (version: string) => (await apiFetch('/api/system/updates/install', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ version }),
    })).json() as Promise<UpdateStatus>,
    onSuccess: (s, version) => {
      setConfirming(false);
      setPending(version);
      qc.setQueryData(STATUS_KEY, s);
    },
  });

  if (!isAdmin) {
    return (
      <>
        <PageHeader title="Updates" back={{ to: '/settings', label: 'Settings' }} />
        <Alert maw={720} color="gray" title="Updates are managed by administrators">
          Ask an administrator of this Hermod to check for and install new releases.
        </Alert>
      </>
    );
  }

  const busy = waiting(pending, status) || BUSY.has(status?.phase ?? '');
  const busyTag = pending || status?.installing || status?.latest?.tag || '';

  return (
    <>
      <PageHeader
        title="Updates"
        lede="Which version is running, whether a newer one is out, and how updates are installed."
        back={{ to: '/settings', label: 'Settings' }}
        actions={
          <Button variant="default" leftSection={<IconRefresh size="1rem" />} loading={check.isPending} disabled={busy || !status} onClick={() => check.mutate()}>
            Check now
          </Button>
        }
      />
      <Stack gap="lg" maw={720}>
        {isLoading && <Group><Loader size="sm" /><Text size="sm" c="dimmed">Loading…</Text></Group>}
        {isError && !status && <Alert color="red" icon={<IconAlertTriangle size="1rem" />}>Could not load the update status.</Alert>}

        {status && (
          <Paper withBorder radius="md" p="lg" component="section" aria-labelledby="updates-status">
            <Stack gap="md">
              <Group gap="xs" wrap="wrap">
                <Text size="sm" fw={500}>Running</Text>
                <Text size="sm">{status.current || status.build}</Text>
                <Text size="sm" c="dimmed">· {status.platform}</Text>
              </Group>

              {justUpdated && (
                <Alert color="teal" icon={<IconCircleCheck size="1rem" />} title={`Updated to ${pending}`}>
                  Hermod restarted into the new version.
                </Alert>
              )}
              <Outcome status={status} />

              {busy ? (
                <Group component="output" gap="sm" wrap="nowrap">
                  <Loader size="sm" />
                  <Text size="sm">{phaseText(pending && !BUSY.has(status.phase) ? 'restarting' : status.phase, busyTag)}</Text>
                </Group>
              ) : status.phase === 'failed' ? (
                <Alert color="red" icon={<IconAlertTriangle size="1rem" />} title="The update failed">
                  Nothing was changed; Hermod is still running {status.current}. {status.phase_error}
                </Alert>
              ) : null}

              {status.check_error && (
                <Alert color="yellow" icon={<IconAlertTriangle size="1rem" />} title="Could not check for updates">
                  {status.check_error}
                </Alert>
              )}

              {!busy && status.update_available && status.latest ? (
                <Stack gap="sm">
                  <Group gap="xs" wrap="wrap">
                    <Title order={2} size="h4" id="updates-status">Hermod {status.latest.tag} is available</Title>
                    {status.latest.prerelease && <Badge variant="light" color="orange" tt="none">Pre-release</Badge>}
                  </Group>
                  <Group gap="md" wrap="wrap">
                    {status.latest.published_at && <Text size="sm" c="dimmed">Published {when(status.latest.published_at)}</Text>}
                    {status.latest.url && (
                      <Anchor href={status.latest.url} target="_blank" rel="noreferrer" size="sm">
                        <Group gap={4} wrap="nowrap" component="span">Release notes <IconExternalLink size="0.85rem" aria-hidden /></Group>
                      </Anchor>
                    )}
                  </Group>
                  {status.latest.notes && (
                    <Spoiler maxHeight={96} showLabel="Show more" hideLabel="Show less">
                      <Text size="sm" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{status.latest.notes}</Text>
                    </Spoiler>
                  )}
                  {status.can_install ? (
                    <Group>
                      <Button leftSection={<IconDownload size="1rem" />} onClick={() => setConfirming(true)}>Install {status.latest.tag}</Button>
                    </Group>
                  ) : (
                    <Alert color="gray" title="This installation cannot update itself">{status.install_blocked}</Alert>
                  )}
                  {status.settings.mode === 'automatic' && status.auto_install_held && (
                    <Text size="sm" c="dimmed">{status.auto_install_held}</Text>
                  )}
                </Stack>
              ) : !busy && !status.check_error ? (
                <Group gap="xs" wrap="nowrap">
                  {status.checked_at && status.current && <IconCircleCheck size="1.2rem" color="var(--mantine-color-teal-6)" aria-hidden />}
                  <Stack gap={0}>
                    <Title order={2} size="h4" id="updates-status">
                      {!status.current ? 'This is a development build' : status.checked_at ? 'You are on the newest release' : 'Not checked yet'}
                    </Title>
                    {!status.current && <Text size="sm" c="dimmed">It has no release version, so it is not compared with published releases.</Text>}
                    {status.checked_at && status.current && <Text size="sm" c="dimmed">Last checked {when(status.checked_at)}</Text>}
                  </Stack>
                </Group>
              ) : null}
            </Stack>
          </Paper>
        )}

        {status && <SettingsCard key={JSON.stringify(status.settings)} status={status} />}
      </Stack>

      <Modal opened={confirming} onClose={() => setConfirming(false)} title={`Install Hermod ${status?.latest?.tag ?? ''}?`}>
        <Stack>
          <Text size="sm">
            Hermod downloads the release, checks it against its published checksum, and restarts into it. It is
            unavailable for the few seconds the restart takes; flows stop gracefully and start again with it.
          </Text>
          <Text size="sm">
            If the new version cannot stay up, the previous one is put back by itself after three failed starts.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setConfirming(false)}>Cancel</Button>
            <Button loading={install.isPending} onClick={() => status?.latest && install.mutate(status.latest.tag)}>Install and restart</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

/** How the last update on this machine ended, when it is worth saying. */
function Outcome({ status }: { status: UpdateStatus }) {
  const o = status.last_outcome;
  if (!o) return null;
  if (o.result === 'rolled_back') {
    return (
      <Alert color="orange" icon={<IconArrowBackUp size="1rem" />} title={`${o.to} was rolled back to ${o.from || 'the previous version'}`}>
        {o.detail} {when(o.at) && `(${when(o.at)})`}
      </Alert>
    );
  }
  if (o.to !== status.current) return null;
  return <Text size="sm" c="dimmed">Updated from {o.from} on {when(o.at)}.</Text>;
}

function SettingsCard({ status }: { status: UpdateStatus }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<UpdateSettings>(status.settings);
  const changed = JSON.stringify(draft) !== JSON.stringify(status.settings);

  const save = useMutation({
    mutationFn: async (s: UpdateSettings) => (await apiFetch('/api/system/updates/settings', {
      method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(s),
    })).json() as Promise<UpdateSettings>,
    onSuccess: (saved) => {
      qc.setQueryData<UpdateStatus>(STATUS_KEY, (prev) => (prev ? { ...prev, settings: saved } : prev));
      notifications.show({ color: 'teal', message: 'Update settings saved.' });
    },
  });

  return (
    <Paper withBorder radius="md" p="lg" component="section" aria-labelledby="updates-settings">
      <Stack gap="md">
        <Title order={2} size="h4" id="updates-settings">How updates are installed</Title>
        <Radio.Group value={draft.mode} onChange={(v) => setDraft({ ...draft, mode: v as UpdateMode })} aria-labelledby="updates-settings">
          <SimpleGrid cols={{ base: 1, sm: 3 }}>
            {MODES.map((m) => {
              const disabled = m.value === 'automatic' && !status.can_install;
              return (
                <Radio.Card key={m.value} value={m.value} radius="md" p="sm" disabled={disabled} aria-label={m.label}
                  style={disabled ? { opacity: 0.6, cursor: 'not-allowed' } : undefined}>
                  <Group wrap="nowrap" align="flex-start" gap="sm">
                    <Radio.Indicator mt={2} disabled={disabled} />
                    <Stack gap={4}>
                      <Text fw={600} size="sm">{m.label}</Text>
                      <Text size="xs" c="dimmed">{m.description}</Text>
                    </Stack>
                  </Group>
                </Radio.Card>
              );
            })}
          </SimpleGrid>
        </Radio.Group>
        {!status.can_install && (
          <Text size="xs" c="dimmed">Automatic is unavailable because this installation cannot update itself.</Text>
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <Select
            label="Check for new releases"
            data={INTERVALS}
            value={String(draft.check_interval_hours)}
            onChange={(v) => v && setDraft({ ...draft, check_interval_hours: Number(v) })}
            disabled={draft.mode === 'off'}
            allowDeselect={false}
          />
          <Switch
            mt={{ base: 0, sm: 30 }}
            label="Include pre-releases"
            description="Release candidates, before they are final"
            checked={draft.include_prereleases}
            onChange={(e) => setDraft({ ...draft, include_prereleases: e.currentTarget.checked })}
          />
        </SimpleGrid>
        <Group justify="flex-end">
          <Button disabled={!changed} loading={save.isPending} onClick={() => save.mutate(draft)}>Save</Button>
        </Group>
      </Stack>
    </Paper>
  );
}
