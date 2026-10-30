import { Autocomplete, Paper, Stack, Switch, Text } from '@mantine/core';
import { IconArrowsJoin2 } from '@tabler/icons-react';
import { useState, type FC } from 'react';
import { ReadinessCheck, sentence } from '@/components/shared/ReadinessCheck';
import { useTwoPhaseCapability } from '@/hooks/useTwoPhaseCapability';
import {
  commitTogetherProblems,
  membersOf,
  nodeName,
  TRANSACTION_KEY,
  transactionNames,
  transactionOf,
  type EditorNode,
} from '@/utils/commitTogether';
import { typeLabel } from '@/utils/txgroup';

export type CommitTogetherSectionProps = {
  /** The sink node being edited. */
  nodeId: string;
  /** Its config, as the editor's store holds it. */
  data: Record<string, any>;
  /** Every node of the workflow, to find the other members. */
  nodes: EditorNode[];
  updateNodeConfig: (nodeId: string, config: Record<string, any>) => void;
};

/**
 * "Commit together with other databases" on a sink node.
 *
 * A workflow that builds different rows for different databases has one sink
 * node per database; the nodes that name the same transaction commit as one.
 * The name is the node's, not the sink record's — the same record can serve
 * other workflows that commit alone — so it is written to the node config, the
 * key the server reads when it builds the workflow.
 *
 * Shown only on a database that can take part. The readiness lines repeat what
 * the server will check at save and at start, so a mistake is seen here first.
 */
export const CommitTogetherSection: FC<CommitTogetherSectionProps> = ({ nodeId, data: given, nodes, updateNodeConfig }) => {
  const capability = useTwoPhaseCapability();
  // The drawer hands over the node as it was when opened; the store's copy is
  // the one updateNodeConfig writes to, so read from it where it has the node.
  const data = nodes.find((n) => n.id === nodeId)?.data ?? given;
  // On with no name yet is a state only the editor knows: the server sees a
  // blank as off, so it lives here and not in the node config.
  const [turnedOn, setTurnedOn] = useState(false);
  const type = String(data?.type ?? '');
  if (!capability || !capability.types.includes(type)) return null;

  const name = transactionOf(data);
  const on = name !== '' || turnedOn;
  const others = membersOf(nodes, name).filter((n) => n.id !== nodeId);
  const names = transactionNames(nodes).filter((n) => n !== name);
  const problems = on ? commitTogetherProblems(nodes, name, capability.types) : [];
  const ready = on && problems.length === 0 && capability.logReady;

  const setOn = (next: boolean) => {
    setTurnedOn(next);
    if (next) {
      // Joining the workflow's only transaction is what someone turning this on
      // almost always means; with several, or none, they say which.
      const existing = transactionNames(nodes);
      if (existing.length === 1) updateNodeConfig(nodeId, { [TRANSACTION_KEY]: existing[0] });
    } else {
      updateNodeConfig(nodeId, { [TRANSACTION_KEY]: '' });
    }
  };

  return (
    <Paper withBorder p="md" radius="md" mb="md" data-testid="commit-together">
      <Stack gap="sm">
        <Switch
          label="Commit together with other databases"
          description="This database and the others in the same transaction get each event in one transaction. Either they all keep their rows, or none do."
          checked={on}
          onChange={(e) => setOn(e.currentTarget.checked)}
        />

        {!on && (
          <Text size="sm" c="dimmed">
            Off: this sink commits on its own, as it does today.
          </Text>
        )}

        {on && (
          <>
            <Autocomplete
              label="Transaction"
              description="Pick a name already in this workflow to join it, or type a new one."
              placeholder="orders"
              data={names}
              value={name}
              onChange={(value) => updateNodeConfig(nodeId, { [TRANSACTION_KEY]: value })}
              leftSection={<IconArrowsJoin2 size="1rem" />}
            />

            {name && (
              <Stack gap={4}>
                <Text size="sm" fw={600}>
                  In this transaction
                </Text>
                <Text size="sm">
                  {nodeName({ id: nodeId, data })} · {typeLabel(type)}{' '}
                  <Text span c="dimmed">
                    this node
                  </Text>
                </Text>
                {others.map((n) => (
                  <Text size="sm" key={n.id}>
                    {nodeName(n)} · {typeLabel(String(n.data?.type ?? ''))}
                  </Text>
                ))}
              </Stack>
            )}

            <Paper withBorder p="sm" radius="md">
              <Stack gap={6}>
                <Text size="sm" fw={600}>
                  {ready ? 'Ready to run' : 'Not ready yet'}
                </Text>
                <ReadinessCheck state={capability.logReady ? 'ok' : 'problem'}>
                  {capability.logReady
                    ? 'The transaction log is durable, so a crash mid-commit is finished on restart.'
                    : sentence(capability.logProblem || 'the transaction log is not durable here')}
                </ReadinessCheck>
                {problems.length === 0 ? (
                  <ReadinessCheck state="ok">
                    {others.length + 1} databases in “{name}”.
                  </ReadinessCheck>
                ) : (
                  problems.map((p) => (
                    <ReadinessCheck key={p.kind + (p.nodeId ?? '')} state="problem">
                      {p.message}
                    </ReadinessCheck>
                  ))
                )}
              </Stack>
            </Paper>
          </>
        )}
      </Stack>
    </Paper>
  );
};
