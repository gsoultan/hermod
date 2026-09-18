/**
 * The `column.<path>` half of a `set` / `advanced` node's config.
 *
 * These nodes store their field mappings as flat keys on the node config, so
 * the editor's row order *is* JavaScript object key order and every edit has to
 * preserve it deliberately. Rebuilding the object by spreading the survivors
 * and appending the edited key -- the obvious way to write a rename -- sends
 * the edited row to the bottom of the list on every keystroke.
 *
 * Kept separate from the editor so the ordering and collision rules can be
 * asserted without a render, and so the raw-JSON pane (useColumnFields.ts) and
 * the row editor cannot drift apart on what counts as a column key.
 */

export const COLUMN_PREFIX = 'column.'

export interface ColumnField {
  /** The config key, e.g. `column.user.id`. */
  fullKey: string
  /** The key without the prefix, e.g. `user.id`. May be empty. */
  path: string
  value: unknown
}

type Config = Record<string, unknown>

export function isColumnKey(key: string): boolean {
  return key.startsWith(COLUMN_PREFIX)
}

/** The column entries of a config, in config order. */
export function listColumnFields(config: Config | undefined): ColumnField[] {
  return Object.entries(config ?? {})
    .filter(([k]) => isColumnKey(k))
    .map(([k, value]) => ({ fullKey: k, path: k.slice(COLUMN_PREFIX.length), value }))
}

/**
 * Renames one column key *in place*, returning the whole config so the caller
 * can commit it with `replace`.
 *
 * Returns null when there is nothing to commit: the path is unchanged, the key
 * is not there, or another row already holds the target path. A collision is
 * not a rename — the object can only hold the key once, so writing it would
 * silently drop one of the two rows along with its value. The caller keeps the
 * typed text on screen and tells the user instead.
 */
export function renameColumnField(
  config: Config | undefined,
  oldFullKey: string,
  newPath: string,
): Config | null {
  const source = config ?? {}
  const newFullKey = `${COLUMN_PREFIX}${newPath}`
  if (newFullKey === oldFullKey) return null
  if (!(oldFullKey in source)) return null
  if (newFullKey in source) return null

  const next: Config = {}
  for (const [k, v] of Object.entries(source)) {
    if (k === oldFullKey) next[newFullKey] = v
    else next[k] = v
  }
  return next
}

/** Drops one column key, leaving everything else in place and in order. */
export function removeColumnField(config: Config | undefined, fullKey: string): Config {
  return Object.fromEntries(Object.entries(config ?? {}).filter(([k]) => k !== fullKey))
}

/**
 * The name "Add Field" gives a new row.
 *
 * It used to be `new_field_${count}`. Delete a middle row and the count points
 * at a name that is still taken, so the new key merged into the existing row:
 * the button appeared to do nothing and quietly overwrote that row's value.
 */
export function nextColumnFieldName(config: Config | undefined): string {
  const taken = new Set(listColumnFields(config).map((f) => f.path))
  let n = 0
  while (taken.has(`new_field_${n}`)) n += 1
  return `new_field_${n}`
}
