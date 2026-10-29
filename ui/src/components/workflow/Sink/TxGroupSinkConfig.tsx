import { Accordion, Anchor, Code, Group, List, MultiSelect, Paper, Stack, Text, ThemeIcon } from '@mantine/core';
import { TextInput } from '@/components/shared/ReferenceInputs';
import { IconAlertTriangle, IconCircleCheck, IconLoader2 } from '@tabler/icons-react';
import { useEffect, useState, type FC, type ReactNode } from 'react';
import { apiFetch } from '@/api';
import {
  eligibleSinks,
  formatMembers,
  ineligibleSinks,
  isValidPreparedAge,
  parseMembers,
  setupNotesFor,
  typeLabel,
  validateTxGroup,
  type SinkOption,
} from '@/utils/txgroup';

export type TxGroupSinkConfigProps = {
  config: any;
  /** Sinks the caller already has. Without them the form asks for the list. */
  sinks?: any[];
  currentSinkId?: string;
  updateConfig: (key: string, value: any) => void;
};

type Capability = { types: string[]; logReady: boolean; logProblem: string };

/** The JSON of a response, or the value itself when a test stubbed apiFetch. */
const readJSON = (res: any) => (res && typeof res.json === 'function' ? res.json() : res);

/**
 * Which sinks can take part, and whether a group could keep its transaction log
 * here. Both come from the backend, which enforces the same rules when the
 * workflow starts, so the form and the engine cannot disagree.
 */
