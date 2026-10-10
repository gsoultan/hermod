/**
 * Hands an unsaved workflow to the editor at /workflows/new.
 *
 * The natural-language builder answers with a workflow that is deliberately
 * not saved. Opening it must not save it either, so it travels to the editor
 * here rather than through the API: the builder stashes it, the editor takes
 * it once when it opens a new workflow, and the person saves it, or not.
 *
 * sessionStorage keeps the draft across the route change and a reload of the
 * same tab; the in-memory copy covers a browser where storage throws.
 */

const KEY = 'hermod_draft_workflow_v1'

let memory: unknown = null

export function stashDraftWorkflow(workflow: unknown): void {
  memory = workflow
  try {
    sessionStorage.setItem(KEY, JSON.stringify(workflow))
  } catch {
    // Storage blocked or full: the in-memory copy still reaches the editor.
  }
}

/**
 * The stashed draft, left in place, or null when there is none. Reading
 * without removing is what a render may do: React can run a state
 * initializer twice and keep either result.
 */
export function peekDraftWorkflow<T = any>(): T | null {
  let draft: unknown = memory
  try {
    const raw = sessionStorage.getItem(KEY)
    if (raw) draft = JSON.parse(raw)
  } catch {
    // Unreadable storage: fall back to the in-memory copy.
  }
  return (draft && typeof draft === 'object' ? draft : null) as T | null
}

/** Forgets the stashed draft, so the next new workflow starts blank. */
export function clearDraftWorkflow(): void {
  memory = null
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    // Nothing more to clear.
  }
}

/** The stashed draft, removed as it is read, or null when there is none. */
export function takeDraftWorkflow<T = any>(): T | null {
  const draft = peekDraftWorkflow<T>()
  clearDraftWorkflow()
  return draft
}
