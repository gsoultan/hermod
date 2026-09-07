// Where the local dev stack is listening.
//
// scripts/dev.sh prefers 4005/50051/5175 but steps over whichever is taken, so
// the ports are not knowable statically. It records what it chose in
// .dev/.ports, and that file — not an environment variable — is what makes the
// answer available to a *later* process: CI starts the stack and runs Playwright
// in separate steps, and an export does not survive between them.
//
// Resolution order, most specific first:
//   1. an explicit base URL          (PLAYWRIGHT_BASE_URL / AUDIT_BASE_URL)
//   2. the exported port             (HERMOD_UI_PORT, same shell as dev.sh)
//   3. the running stack's record    (.dev/.ports)
//   4. the preferred default         (5175)
//
// Deliberately no `import.meta.url`: Playwright transpiles a module imported
// from its TypeScript config down to CommonJS, where `import.meta` is a syntax
// error that takes the whole config — and therefore every spec — with it. The
// repo root is found by walking up for go.mod instead, which also lets this be
// imported from `ui/` as well as the root.
import fs from 'node:fs'
import path from 'node:path'

export const DEFAULT_UI_PORT = 5175
export const DEFAULT_API_PORT = 4005

function repoRoot() {
  let dir = process.cwd()
  for (let i = 0; i < 10; i++) {
    if (fs.existsSync(path.join(dir, 'go.mod'))) return dir
    const parent = path.dirname(dir)
    if (parent === dir) break
    dir = parent
  }
  return process.cwd()
}

// Reads .dev/.ports, which dev.sh writes as shell assignments (UI_PORT=5176).
// A missing file is the normal case when no stack is running; a malformed one
// must not take the test run down, so both fall through to the defaults.
function recordedPorts() {
  try {
    const raw = fs.readFileSync(path.join(repoRoot(), '.dev', '.ports'), 'utf8')
    return Object.fromEntries(
      raw
        .split('\n')
        .map((line) => line.match(/^(\w+)=(\d+)$/))
        .filter(Boolean)
        .map((m) => [m[1], Number(m[2])]),
    )
  } catch {
    return {}
  }
}

export function uiPort() {
  return Number(process.env.HERMOD_UI_PORT) || recordedPorts().UI_PORT || DEFAULT_UI_PORT
}

export function apiPort() {
  return Number(process.env.HERMOD_API_PORT) || recordedPorts().API_PORT || DEFAULT_API_PORT
}

export function uiBaseURL() {
  return `http://localhost:${uiPort()}`
}

export function apiBaseURL() {
  return `http://127.0.0.1:${apiPort()}`
}
