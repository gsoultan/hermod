### Login lockout: what it counts and how to lift it

**Rule.** 5 failed logins (wrong password *or* unknown username) for one
`username|clientIP` key lock that key for 15 minutes (`MaxLoginAttempts`,
`LoginLockoutDuration`, `LoginAttemptWindow` in `internal/api/handlers/common.go`;
logic in `internal/auth/transport/http/login_security.go`). Attempts made while
locked are not counted, so retrying does not extend the wait. A correct login
deletes the key.

**State is in memory only** (`Handler.LoginAttempts`, a `sync.Map`). A restart
clears every lockout; it is not shared between instances.

**Lifting it.** An admin password change (`PUT /api/users/{id}/password`) calls
`Handler.ClearLoginLockout(username)`, which removes the key for that username on
every IP and writes an audit entry. There is no separate "unlock" endpoint.

**Client IP.** `handlers.ClientIP` is the one place that reads
`X-Forwarded-For`, used by both the login lockout and the form rate limit. It
believes the header only when `RemoteAddr` is loopback/private/link-local, then
takes the rightmost non-private hop. Before this, the leftmost header value was
trusted from anyone, so a client could rotate the header to bypass the lockout.
A deployment whose proxy reaches Hermod from a *public* address will now key on
the proxy IP; still per username, so it cannot lock other users out.
