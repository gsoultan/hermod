import type { Tone } from '@/components/rows';

/**
 * The activity feed's wording, as plain functions: what the filter asks the
 * logs endpoint for, and how a log entry reads as a row.
 */

export type FeedView = 'everything' | 'problems' | 'warnings';

export interface FeedFilter {
  view: FeedView;
  search: string;
  page: number;
  workflowId?: string;
  level?: string | null;
  action?: string | null;
}

export const FEED_PAGE_SIZE = 30;

const VIEW_LEVEL: Record<FeedView, string | null> = { everything: null, problems: 'ERROR', warnings: 'WARN' };

/** The query string for GET /api/logs. The endpoint matches one level and one action exactly. */
export function feedParams(f: FeedFilter): string {
  let q = `page=${f.page}&limit=${FEED_PAGE_SIZE}&search=${encodeURIComponent(f.search)}`;
  if (f.workflowId) q += `&workflow_id=${encodeURIComponent(f.workflowId)}`;
  const level = f.level || VIEW_LEVEL[f.view];
  if (level) q += `&level=${level}`;
  if (f.action) q += `&action=${f.action}`;
  return q;
}

interface LogLike { level?: string; action?: string; message?: string; username?: string; workflow_id?: string; source_id?: string; sink_id?: string }

export function logTone(log: LogLike): Tone {
  if (log.level === 'ERROR') return 'err';
  if (log.level === 'WARN') return 'warn';
  if (log.action) return 'ok';
  if (log.level === 'DEBUG') return 'off';
  return 'info';
}

export interface Names { workflows: Record<string, string>; sources: Record<string, string>; sinks: Record<string, string> }

/** The flow, else the source, else the sink the entry is about; a deleted one keeps a short id. */
export function logTitle(log: LogLike, names: Names): string {
  if (log.workflow_id && names.workflows[log.workflow_id]) return names.workflows[log.workflow_id];
  // An audited change to something else (a login, a virtual host) carries that
  // thing's id in workflow_id, so it reads as whoever made it.
  if (log.action && !log.source_id && !log.sink_id) return log.username || 'Hermod';
  if (log.workflow_id) return `Flow ${log.workflow_id.slice(0, 8)}`;
  if (log.source_id) return names.sources[log.source_id] || `Source ${log.source_id.slice(0, 8)}`;
  if (log.sink_id) return names.sinks[log.sink_id] || `Sink ${log.sink_id.slice(0, 8)}`;
  return 'Hermod';
}

/** Whether the entry is about a flow that still exists, so it can link there. */
export function knownFlow(log: LogLike, names: Names): boolean {
  return Boolean(log.workflow_id && names.workflows[log.workflow_id]);
}

export function logDetail(log: LogLike): string {
  const message = log.message || '';
  return log.action && log.username ? `${message} · by ${log.username}` : message;
}
