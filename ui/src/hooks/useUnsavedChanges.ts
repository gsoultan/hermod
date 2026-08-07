import { useEffect } from 'react'

/**
 * Warns before the user loses unsaved edits by closing or reloading the tab.
 *
 * Nothing guarded this before, so navigating away from a half-filled form —
 * Settings alone holds dozens of fields — discarded everything silently. The
 * browser-level guard covers tab close, reload and back to an external page;
 * in-app navigation is guarded separately by the router (see useBlocker in
 * TanStack Router) where a form opts in.
 *
 * @param when Whether there are unsaved changes right now.
 */
export function useUnsavedChanges(when: boolean) {
  useEffect(() => {
    if (!when) return

    const handler = (e: BeforeUnloadEvent) => {
      // Browsers ignore custom text now and show their own prompt; setting
      // returnValue is what actually triggers it.
      e.preventDefault()
      e.returnValue = ''
      return ''
    }

    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [when])
}

/**
 * Tracks whether a value has diverged from its last-saved snapshot.
 *
 * Comparison is structural rather than by reference, because form state is
 * rebuilt on every keystroke and a reference check would report "dirty" even
 * after the user typed a character and deleted it again.
 */
export function isDirty(current: unknown, saved: unknown): boolean {
  if (current === saved) return false
  try {
    return JSON.stringify(current) !== JSON.stringify(saved)
  } catch {
    // Non-serialisable values (File handles, cyclic refs) fall back to
    // reference identity, which we already know differs.
    return true
  }
}
