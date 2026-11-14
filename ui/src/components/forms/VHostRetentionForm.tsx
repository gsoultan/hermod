import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Group, NumberInput, Stack, Text } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@/api';

interface VHostRetention {
  audit_log_days: number;
  workflow_versions: number;
  approval_days: number;
}

type Limit = number | '';

const MAX_DAYS = 3650;
const MAX_VERSIONS = 10000;

const toLimit = (n: number | undefined): Limit => (n && n > 0 ? n : '');
const fromLimit = (v: Limit): number => (typeof v === 'number' && v > 0 ? Math.floor(v) : 0);

/**
 * How long one vhost keeps its audit log, its workflows' saved versions and
 * its decided approvals. An empty box keeps everything. The hourly sweep
 * applies a saved limit; saving deletes nothing by itself.
 */
export function VHostRetentionForm({ vhost }: { vhost: string }) {
  const queryClient = useQueryClient();
  const url = `/api/vhosts/${encodeURIComponent(vhost)}/retention`;
  const [auditDays, setAuditDays] = useState<Limit>('');
  const [versions, setVersions] = useState<Limit>('');
  const [approvalDays, setApprovalDays] = useState<Limit>('');

  const { data, error, isPending } = useQuery<VHostRetention>({
    queryKey: ['vhost-retention', vhost],
    queryFn: async () => {
      const res = await apiFetch(url);
      if (!res.ok) throw new Error('Failed to read the retention');
      return res.json();
    },
  });

  // Hydrate once per vhost: a refetch after this form's own save must not
  // overwrite what has been typed since.
  const hydratedFor = useRef<string | null>(null);
  useEffect(() => {
    if (!data || hydratedFor.current === vhost) return;
    hydratedFor.current = vhost;
    setAuditDays(toLimit(data.audit_log_days));
    setVersions(toLimit(data.workflow_versions));
    setApprovalDays(toLimit(data.approval_days));
  }, [data, vhost]);

  const save = useMutation({
    mutationFn: async (retention: VHostRetention) => {
      const res = await apiFetch(url, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(retention),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || 'Failed to save the retention');
      }
      return res.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['vhost-retention', vhost] });
    },
  });

  if (error) {
    return <Alert color="red" variant="light">Could not read how long this virtual host keeps its history.</Alert>;
  }
  if (isPending) {
    return <Text size="sm" c="dimmed">Loading…</Text>;
  }

  const changed = () => save.reset();

  return (
    <Stack gap="md">
      <NumberInput
        label="Audit log, days"
        description="Entries about this virtual host's workflows, sources and sinks older than this are deleted."
        placeholder="Keep everything"
        min={0}
        max={MAX_DAYS}
        allowDecimal={false}
        allowNegative={false}
        value={auditDays}
        onChange={(v) => { changed(); setAuditDays(typeof v === 'number' ? v : ''); }}
      />
      <NumberInput
        label="Workflow versions, per workflow"
        description="Only the newest of each workflow's saved versions are kept."
        placeholder="Keep everything"
        min={0}
        max={MAX_VERSIONS}
        allowDecimal={false}
        allowNegative={false}
        value={versions}
        onChange={(v) => { changed(); setVersions(typeof v === 'number' ? v : ''); }}
      />
      <NumberInput
        label="Approvals, days after the decision"
        description="Approved and rejected approvals older than this are deleted. One still waiting is always kept."
        placeholder="Keep everything"
        min={0}
        max={MAX_DAYS}
        allowDecimal={false}
        allowNegative={false}
        value={approvalDays}
        onChange={(v) => { changed(); setApprovalDays(typeof v === 'number' ? v : ''); }}
      />
      <Text size="sm" c="dimmed">
        An empty box keeps everything. Limits are applied once an hour, and what they delete cannot be brought back.
      </Text>
      {save.error && <Alert color="red" variant="light">{save.error.message}</Alert>}
      <Group justify="flex-end">
        {save.isSuccess && <Text component="output" size="sm" c="dimmed">Saved</Text>}
        <Button
          loading={save.isPending}
          onClick={() => save.mutate({
            audit_log_days: fromLimit(auditDays),
            workflow_versions: fromLimit(versions),
            approval_days: fromLimit(approvalDays),
          })}
        >
          Save retention
        </Button>
      </Group>
    </Stack>
  );
}
