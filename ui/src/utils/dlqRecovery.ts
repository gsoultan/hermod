// "Prioritize DLQ on startup" and the Drain DLQ button both read messages back
// out of the dead-letter sink, which only works for sink types Hermod can also
// open as a source.
//
// The editor used to answer this from a literal list of 25 sink types written
// into SidebarDrawer.tsx. It had drifted from the factory in both directions:
// four types it called recovery-capable are not sources at all, so the
// checkbox was offered and StartWorkflow then refused the workflow; and nine
// types that are both sink and source were missing, so the checkbox was
// disabled and the feature was unreachable for them. The list now comes from
// GET /api/sinks/capabilities/dlq-recovery, which the factory owns.

export interface DLQSinkLike {
  type?: string;
}

/**
 * Whether the editor should treat this dead-letter sink as drainable.
 *
 * `capableTypes` is null/undefined while the capability request is in flight.
 * That is reported as capable on purpose: flashing a warning and disabling the
 * checkbox for a moment on every page load is worse than being briefly
 * optimistic, and the engine validates the choice on start either way.
 */
export function dlqRecoverySupported(
  sink: DLQSinkLike | null | undefined,
  capableTypes: string[] | null | undefined,
): boolean {
  if (!sink) return true;
  if (!capableTypes) return true;
  return typeof sink.type === 'string' && capableTypes.includes(sink.type);
}
