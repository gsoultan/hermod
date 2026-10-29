import type { Tone } from '@/components/rows';

/**
 * What a connection row says, as plain functions: the state from the live
 * feed and the saved record, and the one line of detail under the name.
 */

export type ConnectionKind = 'source' | 'sink';

interface Saved { active?: boolean; status?: string; type: string; vhost?: string; worker_id?: string; workspace_id?: string }

/**
 * The live feed reports per workflow. A source is named by source_id; a sink
 * is named by sink_id, or listed under sink_statuses when a workflow writes
 * to several. The worst word wins, so one failing workflow shows.
 */
export function liveStatusFor(liveStatuses: Record<string, any>, kind: ConnectionKind, id: string): string | null {
  const found: string[] = [];
  for (const s of Object.values(liveStatuses || {})) {
    if (!s) continue;
    if (s[`${kind}_id`] === id && s[`${kind}_status`]) found.push(s[`${kind}_status`]);
    const many = s[`${kind}_statuses`];
    if (many && many[id]) found.push(many[id]);
  }
  if (found.length === 0) return null;
  for (const word of ['error', 'reconnecting', 'connecting', 'running']) {
    if (found.some((f) => f === word)) return word;
  }
  return found[0];
}

export function connectionState(saved: Saved, live: string | null): { tone: Tone; label: string } {
  const status = live || saved.status;
  if (!saved.active && !live && !saved.status) return { tone: 'off', label: 'Offline' };
  if (status === 'running') return { tone: 'ok', label: 'Online' };
  if (status === 'error' || (status && status.startsWith('error'))) return { tone: 'err', label: 'Offline' };
  if (status === 'reconnecting') return { tone: 'warn', label: 'Reconnecting' };
  if (status === 'connecting') return { tone: 'warn', label: 'Connecting' };
  return { tone: 'ok', label: status ? status.charAt(0).toUpperCase() + status.slice(1) : 'Online' };
}

/** "postgresql · prod · worker-eu · Sales ops": what it is, where it lives, who runs it. */
export function connectionDetail(saved: Saved, workerName?: string | null, workspaceName?: string | null): string {
  return [saved.type, saved.vhost || 'default', workerName, workspaceName].filter(Boolean).join(' · ');
}
