/**
 * What Save writes, reduced to one comparable string, so the editor can tell
 * a canvas with unsaved edits from one that still matches the server.
 * useWorkflowInitialization compares a refetched workflow against it to decide
 * whether a newer server version may replace the canvas.
 */

// The settings Save writes. Run state (active, status) is not among them: it
// changes underneath the editor without anyone editing anything.
const PERSISTED_SETTINGS = [
  'name', 'vhost', 'workerID', 'deadLetterSinkID', 'dlqThreshold', 'prioritizeDLQ',
  'maxRetries', 'retryInterval', 'reconnectInterval', 'schemaType', 'schema', 'tags',
  'dryRun', 'workspaceID', 'cpuRequest', 'memoryRequest', 'throughputRequest',
  'idleTimeout', 'tier', 'cron', 'retentionDays', 'traceSampleRate', 'traceRetention',
  'auditRetention',
] as const;

// Sorted keys: the server marshals config maps in key order, the editor keeps
// insertion order, and that difference is not an edit.
function stableJson(value: unknown): string {
  return JSON.stringify(value, (_key, v) =>
    v && typeof v === 'object' && !Array.isArray(v)
      ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, v[k]]))
      : v
  );
}

/**
 * persistedSnapshot is what Save would write, from store-shaped state: the
 * settings, each node's identity, position and config, and each edge's
 * wiring. React Flow's own fields (selected, measured, styling) are left out.
 */
export function persistedSnapshot(state: any): string {
  const settings: Record<string, unknown> = {};
  for (const key of PERSISTED_SETTINGS) settings[key] = state[key];
  return stableJson({
    settings,
    nodes: (state.nodes || []).map((n: any) => ({
      id: n.id, type: n.type, x: n.position?.x ?? 0, y: n.position?.y ?? 0, data: n.data,
    })),
    edges: (state.edges || []).map((e: any) => ({
      id: e.id, source: e.source, target: e.target,
      sourceHandle: e.sourceHandle ?? null, targetHandle: e.targetHandle ?? null, data: e.data ?? null,
    })),
  });
}
