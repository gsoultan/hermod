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
