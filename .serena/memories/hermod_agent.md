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
  unacknowledged records in flight; MaxBatchBytes (1 MiB) caps a batch's
  payload, because Hermod refuses gRPC messages over 4 MiB. Framing refuses
  a record limit over 1 MiB. Plaintext only to loopback.
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

## Hermod side

- `internal/storage/agents.go` — `Agent` (vhost, name, token hash, claimed
  `Devices` settings, reported `Status`), `ValidateAgentDevices` (mirrors what
  the agent refuses), tokens `hag_<agent id>_<32 random bytes b64url>` stored
  as SHA-256 hex, `AgentStore` (optional, type-asserted). SQL table `agents`
  (devices/status JSON columns; in vhost rename + delete), Mongo collection
  `agents`. `ErrAgentsUnsupported`.
- `internal/agenthub` — `Hub` implements `AgentService.Connect`; `Default` is
  the process hub, registered on the gRPC server in `internal/api/server.go`
  with the current store. Per-agent `ledger` (survives reconnects): acks are
  cumulative, so the ack moves only when every subscription waiting on a
  record has called `Done`. Resends (seq <= highest accepted) are skipped;
  Hello.last_seq below highest = wiped spool -> ledger reset. A
  `Subscription` closed while holding records resets the ledger and ends the
  stream (codes.Aborted) so the agent resends. Records with no subscriber are
  acked and counted (`Live().Unrouted`). Subscriptions match on vhost
  ("" = "default") and device ids.
- `internal/agenthub/transport/http` — `/api/vhosts/{vhost}/agents` (GET,
  POST -> token once), `/api/agents/{id}` (GET, PUT name+devices -> pushes
  settings, DELETE), `/api/agents/{id}/token` (POST, revokes + disconnects).
  Audited with entity type `agent`.
- `pkg/comm/source/agent` — Agent source (`type: agent`, config `agent_id`,
  `devices`, `format` text|json|hex|base64). Subscribes on first Read; Ack
  reads `agent_seq` metadata. Message id `<agent>:<seq>`, table = device name.
- Limitation: agent and its workflows must be in the same process; the gRPC
  port is plaintext, so production needs a TLS terminator in front.
- `cmd/hermod-agent/internal/agent/hermod_e2e_test.go` runs the real agent
  against the real hub, SQL store and source.

## Phase 2 (branch feat/hermod-agent-commands)

- Commands. Proto: `ConnectResponse.command`, `ConnectRequest.command_result`,
  `DeviceSettings.allow_commands`. Agent `agent/commands.go` re-checks
  allow_commands itself; `reader.Link` serialises commands per device, writes
  to the open connection (5 s deadline) and takes the next framed record as
  the reply. `WrittenError` = may have reached the device. Modbus devices
  write through `reader.ModbusDevice` ("holding <addr> <v>..." /
  "coil <addr> <on|off>...", `modbus.ParseWrite`).
  Hermod: `storage.AgentCommand` templates (text escapes \r \n \t \\ \xHH,
  `{{field}}` placeholders, control chars in values refused; or hex),
  `Agent.FindCommand`, `RenderAgentCommand`. `Hub.SendCommand` (offline =
  `ErrAgentOffline` at once; `CommandError{Written}`), `Hub.RunCommand`
  (reloads agent from store, vhost check, wraps hermod.ErrPermanent for
  refusals, offline and possibly-written). Sink `pkg/comm/sink/agent`
  (`type: agent`, agent_id/device_id/command). REST
  `POST /api/agents/{id}/devices/{device}/commands/{name}` (Editor, audited
  action `command`; 409 offline, 400 refused, 502 device failure).
- Kinds: `udp` (`reader.RunUDP`, datagram = record, port = sender,
  `allow_from`), `modbus` (`internal/modbus` own client, TCP MBAP + RTU CRC16
  over go.bug.st/serial; `reader.RunModbus` polls, JSON record in read order,
  exception skips a poll, other errors reconnect), `hid` (Linux only:
  `internal/hid` sysfs listing, evdev keyboard + EVIOCGRAB 0x40044590, US
  keymap, record per Enter; raw = hidraw, `reader.Config.PerRead`).
- Discovery `internal/discover`: mDNS `Browse` from an ephemeral port
  (legacy unicast answers), `Scan` IPv4 port 502, max 4096 hosts, 32 workers.
  Agent flags `-mdns-services` (default `_hermod._tcp,_modbus._tcp`),
  `-scan-subnet` (opt-in), `-discover-interval` (5m, min 1m). Found devices
  have `discovered_by` mdns|scan; ids `mdns:<inst>.<svc>`, `modbus:<ip>:<port>`.
- Self-update `internal/update`: `Update{version}` only; agent builds
  `<base>/<tag>/hermod-agent_<ver>_<os>_<arch>[v<goarm>].{tar.gz|zip}`,
  checks checksums.txt, runs `-version`, renames (keeps `.old`). Opt-in
  `-allow-update` (Hello.accepts_updates). `agent.ErrRestart` -> main
  `restartSelf`: Unix syscall.Exec (exe path captured at start), Windows
  service exit 3 (SCM recovery), console starts new process. Hermod offers
  `version.Version` via `Hub.SetAgentRelease` when newer than the agent's.
- Branch merges #431's branch (feat/hermod-agent-platforms) and is based on
  #430's; that merge garbled core.md's hermod-agent entry, fixed here.

## Not built yet

UI (Agents tab in Settings, agent page, claim device with Listen, Agent
source and Agent device sink forms), Listen (live frames), HID on Windows and
macOS, Bluetooth LE, OPC UA, SNMP (phase 3).
