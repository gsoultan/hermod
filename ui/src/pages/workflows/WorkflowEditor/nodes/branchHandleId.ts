/**
 * The id a router rule's or switch case's handle is drawn with: its label, or
 * `rule_<index>` / `case_<index>` when it has none.
 *
 * An edge drawn from the handle carries this id as its label, and the engine
 * sends a message down the edges labelled with the branch it chose, so the
 * engine's `routeBranch` must name the branch exactly this way. Both are tested
 * against internal/engine/registry/nodes/control/testdata/branch_names.json.
 */
export function branchHandleId(node: 'router' | 'switch', label: string | undefined, index: number): string {
  return label || `${node === 'router' ? 'rule' : 'case'}_${index}`;
}

/** The branch ai_classify takes when the model is unsure (genai.UnsureLabel). */
export const UNSURE_BRANCH = 'unsure';

export interface ClassifyLabel {
  label: string;
  description: string;
}

/**
 * An ai_classify node's labels, read the way genai.parseLabels reads them: a
 * list of `{label, description}` objects or names, a JSON list, or
 * comma-separated names. Names are trimmed and blank ones dropped.
 */
export function classifyLabels(raw: unknown): ClassifyLabel[] {
  if (typeof raw === 'string') {
    if (raw.trim().startsWith('[')) {
      try {
        return classifyLabels(JSON.parse(raw));
      } catch {
        return [];
      }
    }
    return raw
      .split(',')
      .map((p) => p.trim())
      .filter(Boolean)
      .map((label) => ({ label, description: '' }));
  }
  if (!Array.isArray(raw)) return [];
  const out: ClassifyLabel[] = [];
  for (const item of raw) {
    if (typeof item === 'string') {
      if (item.trim()) out.push({ label: item.trim(), description: '' });
    } else if (item && typeof item === 'object') {
      const label = typeof item.label === 'string' ? item.label.trim() : '';
      const description = typeof item.description === 'string' ? item.description : '';
      if (label) out.push({ label, description });
    }
  }
  return out;
}

/**
 * The ids of an ai_classify node's handles: each label as the engine names
 * the branch, then `unsure`. The node returns the chosen label itself as the
 * branch, so — as with branchHandleId — the handle id, which an edge drawn
 * from it carries as its label, must be exactly that string.
 */
export function classifyBranchIds(raw: unknown): string[] {
  const labels = new Set(classifyLabels(raw).map((l) => l.label));
  labels.delete(UNSURE_BRANCH);
  return [...labels, UNSURE_BRANCH];
}

/** A switch's cases or a router's rules, parsed the way the node renders them. */
function branchItems(raw: unknown): any[] {
  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw || '[]');
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  }
  return Array.isArray(raw) ? raw : [];
}

/**
 * Where each old case or rule went: its index in the new list, or -1 when it
 * was removed. The editor changes one item at a time, so a list one shorter
 * lost the first item that no longer lines up; otherwise items keep their
 * index.
 */
function branchMoves(before: any[], after: any[]): number[] {
  if (after.length === before.length - 1) {
    const same = (a: any, b: any) => JSON.stringify(a) === JSON.stringify(b);
    let removed = before.length - 1;
    for (let i = 0; i < after.length; i++) {
      if (!same(before[i], after[i])) {
        removed = i;
        break;
      }
    }
    return before.map((_, i) => (i < removed ? i : i === removed ? -1 : i - 1));
  }
  return before.map((_, i) => (i < after.length ? i : -1));
}

/**
 * Keeps a switch's or router's out-edges on the cases or rules they were drawn
 * from when those change.
 *
 * An edge's handle and label are the case's handle id, and the label is the
 * branch the engine sends a message down. Renaming a case changes its id, and
 * so does removing an unnamed case before it (case_2 becomes case_1). The edge
 * was left on the old id: the canvas no longer drew it, but it was saved, and
 * a message matching the case found no edge and stopped at the node. A
 * removed case's edges go with it.
 */
export function remapBranchEdges<E extends { source: string; sourceHandle?: string | null; data?: any }>(
  edges: E[],
  nodeId: string,
  node: 'router' | 'switch',
  rawBefore: unknown,
  rawAfter: unknown,
): E[] {
  const before = branchItems(rawBefore);
  const after = branchItems(rawAfter);
  const moves = branchMoves(before, after);
  const oldIds = before.map((item, i) => branchHandleId(node, item?.label, i));
  const renamed = new Map<string, string | null>();
  before.forEach((_, i) => {
    const oldId = oldIds[i];
    // Two cases with one name share a handle, and its edges cannot be told
    // apart; moving them would move the other case's too. Typing a new name
    // passes through such states, so they are left where they are.
    if (oldIds.indexOf(oldId) !== oldIds.lastIndexOf(oldId)) return;
    const to = moves[i];
    const newId = to < 0 ? null : branchHandleId(node, after[to]?.label, to);
    if (newId !== oldId) renamed.set(oldId, newId);
  });
  if (renamed.size === 0) return edges;

  const out: E[] = [];
  for (const e of edges) {
    if (e.source !== nodeId || !e.sourceHandle || !renamed.has(e.sourceHandle)) {
      out.push(e);
      continue;
    }
    const newId = renamed.get(e.sourceHandle);
    if (newId === null || newId === undefined) continue;
    out.push({ ...e, sourceHandle: newId, data: { ...(e.data || {}), label: newId } });
  }
  return out;
}
