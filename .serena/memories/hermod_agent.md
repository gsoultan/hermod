# hermod-agent: the device data agent

A separate program (`cmd/hermod-agent`) that runs next to hardware, reads
serial and LAN devices, and streams their readings to a Hermod server. Built in
phases; the design doc is the Claude Doc "Hermod Device Agent: design".

## What exists (phase 1, part 1: the agent program)

- `pkg/agent/proto/agent.proto` — `hermod.agent.v1 AgentService.Connect`, one
  bidirectional stream. Agent sends Hello, DeviceList, RecordBatch, Heartbeat;
  server sends Welcome, DeviceSettingsList, Ack. Token as
  `authorization: Bearer <token>` metadata. A buf module (lint gated; add it to
  the `buf breaking` step once it exists on main).
- `cmd/hermod-agent/internal/spool` — append-only segment files, CRC-32C per
  record, torn tail truncated on open, `state.json` holds acked + dropped.
  Sequence numbers never repeat for one spool directory, even after every
  segment is deleted. Full spool drops oldest segments and counts them as
  dropped (and acked), never silently.
- `framing` — line (LF/CR/CRLF), delimiter, fixed length, start/end,
  length prefix (1/2/4 BE), idle gap. Oversize records dropped and counted.
- `reader` — one goroutine per device; reads on a pump goroutine so idle-gap
  and cancel work for any io.Reader. A new framer per connection, so a partial
  record never glues onto the next connection. TCP listen mode frames each
  connection separately.
- `detect` — polls every 2 s. Device id is `usb:<vid>:<pid>:<serial>` when the
  USB serial number is known, else `port:<path>`. The resolver follows a device
  to a new port after a replug. go.bug.st/serial's enumerator needs cgo on
  macOS, so `ports_plain.go` lists names only there; Linux and Windows are full.
- `uplink` — resends from the last acked seq after any reconnect; Welcome's
  acked_seq acknowledges what the server already has; Window (4096) caps
  unacknowledged records in flight. Plaintext only to loopback.
- `agent` — wires it; caches server settings to `settings.pb` so devices keep
  reading while the server is down. Batches spool writes (256 or 20 ms).

## Platforms and service

- `service.go` uses github.com/kardianos/service (already a module dep; the
  guard allows non-Hermod imports). `-service install|uninstall|start|stop|
  restart|status`. Install needs `-token-file` and writes absolute paths
  (`serviceArgs`). When started by a service manager (`!service.Interactive()`)
  `runAsService` hands start/stop to it; `program.Stop` waits up to 20 s for
  the agent to close its spool. Windows logs go to the Event Log
  (`eventLogWriter`); launchd gets `LogOutput` (macOS only, it would take
  systemd's log out of the journal); systemd-only `Dependencies` (they are
  service names on Windows/OpenRC).
- kardianos has no OpenBSD backend: `-service` fails there, the agent runs in
  the foreground. FreeBSD's rc.d script runs `daemon(8)` without `-o`, so its
  output is not kept.
- `defaultDataDir(goos, getenv)`: Windows `%ProgramData%\hermod-agent`, macOS
  `/Library/Application Support/hermod-agent`, else `/var/lib/hermod-agent`.
- Release: `.goreleaser.yaml` build id `hermod-agent` (21 targets, CGO off),
  own archive (zip on Windows), excluded from the hermod archives/nfpms via
  `ids`. CI job `agent-platforms` builds the same matrix. NetBSD excluded
  (go.bug.st/serial does not build). goreleaser has no freebsd/riscv64.

## Rules

- `guard_test.go` fails if the binary links any Hermod package other than
  `cmd/hermod-agent/...` and `pkg/agent/proto`. Keep the agent free of the
  engine; share only the wire contract.
- The token is read from `$HERMOD_AGENT_TOKEN` or `-token-file`, never a flag.
- Decoding (fields from raw bytes) belongs on the server side, so a fix does
  not need an agent upgrade; the agent ships raw frames.

## Not built yet

Server side (agent service, Agent source with real acks, storage), UI (Agents
tab in Settings, agent page, claim device with Listen), commands to devices
(Agent device sink, phase 2), packages and service units.
