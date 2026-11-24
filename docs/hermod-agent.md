# hermod-agent

`hermod-agent` is a small, separate program that runs on a machine next to your
hardware — a Raspberry Pi, an industrial PC or a Windows PC. It finds the
devices attached to it, reads the ones you claim in Hermod, keeps every reading
on its own disk, and sends the readings to a Hermod server. A reading leaves
the agent's disk only after the server confirms delivery, so a network outage
or a server restart loses nothing.

> **Status:** the agent and Hermod's side of it (the agent service, the REST
> API and the Agent source) are built. The Agents page in the UI is the next
> change; until then agents are added and their devices claimed through the
> API below.

## What it reads

| Input | Covers |
| --- | --- |
| Serial | RS-232, RS-485 through an adapter, USB-serial cables, ESP32 and Arduino boards plugged in by USB, scales, many barcode scanners |
| TCP client | A LAN device the agent connects to at `host:port`: scales, label printers, serial-to-Ethernet converters, ESP32 boards on Wi-Fi |
| TCP listen | LAN devices that connect to the agent. The listen address must name a host, so it never opens on every interface by accident |

ESP32 boards that publish to an MQTT broker do not need the agent: use Hermod's
MQTT source.

Each device's byte stream is cut into records by a framing rule: one record per
line (LF, CR or CRLF), a delimiter, a fixed length, start and end bytes (such
as STX and ETX), a length prefix, or a quiet gap. A record longer than the
limit (64 KiB by default, at most 1 MiB) is dropped and counted rather than
held in memory.

## Running it

```sh
export HERMOD_AGENT_TOKEN=...        # or -token-file /etc/hermod-agent/token
hermod-agent -server hermod.example.com:9443
```

The agent connects to Hermod's gRPC port (50051 unless `-grpc-port` says
otherwise). That port serves plain gRPC, and the agent requires TLS to anything
but localhost, so put a TLS terminator in front of it (a load balancer, an
ingress or a reverse proxy that passes HTTP/2 through) and point `-server` at
that.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-server` | (required) | The Hermod agent address, `host:port` |
| `-token-file` | | File holding the agent token. Otherwise `$HERMOD_AGENT_TOKEN`. The token is never a flag, so it does not show in the process list |
| `-ca-file` | | An extra CA certificate to trust for the server |
| `-plaintext` | off | Connect without TLS. Refused for anything but localhost |
| `-data-dir` | `/var/lib/hermod-agent`, `%ProgramData%\hermod-agent` on Windows | Where the spool and the cached device settings live |
| `-spool-max-mb` | 1024 | The most the spool may hold on disk |

On Linux the user running the agent needs to be in the `dialout` group to open
serial ports.

## Devices and their identity

The agent lists serial ports every 2 seconds. A USB device that reports a
serial number is known as `usb:<vid>:<pid>:<serial>` and keeps that identity
in any USB socket; the agent follows it to its new port after a replug. A
device without a serial number is known by its port path, which can change
after a replug.

## Delivery

Every reading gets a sequence number that increases by one and never repeats
for one data directory. Readings are written to the spool and synced before
they are sent. The server acknowledges by sequence number; after a disconnect
the agent sends again everything after the last acknowledgement, so a sink sees
each reading at least once and `agent id + seq` is a ready idempotency key.

When the spool reaches its size limit, the oldest readings are deleted and
counted as dropped, and the count is reported to the server. The agent keeps
the last device settings the server sent, so after a restart it reads its
devices even while the server is unreachable.

## Adding an agent and claiming devices

An agent belongs to one vhost. An Editor of the vhost adds it, and is shown
its token once (these calls go to Hermod's API as a signed-in user, like the
UI's):

```sh
curl -X POST https://hermod.example.com/api/vhosts/plant/agents -d '{"name":"Line 1 Pi"}'
# {"agent":{"id":"…",…},"live":{…},"token":"hag_…"}
```

Only a hash of the token is stored. `POST /api/agents/{id}/token` issues a new
one, which revokes the old one and disconnects the agent;
`DELETE /api/agents/{id}` removes the agent.

`GET /api/agents/{id}` shows what the agent last reported: its host, version,
the ports it found (`status.ports`), its spool, and whether it is connected to
this Hermod now (`live`). To claim devices, save their settings:

```sh
curl -X PUT https://hermod.example.com/api/agents/$ID -d '{
  "name": "Line 1 Pi",
  "devices": [
    {"device_id": "usb:0403:6001:A1", "kind": "serial", "name": "scale", "enabled": true,
     "serial": {"baud_rate": 9600, "parity": "none", "stop_bits": "1"},
     "framing": {"kind": "line"}},
    {"device_id": "tcp:plc", "kind": "tcp", "name": "plc", "enabled": true,
     "tcp": {"address": "10.0.0.5:4001", "mode": "client"},
     "framing": {"kind": "start_end", "start_hex": "02", "end_hex": "03"}}
  ]}'
```

A serial device's `device_id` is one the agent reported; a TCP device's is any
id you choose. Framing kinds are `line`, `delimiter` (`delimiter_hex`),
`fixed_length` (`length`), `start_end` (`start_hex`, `end_hex`),
`length_prefix` (`prefix_size` 1, 2 or 4) and `idle_gap` (`idle_ms`); bytes are
written in hex. Settings the agent would refuse are refused when they are
saved. A connected agent gets them at once.

## Reading devices in a workflow

The Agent source (`type: agent`) reads an agent of the workflow's vhost:

| Config | Meaning |
| --- | --- |
| `agent_id` | The agent (required) |
| `devices` | Comma-separated device ids to read; all of the agent's devices when empty |
| `format` | How a record's bytes appear: `text` (default; hex when they are not UTF-8), `json`, `hex` or `base64` |

Each message is a JSON object: `agent_id`, `device_id`, `device` (its name),
`seq`, `received_at`, `port`, and the record under `text`, `data` (json),
`hex` or `base64`. A record that is not valid JSON in `json` format keeps its
text and adds `decode_error`. The message id is `<agent id>:<seq>` and its
table is the device's name.

Hermod acknowledges a record to the agent only when every workflow reading its
device has finished with it, so it stays in the agent's spool until then. A
workflow that stops with records in hand ends the agent's stream, and the agent
sends them again. A record no running workflow reads is acknowledged and
counted in `live.unrouted`.

The agent and the workflows that read it meet in one Hermod process: run the
workflows on the Hermod the agent connects to.

## Building

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=0.1.0" ./cmd/hermod-agent
```

Linux (amd64, arm64, arm) and Windows (amd64) build without cgo, at about
11–13 MB. On macOS without cgo the agent lists ports by name only, without USB
details.

The agent shares nothing with the Hermod server except the wire contract in
`pkg/agent/proto`; `cmd/hermod-agent/guard_test.go` fails the build if that
changes.