function useCapability(): Capability | null {
  const [capability, setCapability] = useState<Capability | null>(null);
  useEffect(() => {
    let cancelled = false;
    apiFetch('/api/sinks/capabilities/two-phase', { silent: true })
      .then(readJSON)
      .then((data: any) => {
        if (cancelled) return;
        setCapability({
          types: Array.isArray(data?.types) ? data.types : [],
          // An older server does not report the log; say nothing rather than guess.
          logReady: data?.log_ready !== false,
          logProblem: typeof data?.log_problem === 'string' ? data.log_problem : '',
        });
      })
      .catch(() => {
        // Offer nothing rather than guess. A picker built from a hardcoded list
        // is how someone ends up with a group that cannot start.
        if (!cancelled) setCapability({ types: [], logReady: true, logProblem: '' });
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return capability;
}

/**
 * The stored sinks to choose members from. Neither the Sinks page nor the
 * workflow editor passes them down, which left the picker empty and every saved
 * member reported as deleted; so when none are given, the form asks.
 */
function useSinkOptions(given?: any[]): SinkOption[] | null {
  const [fetched, setFetched] = useState<SinkOption[] | null>(null);
  const useGiven = Array.isArray(given) && given.length > 0;
  useEffect(() => {
    if (useGiven) return;
    let cancelled = false;
    apiFetch('/api/sinks?limit=1000', { silent: true })
      .then(readJSON)
      .then((data: any) => {
        const rows = Array.isArray(data?.data) ? data.data : Array.isArray(data) ? data : [];
        if (!cancelled) setFetched(rows);
      })
      .catch(() => {
        if (!cancelled) setFetched([]);
      });
    return () => {
      cancelled = true;
    };
  }, [useGiven]);
  const rows = useGiven ? given! : fetched;
  return rows === null ? null : rows.map((s: any) => ({ id: s.id, name: s.name, type: s.type }));
}

/** The backend's reason as a sentence: capitalised, with a full stop. */
const sentence = (text: string) => {
  const t = text.trim();
  if (!t) return t;
  return t[0].toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? '' : '.');
};

/** One line of the readiness check. */
function Check({ state, children }: { state: 'ok' | 'problem' | 'waiting'; children: ReactNode }) {
  const icon =
    state === 'ok' ? (
      <ThemeIcon color="green" variant="light" size="sm" radius="xl">
        <IconCircleCheck size="0.9rem" />
      </ThemeIcon>
    ) : state === 'problem' ? (
      <ThemeIcon color="orange" variant="light" size="sm" radius="xl">
        <IconAlertTriangle size="0.9rem" />
      </ThemeIcon>
    ) : (
      <ThemeIcon color="gray" variant="light" size="sm" radius="xl">
        <IconLoader2 size="0.9rem" />
      </ThemeIcon>
    );
  return (
    <Group gap="xs" wrap="nowrap" align="flex-start">
      {icon}
      <Text size="sm" style={{ flex: 1 }}>
        {children}
      </Text>
    </Group>
  );
}

const POSTGRES_SETUP = (
  <Stack gap={4}>
    <Text size="sm" fw={600}>
      PostgreSQL / YugabyteDB
    </Text>
    <List size="sm" spacing={4}>
      <List.Item>
        Set <Code>max_prepared_transactions = 16</Code> (at least one per group) in{' '}
        <Code>postgresql.conf</Code> and restart. It defaults to 0, which makes every write fail.
      </List.Item>
      <List.Item>
        Connect directly, not through PgBouncer in transaction mode: it cannot finish a prepared
        transaction on the connection that started it.
      </List.Item>
    </List>
  </Stack>
);

const MYSQL_SETUP = (
  <Stack gap={4}>
    <Text size="sm" fw={600}>
      MySQL / MariaDB
    </Text>
    <List size="sm" spacing={4}>
      <List.Item>MySQL 5.7.7 or later (8.0.30+ if binary logging is on), or MariaDB 10.5.2 or later.</List.Item>
      <List.Item>
        On MySQL 8, let the sink's account see its own unfinished transactions:{' '}
        <Code>GRANT XA_RECOVER_ADMIN ON *.* TO 'hermod'@'%';</Code>
      </List.Item>
    </List>
  </Stack>
);

/**
 * Transactional group: write to several sinks under one two-phase commit, so a
 * message lands in all of them or none.
 *
 * The form answers the three questions someone setting one up has: which sinks,
 * will it run, and what must the databases allow. Everything else — the
 * prepared-age limit and the server set-up — is folded away until asked for.
 */
export const TxGroupSinkConfig: FC<TxGroupSinkConfigProps> = ({ config, sinks, currentSinkId, updateConfig }) => {
  const capability = useCapability();
  const known = useSinkOptions(sinks);
  const loading = capability === null || known === null;

  const types = capability?.types ?? [];
  const options = known ?? [];
  const available = eligibleSinks(options, types, currentSinkId);
  const hidden = ineligibleSinks(options, types, currentSinkId);
  const members = parseMembers(config?.members);
  const problems = loading ? [] : validateTxGroup(config || {}, options);
  const memberProblem = problems.find((p) => p.field === 'members');
  const ageOK = isValidPreparedAge(config?.max_prepared_age ?? '');
  const ageProblem = problems.find((p) => p.field === 'max_prepared_age');
  // Folded away unless it is the thing that needs fixing.
  const [open, setOpen] = useState<string[]>(ageOK ? [] : ['advanced']);
  const notes = setupNotesFor(members, options);
  const logOK = !!capability?.logReady;
  const membersOK = members.length >= 2 && !memberProblem;

  // A saved member that has since been deleted is still a value; show it so it
  // can be removed rather than silently dropped from the picker.
  const data = [
    ...available.map((s) => ({ value: s.id, label: `${s.name} · ${typeLabel(s.type)}` })),
    ...members
      .filter((id) => !available.some((s) => s.id === id))
      .map((id) => ({ value: id, label: options.find((s) => s.id === id)?.name ?? `Missing sink ${id}` })),
  ];

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        Every message is written to all the sinks you choose, in one transaction: they all get it,
        or none of them do. Use it when a partial write is worse than a slower one.
      </Text>

      <MultiSelect
        label="Sinks to write to"
        placeholder={loading ? 'Loading…' : available.length === 0 ? 'No sink here can take part yet' : 'Choose two or more'}
        data={data}
        value={members}
        onChange={(val) => updateConfig('members', formatMembers(val))}
        error={memberProblem && members.length >= 2 ? memberProblem.message : undefined}
        searchable
        required
        description="PostgreSQL, YugabyteDB, MySQL and MariaDB sinks can be mixed. Order does not matter."
      />

      {!loading && hidden.length > 0 && (
        <Text size="xs" c="dimmed">
          {hidden.length} other {hidden.length === 1 ? 'sink is' : 'sinks are'} not listed (
          {hidden.map((s) => s.name).join(', ')}): only {types.map(typeLabel).join(', ')} sinks can hold a
          write open until every member agrees.
        </Text>
      )}

      <Paper withBorder p="sm" radius="md">
        <Stack gap={6}>
          <Text size="sm" fw={600}>
            {loading ? 'Checking…' : logOK && membersOK && ageOK ? 'Ready to run' : 'Not ready yet'}
          </Text>
          <Check state={loading ? 'waiting' : logOK ? 'ok' : 'problem'}>
            {loading ? (
              'Transaction log'
            ) : logOK ? (
              'Transaction log: kept in the state store, so a crash mid-write is recovered on restart.'
            ) : (
              <>
                {sentence(capability!.logProblem)}{' '}
                <Anchor href="/settings?tab=platform" size="sm">
                  Open Settings
                </Anchor>
              </>
            )}
          </Check>
          <Check state={loading ? 'waiting' : membersOK ? 'ok' : 'problem'}>
            {members.length < 2
              ? `Choose at least two sinks (${members.length} selected).`
              : memberProblem
                ? memberProblem.message
                : `${members.length} sinks selected.`}
          </Check>
          {!ageOK && (
            <Check state="problem">The limit under Advanced is not a duration such as 15m or 1h.</Check>
          )}
          <Text size="xs" c="dimmed">
            Test Connection asks each database whether it accepts two-phase commit.
          </Text>
        </Stack>
      </Paper>

      {/* Unmounted when folded, as the FCM form's sections are. */}
      <Accordion multiple variant="contained" radius="md" keepMounted={false} value={open} onChange={setOpen}>
        {notes.length > 0 && (
          <Accordion.Item value="setup">
            <Accordion.Control>Database setup</Accordion.Control>
            <Accordion.Panel>
              <Stack gap="sm">
                {notes.includes('postgres') && POSTGRES_SETUP}
                {notes.includes('mysql') && MYSQL_SETUP}
                <Text size="xs" c="dimmed">
                  Hermod checks these when the workflow starts and refuses to run if one is missing,
                  rather than risk a half-written batch.{' '}
                  <Anchor
                    href="https://github.com/gsoultan/hermod/blob/main/docs/transactional-sink-groups.md"
                    target="_blank"
                    size="xs"
                  >
                    Full operational notes
                  </Anchor>
                </Text>
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        )}
        <Accordion.Item value="advanced">
          <Accordion.Control>Advanced</Accordion.Control>
          <Accordion.Panel>
            <TextInput
              label="Give up on a stuck transaction after"
              placeholder="15m"
              value={config?.max_prepared_age || ''}
              onChange={(e) => updateConfig('max_prepared_age', e.currentTarget.value)}
              error={ageProblem?.message}
              description="If Hermod stops between the vote and the commit, the databases hold locks until it comes back. After this long they are rolled back. Leave empty for 15 minutes."
            />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  );
};
