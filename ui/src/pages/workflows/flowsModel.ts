import type { Workflow } from '@/types';
import { isStopped } from '@/pages/home/homeModel';

/** The words on a Flows row and the one filter, as plain functions. */

type NodeLike = { type?: string; data?: Record<string, unknown> };

const nodeName = (n: NodeLike) => String(n.data?.label || n.data?.name || 'Untitled');

/** "orders-db → snowflake": the first source and every sink, by their labels on the diagram. */
export function fromTo(wf: Pick<Workflow, 'nodes'>): string {
  const nodes: NodeLike[] = Array.isArray(wf.nodes) ? wf.nodes : [];
  if (nodes.length === 0) return 'No steps yet';
  const source = nodes.find((n) => n.type === 'source');
  const sinks = nodes.filter((n) => n.type === 'sink');
  const from = source ? nodeName(source) : 'no source yet';
  const to = sinks.length > 0 ? sinks.map(nodeName).join(', ') : 'no sink yet';
  return `${from} → ${to}`;
}

export type FlowFilter = 'all' | 'running' | 'problems' | 'off';

export const FLOW_FILTERS: { value: FlowFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'running', label: 'Running' },
  { value: 'problems', label: 'Problems' },
  { value: 'off', label: 'Off' },
];

export function matchesFilter(wf: Pick<Workflow, 'active' | 'status'>, filter: FlowFilter): boolean {
  switch (filter) {
    case 'all': return true;
    case 'off': return !wf.active;
    case 'problems': return isStopped(wf);
    case 'running': return wf.active && !isStopped(wf);
  }
}
