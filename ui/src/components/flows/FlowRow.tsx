import { Switch } from '@mantine/core';
import { IconGitBranch } from '@tabler/icons-react';
import type { Workflow } from '@/types';
import { Row, StatePill, type Tone } from '@/components/rows';
import { normalizeWorkflowStatus } from '@/utils/workflowStatus';
import { isStopped } from '@/pages/home/homeModel';

interface FlowRowProps {
  flow: Workflow;
  /** Omit for a Viewer: the row then has no switch. */
  onToggle?: (flow: Workflow) => void;
  toggling?: boolean;
  /** One line under the name; defaults to the virtual host. */
  detail?: string;
}

/** State of a flow in a pill: Running, Stopped, or the engine's word for what is wrong. */
export function flowState(flow: Pick<Workflow, 'active' | 'status'>): { tone: Tone; label: string } {
  if (!flow.active) return { tone: 'off', label: 'Off' };
  if (isStopped(flow)) return { tone: 'err', label: 'Stopped' };
  const { label, color } = normalizeWorkflowStatus(flow.status);
  return { tone: color === 'green' ? 'ok' : color === 'gray' ? 'off' : 'warn', label };
}

/** A flow as one row: name, where it runs, its state, its switch, and the arrow into it. */
export function FlowRow({ flow, onToggle, toggling, detail }: FlowRowProps) {
  const state = flowState(flow);
  return (
    <Row
      icon={IconGitBranch}
      iconTone={state.tone === 'err' ? 'err' : 'default'}
      title={flow.name}
      detail={detail ?? (flow.vhost || 'Default virtual host')}
      state={<StatePill tone={state.tone}>{state.label}</StatePill>}
      action={onToggle && (
        <Switch
          checked={flow.active}
          disabled={toggling}
          onChange={() => onToggle(flow)}
          aria-label={flow.active ? `Turn off ${flow.name}` : `Turn on ${flow.name}`}
          size="md"
          color="green"
        />
      )}
      to="/workflows/$id"
      params={{ id: flow.id }}
    />
  );
}
