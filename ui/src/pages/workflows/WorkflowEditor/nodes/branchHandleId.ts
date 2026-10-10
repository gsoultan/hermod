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
