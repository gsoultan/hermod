# hermod-agent

`hermod-agent` is a small, separate program that runs on a machine next to your
hardware — a Windows, Linux or macOS workstation, an edge server, an
industrial PC or a Raspberry Pi. It finds the
devices attached to it, reads the ones you claim in Hermod, keeps every reading
on its own disk, and sends the readings to a Hermod server. A reading leaves
the agent's disk only after the server confirms delivery, so a network outage
or a server restart loses nothing.

> **Status:** the agent and Hermod's side of it (the agent service, the REST
> API, the Agent source and the Agent device sink) are built. The Agents page
> in the UI is the next change; until then agents are added and their devices
> claimed through the API below.

## What it reads

| Input | Covers |
| --- | --- |
| Serial | RS-232, RS-485 through an adapter, USB-serial cables, ESP32 and Arduino boards plugged in by USB, scales, many barcode scanners |
| TCP client | A LAN device the agent connects to at `host:port`: scales, label printers, serial-to-Ethernet converters, ESP32 boards on Wi-Fi |
| TCP listen | LAN devices that connect to the agent. The listen address must name a host, so it never opens on every interface by accident |
| UDP | Broadcast sensors and syslog-style devices. Each datagram is one record, tagged with its sender; `allow_from` keeps only listed addresses or CIDR ranges |
| Modbus RTU and TCP | PLCs, energy meters, industrial sensors. The agent polls a register map on an interval; each poll is one record |
| USB HID | Barcode scanners in keyboard mode, read directly so a scan is not typed into the window that has focus. Linux only, see below |

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
| `-data-dir` | see below | Where the spool and the cached device settings live |
| `-spool-max-mb` | 1024 | The most the spool may hold on disk |
| `-service` | | Manage the agent as a system service: `install`, `uninstall`, `start`, `stop`, `restart` or `status` |
| `-mdns-services` | `_hermod._tcp,_modbus._tcp` | mDNS service types to look for on the LAN; empty turns mDNS discovery off |
| `-scan-subnet` | off | IPv4 ranges to scan for Modbus TCP (port 502), comma-separated, at most 4096 addresses in all |
| `-discover-interval` | 5m | How often to look for LAN devices; at least 1m |
| `-allow-update` | off | Install a newer release when Hermod offers one, then restart. See [Updating](#updating) |

On Linux the user running the agent needs to be in the `dialout` group to open
serial ports, and read access to `/dev/input/event*` (the `input` group) and
`/dev/hidraw*` (a udev rule) for HID devices. Installed as a service it runs as
root (`SYSTEM` on Windows), so that does not apply.

## Platforms

One agent program runs on every kind of machine that sits next to hardware.

| Machine | Builds | Runs as | Default data folder | Logs |
| --- | --- | --- | --- | --- |
| Windows workstation or server | x64, ARM64, x86 | Windows service, starts at boot, restarts after a crash | `%ProgramData%\hermod-agent` | Windows Event Log (source `hermod-agent`) |
| Linux workstation or edge server | x86-64, ARM64, x86, RISC-V | systemd unit, restarts after a crash | `/var/lib/hermod-agent` | `journalctl -u hermod-agent` |
| Raspberry Pi and other ARM boards | ARM64, ARMv7, ARMv6 (Pi Zero and Pi 1) | systemd unit | `/var/lib/hermod-agent` | `journalctl -u hermod-agent` |
| macOS workstation | Apple Silicon, Intel | launchd daemon, starts at boot | `/Library/Application Support/hermod-agent` | `/var/log/hermod-agent.out.log` and `.err.log` |
| FreeBSD | x86-64, ARM64, ARMv7, ARMv6, x86 | rc.d script run through `daemon(8)` | `/var/lib/hermod-agent` | not kept: run it in the foreground to watch it |
| OpenBSD | x86-64, ARM64, ARMv7, ARMv6, x86 | in the foreground; `-service` is not available, so start it from your own `rc.d` script | `/var/lib/hermod-agent` | wherever you send stderr |

NetBSD is not supported: the serial-port library the agent uses does not build
there. Linux systems without systemd (OpenRC, SysV init, Upstart) get the
matching service script too.

The server records each agent's host name, operating system and architecture,
so you can tell a Windows workstation from an edge server at a glance.

### Installing it as a service

Put the token in a file only the administrator can read, then install. A
service does not inherit your shell, so `-service install` needs `-token-file`
and records every path as an absolute path.

Linux, Raspberry Pi, macOS and the BSDs, as root:

```sh
install -d -m 700 /etc/hermod-agent
printf '%s\n' 'hag_...' > /etc/hermod-agent/token && chmod 600 /etc/hermod-agent/token
hermod-agent -service install -server hermod.example.com:9443 -token-file /etc/hermod-agent/token
hermod-agent -service start
hermod-agent -service status
```

Windows, in an Administrator PowerShell:

```powershell
New-Item -ItemType Directory -Force "$env:ProgramData\hermod-agent" | Out-Null
Set-Content -NoNewline "$env:ProgramData\hermod-agent\token" 'hag_...'
icacls "$env:ProgramData\hermod-agent\token" /inheritance:r /grant:r "SYSTEM:R" "Administrators:R"
.\hermod-agent.exe -service install -server hermod.example.com:9443 -token-file "$env:ProgramData\hermod-agent\token"
.\hermod-agent.exe -service start
```

To change a setting, run `-service uninstall` and install again with the new
flags. Stopping the service lets the agent finish writing and close its spool;
nothing it has read is lost.

## Devices and their identity

The agent lists serial ports every 2 seconds. A USB device that reports a
serial number is known as `usb:<vid>:<pid>:<serial>` and keeps that identity
in any USB socket; the agent follows it to its new port after a replug. A
device without a serial number is known by its port path, which can change
after a replug.

### HID devices

On Linux the agent lists HID devices from sysfs as `hid:<vid>:<pid>:<serial>`
(the physical path when there is no serial number; interfaces of one device
sharing a serial get their interface appended). In `keyboard` mode it opens
the device's key events and grabs them, so only the agent sees them, and turns
them into text (US layout), one record per Enter. In `raw` mode each HID input
report is one record. On Windows, macOS and the BSDs the agent lists no HID
devices; most scanners can switch to USB-serial (CDC) mode, which every
platform reads as a serial device.

### Finding LAN devices

Every `-discover-interval` the agent asks for the `-mdns-services` over mDNS.
The query goes from an ephemeral port, so devices answer it directly and the
agent needs nothing on port 5353. An ESP32 announces itself in a few lines of
firmware (`MDNS.addService("hermod", "tcp", 9100)`). With `-scan-subnet` the
agent also tries TCP port 502 across those ranges, 32 addresses at a time.

What it finds is reported with the other devices, with `discovered_by` set to
`mdns` or `scan` (`mdns:<instance>.<service>`, or `modbus:<ip>:502`), for you
to add with its address. Nothing is read until you do. The scan is off unless
you name ranges, because a scan of a network you do not own may break its
rules.

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

A serial, HID or Modbus RTU device's `device_id` is one the agent reported; a
TCP, UDP or Modbus TCP device's is any id you choose. Framing kinds are `line`, `delimiter` (`delimiter_hex`),
`fixed_length` (`length`), `start_end` (`start_hex`, `end_hex`),
`length_prefix` (`prefix_size` 1, 2 or 4) and `idle_gap` (`idle_ms`); bytes are
written in hex. Settings the agent would refuse are refused when they are
saved. A connected agent gets them at once.

Other kinds take their own settings instead of `serial` or `tcp`:

```json
{"device_id": "udp:sensors", "kind": "udp", "enabled": true,
 "udp": {"address": "192.168.1.10:5005", "allow_from": ["192.168.1.0/24"]}}

{"device_id": "modbus:meter", "kind": "modbus", "enabled": true,
 "modbus": {"transport": "tcp", "address": "10.0.0.20:502", "unit_id": 1,
            "poll_interval_ms": 1000, "timeout_ms": 1000,
            "reads": [{"name": "power", "table": "holding_registers", "address": 0, "count": 2},
                      {"name": "running", "table": "coils", "address": 10, "count": 1}]}}

{"device_id": "usb:0403:6001:A1", "kind": "modbus", "enabled": true,
 "serial": {"baud_rate": 19200, "parity": "even"},
 "modbus": {"transport": "rtu", "unit_id": 3,
            "reads": [{"name": "temp", "table": "input_registers", "address": 0, "count": 4}]}}

{"device_id": "hid:05e0:1200:S123", "kind": "hid", "enabled": true, "hid": {"mode": "keyboard"}}
```

A Modbus record is a JSON object with each read's values in read order, for
example `{"power":[12,3456],"running":[true]}`: registers as numbers 0 to
65535, coils and inputs as `true` or `false`. Scale and combine them in the
workflow. Tables are `coils`, `discrete_inputs`, `holding_registers` and
`input_registers`; addresses are zero-based; one read takes at most 125
registers or 2000 coils. Polls run at most every 100 ms. A connection that
fails is reopened with backoff; a poll the device refuses with a Modbus
exception is skipped.

## Sending commands to devices

A device takes commands only when `allow_commands` is on, and then only the
commands saved on it. The agent checks this too: it refuses a command for a
device without `allow_commands`, whatever Hermod sends, and it never runs
anything.

```json
{"device_id": "tcp:scale", "kind": "tcp", "enabled": true,
 "tcp": {"address": "10.0.0.9:4001", "mode": "client"},
 "allow_commands": true,
 "commands": [
   {"name": "tare", "payload": "T\\r\\n"},
   {"name": "weigh", "payload": "W\\r\\n", "reply_timeout_ms": 2000},
   {"name": "set", "payload": "S {{target}}\\r\\n"},
   {"name": "reset", "encoding": "hex", "payload": "1b40"}]}
```

A text payload understands `\r`, `\n`, `\t`, `\\` and `\xHH`; a `hex`
payload is the bytes in hex. `{{field}}` (or `{{a.b}}`) is filled from the
message that triggers the command; a value with a control character such as a
line ending is refused, so a field cannot add a second command. With
`reply_timeout_ms` (at most 60000) the agent waits for the device's next record
and returns it as the reply; that record is also an ordinary reading.

For a Modbus device a command is one write, as text:
`holding <address> <value>...` writes holding registers (values 0 to 65535)
and `coil <address> <on|off>...` writes coils, from the address on. For
example `{"name": "setpoint", "payload": "holding 40 {{value}}"}`.

The **Agent device sink** (`type: agent`) sends one saved command per message:

| Config | Meaning |
| --- | --- |
| `agent_id` | The agent (required) |
| `device_id` | The device (required) |
| `command` | The name of a command saved on the device (required) |

The agent and its settings are read again for every message, so turning
`allow_commands` off stops the next command. A command for an agent that is
not connected fails at once and is not kept, so a relay never switches hours
late. A failure that another attempt cannot fix, and a command that may
already have reached the device (written, then no reply in time, or the agent
dropped off), are permanent: they go to the dead-letter sink instead of being
sent twice. A command refused before anything was written is retried. To have a
person approve each command, put an Approval node before the sink.

To try a command, an Editor of the agent's vhost can run it:

```sh
curl -X POST https://hermod.example.com/api/agents/$ID/devices/tcp:scale/commands/set \
  -d '{"fields": {"target": 12.5}}'
# {"replied": false}
```

The answer carries the reply as text (`reply`, when it is UTF-8) and as hex
(`reply_hex`). An agent that is not connected answers 409; a command that is
not saved or not allowed, 400; a failure on the device, 502. Every attempt is
in the audit log with action `command`.

## Updating

An agent started with `-allow-update` tells Hermod so, and Hermod offers it its
own release when the agent runs an older one. Hermod sends only the version:
the agent builds the download address itself from the project's GitHub
releases and its own platform, checks the archive against the release's
`checksums.txt`, runs the new binary with `-version` to see it reports the
offered version, and only then renames it into place, keeping the old one as
`hermod-agent.old` beside it. It then restarts: on Linux, macOS and the BSDs it
replaces its own process (same pid, so the service manager keeps tracking it),
a Windows service exits so the Service Control Manager restarts it, and a
console run starts the new binary. It never moves to an older version, and a
development build of either side never updates.

The agent must be able to write the folder its binary is in. A service runs as
root or `SYSTEM`, so it can; an agent run as another user needs its binary in
a folder that user owns. Without `-allow-update` an offer is only logged.

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

Every platform in the table above builds without cgo, at about 11–13 MB, and
CI builds all of them on every change (the `hermod-agent platforms` job). The
release attaches an archive per platform: `hermod-agent_<version>_<os>_<arch>`,
a `.zip` for Windows and a `.tar.gz` for the rest.

Without cgo the macOS build lists serial ports by name only, without the USB
vendor, product and serial number, so a device on macOS is known by its port
path. Build on a Mac with `CGO_ENABLED=1` to get the USB details.

The agent shares nothing with the Hermod server except the wire contract in
`pkg/agent/proto`; `cmd/hermod-agent/guard_test.go` fails the build if that
changes.
