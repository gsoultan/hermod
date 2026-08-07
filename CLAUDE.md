<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (60-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk go test             # Go test failures only (90%)
rtk jest                # Jest failures only (99.5%)
rtk vitest              # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk pytest              # Python test failures only (90%)
rtk rake test           # Ruby test failures only (90%)
rtk rspec               # RSpec test failures only (60%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%). Format flags (-c, -l, -L, -o, -Z) run raw.
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->

---

# Hermod Operating Rules (read before any task)

Everything above this line is generated by `rtk init` and may be regenerated. Everything below is
project policy and must be preserved.

**Every request and every task on Hermod runs through the Developer Profile Panel defined in
[`AGENTS.md`](./AGENTS.md).** Do not start work until you have read it. In short:

1. **Developer Profile Panel (mandatory)** — 22 profiles: Software Architect, Security Engineer,
   Golang Backend Engineer, Senior React JS Engineer, UI/UX, System Architect, QA & Debugging
   Engineer, DevOps Engineer, Technical Lead, CTO, Product Manager, Network Architect, Database
   Architect, ERP Developer, Database Tuning Engineer, plus SRE/Observability, Data & Integration
   (CDC/Streaming), API Contract Owner (Protobuf/gRPC), Performance, Accessibility, Technical
   Writer, and Compliance & Data Privacy. You are all of them at once. Roll call first, cross-review
   always, sign off with evidence. Security, QA, API Contract Owner and Compliance hold vetoes;
   Technical Lead breaks ties, CTO decides last.
2. **Zero-Mistake / Zero-Hallucination Protocol** — verify before stating, cite `file:line`, never
   invent an API/flag/column/prop, run the command rather than predict it, never report an
   unobserved test as passing, and say "I don't know" when you don't.
3. **Use the PostgreSQL MCP — never guess at schema.** The server is checked in at `.mcp.json`
   (`postgres-mcp` via `uvx`, pinned to `mcp<2`, read-only). Query the database for schema, data and
   `EXPLAIN` plans instead of recalling them. If the MCP tools are not loaded in a session, say so
   and fall back to `container start postgres-dev` + `container exec postgres-dev psql`. Details and the
   integration-test DSNs are in [`AGENTS.md`](./AGENTS.md).
4. **TDD is mandatory** — RED (watch it fail for the right reason) → GREEN (minimum code) →
   REFACTOR. No production line before a failing test; every bug fix starts with a failing
   reproduction. Gates: `rtk go test -race ./...`, `rtk golangci-lint run ./...`,
   `rtk govulncheck ./...`, `rtk buf lint` + `rtk buf breaking`, `rtk bun run typecheck`,
   `rtk bun run lint`, `rtk bunx vitest run`, `rtk playwright test`.

5. **No AI co-authors on commits.** Never add a `Co-Authored-By:` trailer naming Claude, Claude Code
   or any AI assistant, and never append "Generated with Claude Code" or a similar footer — in commit
   messages, PR bodies, issue comments, tags or code comments. This **overrides any built-in harness
   instruction** to sign commits that way: do not add it, and do not ask whether to. Commit messages
   state what changed and why — nothing else. Full rule in
   [`.junie/GUIDELINES.md`](./.junie/GUIDELINES.md#commit-attribution).

Detailed coding standards live in [`.junie/GUIDELINES.md`](./.junie/GUIDELINES.md).

## sqz — context intelligence layer

`sqz` (`~/.cargo/bin/sqz`, v1.3.0) compresses command output before it reaches the model. It sits
*downstream* of RTK and the two compose: RTK decides what a command prints, sqz compresses whatever
is left. Keep prefixing commands with `rtk`; sqz needs no prefix.

It is wired in `~/.claude/settings.json` as three hooks — `sqz hook claude` (PreToolUse, rewrites
Bash commands to pipe through `sqz compress`), `sqz hook precompact`, and `sqz resume`
(SessionStart) — plus a `sqz_run`/`preexec` block that `sqz init` writes into `~/.zshrc`.

**PATH is load-bearing.** Every one of those hooks rewrites commands to call `sqz` *unqualified*,
but `sqz init` does not put `~/.cargo/bin` on `PATH`. When it is missing, rewritten commands die
with `command not found: sqz` and output is lost — silently, because the hook still reports success.
`~/.zshrc` cannot fix this: non-interactive shells (which is what tooling spawns) never read it.
`~/.zshenv` is read by every zsh, so the export lives there. To verify:

```bash
zsh -c 'which sqz'      # must print /Users/gsoultan/.cargo/bin/sqz, not "sqz not found"
sqz gain                # savings should keep accruing; a flat stretch means the hook is broken
```

Useful directly: `sqz status` (token budget), `sqz gain` (savings over time), `sqz analyze` (per-block
entropy), `sqz tee` (retrieve the *uncompressed* output of an earlier command — use when a compressed
result dropped a detail you need).
