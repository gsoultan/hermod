/**
 * The dead-letter mail Hermod sends through Panmail links to
 * `/workflows/<id>/edit?dlq=open`. These read that link and open the
 * dead-letter inspector on the workflow's dead-letter sink.
 */

/** A sink as the dead-letter inspector takes it. */
export interface DeadLetterInspectorSink {
  id: string
  name: string
  type: string
  config: Record<string, any>
}

/** True when the address asks for the dead-letter queue. */
export function wantsDeadLetterQueue(search: string): boolean {
  return new URLSearchParams(search).get('dlq') === 'open'
}

/**
 * Opens the inspector when the address asks for it and the workflow has a
 * dead-letter sink, and reports whether it did. A sink that cannot be read
 * leaves the editor as it is: the link is a shortcut, not a page of its own.
 */
export async function openDeadLetterQueueFromLink({ search, deadLetterSinkID, fetchSink, open }: {
  search: string
  deadLetterSinkID: string
  fetchSink: (id: string) => Promise<{ id: string; name: string; type: string; config?: Record<string, any> }>
  open: (sink: DeadLetterInspectorSink) => void
}): Promise<boolean> {
  if (!deadLetterSinkID || !wantsDeadLetterQueue(search)) return false
  try {
    const sink = await fetchSink(deadLetterSinkID)
    open({ id: sink.id, name: sink.name, type: sink.type, config: sink.config ?? {} })
    return true
  } catch {
    return false
  }
}
