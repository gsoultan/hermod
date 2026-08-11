import { Alert, Anchor, List, MultiSelect, Stack, Text, TextInput } from '@mantine/core';
import { IconAlertTriangle, IconInfoCircle } from '@tabler/icons-react';
import { useEffect, useState, type FC } from 'react';
import { apiFetch } from '@/api';
import {
  eligibleSinks,
  formatMembers,
  ineligibleSinks,
  parseMembers,
  validateTxGroup,
  type SinkOption,
} from '@/utils/txgroup';

export type TxGroupSinkConfigProps = {
  config: any;
  sinks: any[];
  currentSinkId?: string;
  updateConfig: (key: string, value: any) => void;
};

/**
 * Transactional group: write to several sinks under one two-phase commit, so a
 * message lands in all of them or none.
 *
 * The picker only offers sinks that can actually take part, and the list of
 * those comes from the backend rather than being written down here. Keeping a
 * copy in the browser would mean two lists that must agree, and the cost of them
 * disagreeing is a group that saves cleanly and refuses to start when the
 * workflow next runs.
 */
export const TxGroupSinkConfig: FC<TxGroupSinkConfigProps> = ({
  config,
  sinks,
  currentSinkId,
  updateConfig,
}) => {
  const [capableTypes, setCapableTypes] = useState<string[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    apiFetch('/api/sinks/capabilities/two-phase', { silent: true })
      .then((res: any) => (res && typeof res.json === 'function' ? res.json() : res))
      .then((data: any) => {
        if (!cancelled) setCapableTypes(Array.isArray(data?.types) ? data.types : []);
      })
      .catch(() => {
        // Offer nothing rather than guess. An empty picker with the note below
        // is honest; a picker built from a hardcoded guess is how someone ends
        // up with a group that cannot start.
        if (!cancelled) setCapableTypes([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const known: SinkOption[] = (sinks || []).map((s: any) => ({
    id: s.id,
    name: s.name,
    type: s.type,
  }));

  const types = capableTypes ?? [];
  const available = eligibleSinks(known, types, currentSinkId);
  const excluded = ineligibleSinks(known, types, currentSinkId);
  const members = parseMembers(config?.members);
  const problems = capableTypes === null ? [] : validateTxGroup(config || {}, known);
  const memberProblem = problems.find((p) => p.field === 'members');
  const ageProblem = problems.find((p) => p.field === 'max_prepared_age');

  return (
    <Stack gap="sm">
      <Alert icon={<IconInfoCircle size="1rem" />} color="blue">
        A transactional group writes each message to every member under one two-phase commit:
        either all of them apply it or none do. Use it when a partial write is worse than no
        write at all.
      </Alert>

      <MultiSelect
        label="Members"
        placeholder={
          capableTypes === null
            ? 'Loading…'
            : available.length === 0
              ? 'No eligible sinks'
              : 'Select at least two sinks'
        }
        data={available.map((s) => ({ value: s.id, label: `${s.name} (${s.type})` }))}
        value={members}
        onChange={(val) => updateConfig('members', formatMembers(val))}
        error={memberProblem?.message}
        searchable
        required
        description="Every member takes part in the same transaction. Order does not matter."
      />

      {capableTypes !== null && excluded.length > 0 && (
        <Text size="xs" c="dimmed">
          Not listed:{' '}
          {excluded.map((s) => `${s.name} (${s.type})`).join(', ')}. Only{' '}
          {types.length > 0 ? types.join(' and ') : 'certain'} sinks can take part in two-phase
          commit — the rest have no way to prepare a write and commit it later.
        </Text>
      )}

      <TextInput
        label="Maximum prepared age"
        placeholder="15m"
        value={config?.max_prepared_age || ''}
        onChange={(e) => updateConfig('max_prepared_age', e.currentTarget.value)}
        error={ageProblem?.message}
        description="How long a transaction may sit in doubt before the reaper rolls it back. Leave empty for the default."
      />

      <Alert icon={<IconAlertTriangle size="1rem" />} color="orange" title="Before you use this">
        <List size="sm" spacing={4}>
          <List.Item>
            PostgreSQL needs <Text span ff="monospace">max_prepared_transactions</Text> above 0. It
            defaults to 0, and changing it requires a server restart. The group refuses to start
            otherwise, which is the safe failure.
          </List.Item>
          <List.Item>
            A prepared transaction holds its locks and blocks <Text span ff="monospace">VACUUM</Text>{' '}
            until it is resolved. That is why the reaper exists, and why the age above matters.
          </List.Item>
          <List.Item>
            PgBouncer in transaction pooling mode cannot carry a prepared transaction. Connect
            members directly, or use session pooling.
          </List.Item>
        </List>
        <Anchor
          href="https://github.com/gsoultan/Hermod#two-phase-commit-operational-hazard"
          target="_blank"
          size="sm"
        >
          Operational notes
        </Anchor>
      </Alert>
    </Stack>
  );
};
