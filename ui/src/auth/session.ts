import { create } from 'zustand'

/**
 * Who is logged in.
 *
 * This replaces decoding the session JWT in the browser. The UI used to keep a
 * copy of the token in localStorage and unpack its claims to learn the current
 * user's role — which meant a 24-hour credential sat in storage that any XSS
 * could read, purely so the sidebar could decide whether to show an admin link.
 *
 * The identity is not the credential. The credential is the HttpOnly cookie the
 * browser holds and JavaScript cannot see; the identity comes from GET /api/me,
 * which the server answers using that cookie. Role and username are not secrets,
 * so keeping them in memory costs nothing if they leak.
 *
 * Hydrated once in the router's beforeLoad, before any component reads it, so
 * the synchronous accessors below are safe during render.
 */

export interface SessionUser {
  id: string
  username: string
  full_name?: string
  email?: string
  role: string
  vhosts?: string[]
}

type SessionStatus = 'unknown' | 'authenticated' | 'anonymous'

interface SessionState {
  user: SessionUser | null
  status: SessionStatus
  setUser: (user: SessionUser | null) => void
}

export const useSessionStore = create<SessionState>((set) => ({
  user: null,
  status: 'unknown',
  setUser: (user) => set({ user, status: user ? 'authenticated' : 'anonymous' }),
}))

/**
 * A single in-flight /api/me request shared by concurrent callers.
 *
 * beforeLoad runs per matched route, so a deep link can trigger several at once.
 * Without this they would each hit the endpoint.
 */
let inFlight: Promise<SessionUser | null> | null = null

async function fetchMe(): Promise<SessionUser | null> {
  try {
    const response = await fetch('/api/me', {
      // The HttpOnly session cookie is the credential.
      credentials: 'include',
      headers: { Accept: 'application/json' },
    })

    if (!response.ok) {
      // 401 is the normal "not logged in" answer, not an error worth throwing.
      useSessionStore.getState().setUser(null)
      return null
    }

    const user = (await response.json()) as SessionUser
    useSessionStore.getState().setUser(user)
    return user
  } catch {
    // Network failure is not proof of being logged out, but the UI has nothing
    // to render a role from either. Treat it as anonymous and let the route
    // guard send the user to /login rather than showing a half-populated shell.
    useSessionStore.getState().setUser(null)
    return null
  }
}

/**
 * Resolves the current session, fetching it once and reusing it afterwards.
 * Call before anything reads the accessors below.
 */
export async function ensureSession(): Promise<SessionUser | null> {
  const { status, user } = useSessionStore.getState()
  if (status !== 'unknown') return user

  if (!inFlight) {
    inFlight = fetchMe().finally(() => {
      inFlight = null
    })
  }
  return inFlight
}

/** Re-fetches the session, ignoring what is already loaded. Use after login. */
export async function refreshSession(): Promise<SessionUser | null> {
  useSessionStore.setState({ status: 'unknown', user: null })
  return ensureSession()
}

/** Forgets the session. Call on logout and on any 401. */
export function clearSession(): void {
  useSessionStore.setState({ user: null, status: 'unknown' })
}

/**
 * The current user, or null. Synchronous: safe during render because the route
 * guard awaits ensureSession before any component mounts.
 */
export function getSessionUser(): SessionUser | null {
  return useSessionStore.getState().user
}

/** The current user's role, or null when nobody is logged in. */
export function getSessionRole(): string | null {
  return getSessionUser()?.role ?? null
}

/** True once the session has been resolved and somebody is logged in. */
export function isAuthenticated(): boolean {
  return useSessionStore.getState().status === 'authenticated'
}
