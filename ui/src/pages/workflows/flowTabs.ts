/**
 * The tabs of a flow, shared by the read-only view and the editor so the
 * two modes of the one page agree.
 */

export type FlowTab = 'build' | 'runs' | 'traces' | 'logs' | 'history' | 'debugger' | 'tuning';

export const FLOW_TABS: { value: FlowTab; label: string }[] = [
  { value: 'build', label: 'Build' },
  { value: 'runs', label: 'Runs' },
  { value: 'traces', label: 'Traces' },
  { value: 'logs', label: 'Logs' },
  { value: 'history', label: 'History' },
  { value: 'debugger', label: 'Debugger' },
  { value: 'tuning', label: 'Tuning' },
];

/** The detail page's old tab values, kept so a saved link still lands on the right tab. */
const LEGACY: Record<string, FlowTab> = {
  graph: 'build',
  executions: 'runs',
  proposals: 'tuning',
  opt: 'tuning',
  debug: 'debugger',
};

export function flowTab(value: string | null | undefined): FlowTab {
  if (!value) return 'build';
  if (FLOW_TABS.some((t) => t.value === value)) return value as FlowTab;
  return LEGACY[value] ?? 'build';
}
