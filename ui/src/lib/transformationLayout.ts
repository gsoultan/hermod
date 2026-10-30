// Field mappings are rows of long paths and JSON values, and a SQL statement
// is an editor; they get the wider middle column even when the side panels
// are showing. The Redis steps take keys such as customer:{{.after.id}} and
// secret references, which the narrow column cut to a few characters.
const WIDE_CONFIG = new Set(['set', 'advanced', 'execute_sql', 'redis_lookup', 'redis_set'])

/** The transformation editor's middle column span, in twelfths, on a large screen. */
export function configColumnSpan(transType: string): 5 | 6 {
  return WIDE_CONFIG.has(transType) ? 6 : 5
}
