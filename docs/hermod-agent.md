# hermod-agent

`hermod-agent` is a small, separate program that runs on a machine next to your
hardware — a Windows, Linux or macOS workstation, an edge server, an
industrial PC or a Raspberry Pi. It finds the
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
| `-data-dir` | see below | Where the spool and the cached device settings live |
| `-spool-max-mb` | 1024 | The most the spool may hold on disk |
| `-service` | | Manage the agent as a system service: `install`, `uninstall`, `start`, `stop`, `restart` or `status` |

On Linux the user running the agent needs to be in the `dialout` group to open
serial ports. Installed as a service it runs as root (`SYSTEM` on Windows), so
that does not apply.

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
