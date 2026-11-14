/**
 * What a node's config may hold once saved. The server applies the same rule
 * (internal/storage/nodeclean) to everything it stores; doing it here too
 * keeps the request free of it and keeps the unsaved-changes check from
 * counting what Save leaves out.
 */

// The fields of a stored source or sink. On a source or sink node they are a
// copy of the connection, never a setting of the node: `config` holds its
// password, `state` its CDC cursor, `sample` a row from its database. The
// engine loads the connection by the node's ref_id.
const CONNECTION_COPY_KEYS = [
  'id', 'name', 'vhost', 'active', 'status', 'worker_id', 'workspace_id',
  'config', 'sample', 'state', 'created_at', 'updated_at',
];

// Never saved on any node: data the editor captured, and ref_id, which the
// node carries on its own.
const EDITOR_ONLY_KEYS = ['lastSample', 'testResult', 'ref_id'];

export function savedNodeConfig(node: { id?: string; type?: string; data?: Record<string, any> }): Record<string, any> {
  const out: Record<string, any> = { ...(node.data || {}) };
  if (node.type === 'source' || node.type === 'sink') {
    if (!out.label && typeof out.name === 'string' && out.name) out.label = out.name;
    for (const k of CONNECTION_COPY_KEYS) delete out[k];
  }
  for (const k of EDITOR_ONLY_KEYS) delete out[k];
  return out;
}
