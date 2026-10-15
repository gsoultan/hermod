/**
 * Dead-letter mail sent by an earlier release linked to
 * `/workflows/<id>/edit?dlq=open`. A workflow's dead letters are on the Dead
 * Letters page now, so the editor sends that link on there.
 */

/** True when the address asks for the dead-letter queue. */
export function wantsDeadLetterQueue(search: string): boolean {
  return new URLSearchParams(search).get('dlq') === 'open'
}

/** The Dead Letters page, narrowed to one workflow. */
export function deadLetterPageFor(workflowID: string): string {
  return `/dead-letters?${new URLSearchParams({ workflow_id: workflowID })}`
}
