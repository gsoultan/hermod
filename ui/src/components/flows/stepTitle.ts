/**
 * What a step is called on the canvas: its own label, else the name of the
 * connection it reads or writes, else what kind of step it is. Never its id,
 * which means nothing to the person reading the flow.
 */
export function stepTitle(data: { label?: unknown } | undefined, kind: string, connectionName?: string): string {
  const label = typeof data?.label === 'string' ? data.label.trim() : '';
  return label || connectionName?.trim() || kind;
}
