# hermod-agent

`hermod-agent` is a small, separate program that runs on a machine next to your
hardware — a Raspberry Pi, an industrial PC or a Windows PC. It finds the
devices attached to it, reads the ones you claim in Hermod, keeps every reading
on its own disk, and sends the readings to a Hermod server. A reading leaves
the agent's disk only after the server confirms delivery, so a network outage
or a server restart loses nothing.

> **Status:** the agent program is built. The server side (the Agents page,
> claiming devices and the Agent source for workflows) is the next change, so
> the agent has nothing to connect to yet.

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
limit (64 KiB by default) is dropped and counted rather than held in memory.

## Running it

```sh
export HERMOD_AGENT_TOKEN=...        # or -token-file /etc/hermod-agent/token
hermod-agent -server hermod.example.com:9443
```

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
