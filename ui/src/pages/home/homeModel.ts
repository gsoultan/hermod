import type { DashboardStats } from '@/hooks/useDashboardStream';
import type { Workflow } from '@/types';
import { normalizeWorkflowStatus } from '@/utils/workflowStatus';
import type { Tone } from '@/components/rows';
import type { Waiting } from '@/lib/approvals';

/**
 * What Home says, kept as plain functions so the words can be tested
 * without rendering anything.
 */

export function greeting(now: Date, username?: string | null): string {
  const h = now.getHours();
  const part = h < 12 ? 'morning' : h < 18 ? 'afternoon' : 'evening';
  return username ? `Good ${part}, ${username}` : `Good ${part}`;
}

const perSecond = (mps: number) => {
  if (mps <= 0) return 'nothing moving';
  const n = mps >= 10 ? Math.round(mps) : Number(mps.toFixed(1));
  return `${n.toLocaleString('en-US')} messages a second`;
};

export function statusLine(stats: DashboardStats, mps: number): string {
  return `${stats.active_workflows} of ${stats.total_workflows} flows running · ${perSecond(mps)}`;
}

export interface AttentionItem {
  key: string;
  tone: Tone;
  title: string;
  detail: string;
  action: { label: string; to: string; params?: Record<string, string>; search?: Record<string, string> };
}

interface AttentionInput {
  workflows: Pick<Workflow, 'id' | 'name' | 'active' | 'status'>[];
  stats: DashboardStats;
  deadLettersQueued: number;
  /** What waits for this person's decision (lib/approvals). */
  waiting: Waiting;
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** A flow that is switched on but not running is stopped, whatever the engine calls it. */
export function isStopped(wf: Pick<Workflow, 'active' | 'status'>): boolean {
  if (!wf.active) return false;
  const { color } = normalizeWorkflowStatus(wf.status);
  return color === 'red';
}

/**
 * The short list at the top of Home: each entry is one thing a person can
 * do something about, with the one button that does it. Stopped flows come
 * first because a stopped flow is the reason most people open Hermod; what
 * waits for your decision comes next, because nothing moves until you decide.
 * Each kind of approval gets its own row, worded for what approving it does.
 */
export function attentionItems({ workflows, stats, deadLettersQueued, waiting }: AttentionInput): AttentionItem[] {
  const items: AttentionItem[] = [];
  for (const wf of workflows) {
    if (!isStopped(wf)) continue;
    items.push({
      key: `wf-${wf.id}`,
      tone: 'err',
      title: `${wf.name} stopped`,
      detail: normalizeWorkflowStatus(wf.status).label === 'Error'
        ? 'A step failed. Open the flow to see which, and why.'
        : `Reported as ${normalizeWorkflowStatus(wf.status).label.toLowerCase()}. Open the flow to see why.`,
      action: { label: 'Fix it', to: `/workflows/${wf.id}` },
    });
  }
  if (waiting.releases > 0) {
    items.push({
      key: 'releases',
      tone: 'info',
      title: `${plural(waiting.releases, 'release', 'releases')} waiting to go live`,
      detail: 'Flow versions sent from another environment. They go live here when you approve.',
      action: { label: 'Review', to: '/approvals', search: { tab: 'releases' } },
    });
  }
  if (waiting.requests > 0) {
    items.push({
      key: 'vhost-requests',
      tone: 'info',
      title: `${plural(waiting.requests, 'connected environment asks', 'connected environments ask')} to add a virtual host`,
      detail: 'Allow it and releases can be sent into that virtual host too.',
      action: { label: 'Review', to: '/approvals', search: { tab: 'releases' } },
    });
  }
  if (waiting.held > 0) {
    items.push({
      key: 'held',
      tone: 'info',
      title: `${plural(waiting.held, 'message', 'messages')} held for your approval`,
      detail: 'An Approval step in a running flow holds it until someone lets it continue.',
      action: { label: 'Review', to: '/approvals', search: { tab: 'held' } },
    });
  }
  if (deadLettersQueued > 0) {
    items.push({
      key: 'dead-letters',
      tone: 'err',
      title: `${plural(deadLettersQueued, 'message is', 'messages are')} waiting in Dead letters`,
      detail: 'Each one failed at a step or a sink. Retry sends them again from where they failed.',
      action: { label: 'Retry them', to: '/activity', search: { tab: 'dead-letters' } },
    });
  }
  if (stats.circuit_breakers_open > 0) {
    items.push({
      key: 'breakers',
      tone: 'err',
      title: `${plural(stats.circuit_breakers_open, 'sink', 'sinks')} stopped accepting writes`,
      detail: 'The circuit breaker opened after repeated failures. It closes again on its own once the sink answers.',
      action: { label: 'See which', to: '/activity', search: { tab: 'health' } },
    });
  }
  if (stats.backpressure >= 0.8) {
    items.push({
      key: 'backpressure',
      tone: 'warn',
      title: `A sink buffer is ${Math.round(stats.backpressure * 100)}% full`,
      detail: 'The sink is slower than its source. Messages queue up until it catches up.',
      action: { label: 'See which', to: '/activity', search: { tab: 'health' } },
    });
  }
  return items;
}

/** Nothing connected and no flow yet: Home turns into the three-step start. */
export function isFirstRun(stats: DashboardStats, workflows: unknown[]): boolean {
  return workflows.length === 0 && stats.total_sources === 0 && stats.total_sinks === 0 && stats.total_workflows === 0;
}
