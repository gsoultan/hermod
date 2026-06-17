// Normalizes raw workflow/engine status strings into a consistent, human-readable
// label and a Mantine color so the UI reflects the real-time backend engine state
// (e.g. running, reconnecting, restarting, stopped, error) coherently across pages.

export interface NormalizedStatus {
  label: string;
  color: string;
}

const STATUS_FALLBACK: NormalizedStatus = { label: 'Unknown', color: 'gray' };

export function normalizeWorkflowStatus(status?: string | null): NormalizedStatus {
  if (!status) return STATUS_FALLBACK;

  const normalized = status.trim().toLowerCase();
  if (normalized === '') return STATUS_FALLBACK;

  if (normalized === 'running' || normalized === 'active') {
    return { label: 'Running', color: 'green' };
  }
  if (normalized.startsWith('reconnecting')) {
    return { label: 'Reconnecting', color: 'orange' };
  }
  if (normalized === 'restarting') {
    return { label: 'Restarting', color: 'orange' };
  }
  if (normalized === 'connecting' || normalized === 'starting') {
    return { label: 'Connecting', color: 'orange' };
  }
  if (normalized === 'stopping') {
    return { label: 'Stopping', color: 'orange' };
  }
  if (normalized === 'stopped' || normalized === 'inactive') {
    return { label: 'Stopped', color: 'gray' };
  }
  if (normalized === 'paused') {
    return { label: 'Paused', color: 'yellow' };
  }
  if (normalized === 'completed' || normalized === 'complete') {
    return { label: 'Completed', color: 'blue' };
  }
  if (
    normalized.startsWith('error') ||
    normalized === 'failed' ||
    normalized.includes('circuit_breaker_open')
  ) {
    return { label: 'Error', color: 'red' };
  }

  // Fallback: title-case the raw status for display.
  const label = status
    .trim()
    .split(/[\s:_-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');

  return { label: label || STATUS_FALLBACK.label, color: 'blue' };
}
