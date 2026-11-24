// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Agent is a hermod-agent installation: a program on a machine next to the
// devices, which reads them and sends what they say to Hermod. An agent
// belongs to one vhost, and only that vhost's workflows read its devices.
//
// Devices holds the settings of the devices a user has claimed; the agent
// reads those and no others. Status is what the agent last reported.
type Agent struct {
	ID    string `json:"id"`
	VHost string `json:"vhost"`
	Name  string `json:"name"`

	// TokenHash is the SHA-256 of the agent's token (HashAgentToken). The
	// token itself is shown once, when it is issued, and never stored.
	TokenHash string `json:"-"`

	Devices []AgentDevice `json:"devices"`
	Status  AgentStatus   `json:"status"`

	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Device kinds.
const (
	AgentDeviceSerial = "serial"
	AgentDeviceTCP    = "tcp"
	AgentDeviceUDP    = "udp"
	AgentDeviceModbus = "modbus"
	AgentDeviceHID    = "hid"
)

// AgentDevice is how the agent reads one claimed device.
type AgentDevice struct {
	DeviceID string `json:"device_id"`
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	// Enabled is whether the agent reads the device now. A claimed device
	// that is not enabled keeps its settings and is left alone.
	Enabled bool `json:"enabled"`
	// Serial is a serial device's line settings, and a Modbus RTU device's.
	Serial *AgentSerial `json:"serial,omitempty"`
	TCP    *AgentTCP    `json:"tcp,omitempty"`
	UDP    *AgentUDP    `json:"udp,omitempty"`
	Modbus *AgentModbus `json:"modbus,omitempty"`
	HID    *AgentHID    `json:"hid,omitempty"`
	// Framing applies to serial and TCP devices. A UDP datagram, a Modbus
	// poll and a HID scan are already records; only the size limit applies.
	Framing AgentFraming `json:"framing"`
	// AllowCommands lets workflows send this device the Commands saved for
	// it. Off, nothing is written to the device, whatever is saved.
	AllowCommands bool           `json:"allow_commands,omitempty"`
	Commands      []AgentCommand `json:"commands,omitempty"`
}

// AgentSerial is a serial port's line settings. Parity is none, odd, even,
// mark or space; StopBits is 1, 1.5 or 2. Empty values are 8N1.
type AgentSerial struct {
	BaudRate int    `json:"baud_rate"`
	DataBits int    `json:"data_bits,omitempty"`
	Parity   string `json:"parity,omitempty"`
	StopBits string `json:"stop_bits,omitempty"`
}

// AgentTCP is a device on the network. Mode client connects to Address;
// mode listen accepts connections on it.
type AgentTCP struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
}

// AgentUDP receives datagrams on Address. AllowFrom, when set, keeps only
// datagrams from those addresses or CIDR ranges.
type AgentUDP struct {
	Address   string   `json:"address"`
	AllowFrom []string `json:"allow_from,omitempty"`
}

// AgentModbus polls a register map. Transport tcp reaches Address; rtu uses
// the device's serial settings. Each poll is one record: a JSON object with
// each read's name and its values.
type AgentModbus struct {
	Transport      string            `json:"transport"`
	Address        string            `json:"address,omitempty"`
	UnitID         int               `json:"unit_id,omitempty"`
	PollIntervalMS int               `json:"poll_interval_ms,omitempty"`
	TimeoutMS      int               `json:"timeout_ms,omitempty"`
	Reads          []AgentModbusRead `json:"reads"`
}

// AgentModbusRead is one block read on every poll. Table is coils,
// discrete_inputs, holding_registers or input_registers; Address is zero
// based.
type AgentModbusRead struct {
	Name    string `json:"name"`
	Table   string `json:"table"`
	Address int    `json:"address"`
	Count   int    `json:"count"`
}

// AgentHID reads a USB HID device. Mode keyboard turns key presses into
// text, one record per Enter (a barcode scanner); raw records each report.
type AgentHID struct {
	Mode string `json:"mode"`
}

// AgentFraming is how the agent cuts a device's byte stream into records.
// Byte strings are hex, so a delimiter such as CR LF is "0d0a".
type AgentFraming struct {
	Kind           string `json:"kind"`
	DelimiterHex   string `json:"delimiter_hex,omitempty"`
	Length         int    `json:"length,omitempty"`
	StartHex       string `json:"start_hex,omitempty"`
	EndHex         string `json:"end_hex,omitempty"`
	PrefixSize     int    `json:"prefix_size,omitempty"`
	IdleMS         int    `json:"idle_ms,omitempty"`
	MaxRecordBytes int    `json:"max_record_bytes,omitempty"`
}

// Framing kinds.
const (
	AgentFramingLine         = "line"
	AgentFramingDelimiter    = "delimiter"
	AgentFramingFixedLength  = "fixed_length"
	AgentFramingStartEnd     = "start_end"
	AgentFramingLengthPrefix = "length_prefix"
	AgentFramingIdleGap      = "idle_gap"
)

// AgentStatus is what an agent last told Hermod about itself.
type AgentStatus struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Version  string `json:"version,omitempty"`
	// AcceptsUpdates is whether the agent runs with -allow-update, so Hermod
	// offers it its own release when the agent's is older.
	AcceptsUpdates bool        `json:"accepts_updates,omitempty"`
	LastSeenAt     time.Time   `json:"last_seen_at,omitzero"`
	Ports          []AgentPort `json:"ports,omitempty"`
	Spool          AgentSpool  `json:"spool"`
}

// AgentPort is a device the agent found: a serial port, or a TCP device it
// has settings for.
type AgentPort struct {
	DeviceID     string `json:"device_id"`
	Kind         string `json:"kind"`
	Port         string `json:"port,omitempty"`
	VID          string `json:"vid,omitempty"`
	PID          string `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Product      string `json:"product,omitempty"`
	// StableID is whether DeviceID survives the device moving to another
	// port: it does for a USB device with a serial number.
	StableID bool `json:"stable_id"`
	Present  bool `json:"present"`
	// DiscoveredBy is "mdns" or "scan" for a LAN device the agent found by
	// itself, which a user can then add; Service is its mDNS service type.
	DiscoveredBy string `json:"discovered_by,omitempty"`
	Service      string `json:"service,omitempty"`
}

// AgentSpool is the agent's disk spool: records read but not yet confirmed
// by Hermod, and what it had to drop.
type AgentSpool struct {
	Waiting         uint64    `json:"waiting"`
	Bytes           uint64    `json:"bytes"`
	OldestAt        time.Time `json:"oldest_at,omitzero"`
	Dropped         uint64    `json:"dropped"`
	OversizeDropped uint64    `json:"oversize_dropped"`
}

// Bounds on an agent's settings.
const (
	maxAgentDevices    = 256
	maxAgentNameLen    = 128
	maxAgentDeviceID   = 512
	maxAgentFrameBytes = 64
	// MaxAgentRecordBytes is the largest record limit a device may have. It
	// matches the agent's own ceiling, which keeps a record well inside one
	// gRPC message.
	MaxAgentRecordBytes = 1 << 20
)

// ErrAgentsUnsupported is returned when the configured backend cannot store
// agents.
var ErrAgentsUnsupported = errors.New("this storage backend cannot hold agents")

// ValidateAgent reports what is wrong with an agent before it is saved.
func ValidateAgent(a Agent) error {
	if a.ID == "" {
		return errors.New("an agent needs an id")
	}
	if a.VHost == "" || a.VHost == "all" {
		return errors.New("an agent belongs to one vhost: name it")
	}
	if a.Name == "" || len(a.Name) > maxAgentNameLen {
		return fmt.Errorf("an agent needs a name of at most %d characters", maxAgentNameLen)
	}
	return ValidateAgentDevices(a.Devices)
}

// ValidateAgentDevices refuses settings the agent would refuse, so a mistake
// is reported when it is saved rather than in the agent's log.
func ValidateAgentDevices(devices []AgentDevice) error {
	if len(devices) > maxAgentDevices {
		return fmt.Errorf("an agent may read at most %d devices", maxAgentDevices)
	}
	seen := make(map[string]bool, len(devices))
	for _, d := range devices {
		if d.DeviceID == "" || len(d.DeviceID) > maxAgentDeviceID {
			return fmt.Errorf("a device needs an id of at most %d characters", maxAgentDeviceID)
		}
		if seen[d.DeviceID] {
			return fmt.Errorf("device %q has settings twice", d.DeviceID)
		}
		seen[d.DeviceID] = true
		if err := validateAgentDevice(d); err != nil {
			return fmt.Errorf("device %q: %w", d.DeviceID, err)
		}
	}
	return nil
}

func validateAgentDevice(d AgentDevice) error {
	if len(d.Name) > maxAgentNameLen {
		return fmt.Errorf("the name may be at most %d characters", maxAgentNameLen)
	}
	set := map[string]bool{"serial": d.Serial != nil, "tcp": d.TCP != nil, "udp": d.UDP != nil,
		"modbus": d.Modbus != nil, "hid": d.HID != nil}
	var err error
	switch d.Kind {
	case AgentDeviceSerial:
		err = validateAgentSerial(d.Serial)
	case AgentDeviceTCP:
		err = validateAgentTCP(d.TCP)
	case AgentDeviceUDP:
		err = validateAgentUDP(d.UDP)
	case AgentDeviceModbus:
		err = validateAgentModbus(d.Modbus, d.Serial)
		if d.Modbus != nil && d.Modbus.Transport == "rtu" {
			set["serial"] = false
		}
	case AgentDeviceHID:
		err = validateAgentHID(d.HID)
	default:
		return fmt.Errorf("unknown device kind %q (serial, tcp, udp, modbus or hid)", d.Kind)
	}
	if err != nil {
		return err
	}
	set[d.Kind] = false
	for _, other := range []string{"serial", "tcp", "udp", "modbus", "hid"} {
		if set[other] {
			return fmt.Errorf("a %s device has no %s settings", d.Kind, other)
		}
	}
	if err := validateAgentFraming(d.Framing); err != nil {
		return err
	}
	return validateAgentCommands(d.Commands)
}

func validateAgentUDP(u *AgentUDP) error {
	if u == nil {
		return errors.New("a UDP device needs an address to listen on")
	}
	if err := hostAndPort(u.Address); err != nil {
		return err
	}
	if len(u.AllowFrom) > 64 {
		return errors.New("allow_from may list at most 64 entries")
	}
	for _, e := range u.AllowFrom {
		if _, err := netip.ParsePrefix(e); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(e); err != nil {
			return fmt.Errorf("allow_from %q: not an address or CIDR range", e)
		}
	}
	return nil
}

// Modbus limits, from the protocol: one read returns at most 125 registers
// or 2000 coils, and unit ids run to 247.
const (
	maxModbusRegisters = 125
	maxModbusBits      = 2000
	maxModbusUnit      = 247
	maxModbusReads     = 64
	minModbusPollMS    = 100
)

var modbusTables = map[string]int{
	"coils": maxModbusBits, "discrete_inputs": maxModbusBits,
	"holding_registers": maxModbusRegisters, "input_registers": maxModbusRegisters,
}

func validateAgentModbus(m *AgentModbus, serial *AgentSerial) error {
	if m == nil {
		return errors.New("a Modbus device needs its Modbus settings")
	}
	switch m.Transport {
	case "tcp":
		if err := hostAndPort(m.Address); err != nil {
			return err
		}
	case "rtu":
		if m.Address != "" {
			return errors.New("a Modbus RTU device has no address: it uses its serial port")
		}
		if err := validateAgentSerial(serial); err != nil {
			return fmt.Errorf("the serial port for Modbus RTU: %w", err)
		}
	default:
		return fmt.Errorf("unknown Modbus transport %q (tcp or rtu)", m.Transport)
	}
	switch {
	case m.UnitID < 0 || m.UnitID > maxModbusUnit:
		return fmt.Errorf("the unit id must be 0 to %d", maxModbusUnit)
	case m.PollIntervalMS != 0 && (m.PollIntervalMS < minModbusPollMS || m.PollIntervalMS > 24*3600*1000):
		return fmt.Errorf("the poll interval must be %d ms to a day", minModbusPollMS)
	case m.TimeoutMS < 0 || m.TimeoutMS > 60000:
		return errors.New("the timeout must be 0 (1 s) to 60000 ms")
	case len(m.Reads) == 0 || len(m.Reads) > maxModbusReads:
		return fmt.Errorf("a Modbus device needs 1 to %d reads", maxModbusReads)
	}
	names := map[string]bool{}
	for _, r := range m.Reads {
		limit, ok := modbusTables[r.Table]
		switch {
		case r.Name == "" || len(r.Name) > maxAgentNameLen:
			return fmt.Errorf("each read needs a name of at most %d characters", maxAgentNameLen)
		case names[r.Name]:
			return fmt.Errorf("two reads are named %q", r.Name)
		case !ok:
			return fmt.Errorf("read %q: unknown table %q", r.Name, r.Table)
		case r.Count < 1 || r.Count > limit:
			return fmt.Errorf("read %q: the count must be 1 to %d", r.Name, limit)
		case r.Address < 0 || r.Address+r.Count > 65536:
			return fmt.Errorf("read %q: addresses run 0 to 65535", r.Name)
		}
		names[r.Name] = true
	}
	return nil
}

func validateAgentHID(h *AgentHID) error {
	if h == nil {
		return errors.New("a HID device needs its HID settings")
	}
	if h.Mode != "" && h.Mode != "keyboard" && h.Mode != "raw" {
		return fmt.Errorf("unknown HID mode %q (keyboard or raw)", h.Mode)
	}
	return nil
}

func validateAgentSerial(s *AgentSerial) error {
	if s == nil || s.BaudRate <= 0 {
		return errors.New("a serial device needs a baud rate")
	}
	if s.DataBits != 0 && (s.DataBits < 5 || s.DataBits > 8) {
		return fmt.Errorf("data bits %d: must be 5 to 8", s.DataBits)
	}
	switch s.Parity {
	case "", "none", "odd", "even", "mark", "space":
	default:
		return fmt.Errorf("unknown parity %q", s.Parity)
	}
	switch s.StopBits {
	case "", "1", "1.5", "2":
	default:
		return fmt.Errorf("stop bits %q: must be 1, 1.5 or 2", s.StopBits)
	}
	return nil
}

func validateAgentTCP(t *AgentTCP) error {
	if t == nil {
		return errors.New("a TCP device needs an address")
	}
	if t.Mode != "client" && t.Mode != "listen" {
		return fmt.Errorf("unknown TCP mode %q (client or listen)", t.Mode)
	}
	return hostAndPort(t.Address)
}

func hostAndPort(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address %q: %w", address, err)
	}
	// The agent refuses an empty host, which would listen on every
	// interface: the user names one, even 0.0.0.0, so it is never an accident.
	if host == "" || port == "" {
		return fmt.Errorf("address %q needs a host and a port", address)
	}
	return nil
}

func validateAgentFraming(f AgentFraming) error {
	if f.MaxRecordBytes < 0 || f.MaxRecordBytes > MaxAgentRecordBytes {
		return fmt.Errorf("the record limit must be 0 (64 KiB) to %d bytes", MaxAgentRecordBytes)
	}
	check, ok := framingChecks[f.Kind]
	if !ok {
		return fmt.Errorf("unknown framing %q", f.Kind)
	}
	return check(f)
}

// framingChecks holds what each framing needs, as the agent's framers do.
var framingChecks = map[string]func(AgentFraming) error{
	"":                    func(AgentFraming) error { return nil },
	AgentFramingLine:      func(AgentFraming) error { return nil },
	AgentFramingDelimiter: func(f AgentFraming) error { return frameBytes("the delimiter", f.DelimiterHex) },
	AgentFramingFixedLength: func(f AgentFraming) error {
		if f.Length <= 0 || f.Length > MaxAgentRecordBytes {
			return fmt.Errorf("the fixed length must be 1 to %d bytes", MaxAgentRecordBytes)
		}
		return nil
	},
	AgentFramingStartEnd: func(f AgentFraming) error {
		if err := frameBytes("the start bytes", f.StartHex); err != nil {
			return err
		}
		return frameBytes("the end bytes", f.EndHex)
	},
	AgentFramingLengthPrefix: func(f AgentFraming) error {
		if f.PrefixSize != 1 && f.PrefixSize != 2 && f.PrefixSize != 4 {
			return errors.New("the length prefix must be 1, 2 or 4 bytes")
		}
		return nil
	},
	AgentFramingIdleGap: func(f AgentFraming) error {
		if f.IdleMS <= 0 {
			return errors.New("idle-gap framing needs an idle time")
		}
		return nil
	},
}

// frameBytes checks bytes a framing matches, written in hex.
func frameBytes(name, h string) error {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) == 0 || len(b) > maxAgentFrameBytes {
		return fmt.Errorf("%s must be 1 to %d bytes written in hex", name, maxAgentFrameBytes)
	}
	return nil
}

// agentTokenPrefix starts every agent token, so one found in a log or a
// repository can be recognised and revoked.
const agentTokenPrefix = "hag_"

// NewAgentToken issues a token for the agent and returns it with its hash.
// The token names the agent, so Hermod finds the agent without searching,
// and carries 32 random bytes, which is what makes it secret.
func NewAgentToken(agentID string) (token, hash string, err error) {
	if agentID == "" || strings.Contains(agentID, "_") {
		return "", "", fmt.Errorf("agent id %q cannot be put in a token", agentID)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", fmt.Errorf("issuing an agent token: %w", err)
	}
	token = agentTokenPrefix + agentID + "_" + base64.RawURLEncoding.EncodeToString(secret)
	return token, HashAgentToken(token), nil
}

// AgentIDFromToken returns the agent a token names, and whether it is shaped
// like an agent token at all. It proves nothing: the hash has to match too.
func AgentIDFromToken(token string) (string, bool) {
	rest, ok := strings.CutPrefix(token, agentTokenPrefix)
	if !ok {
		return "", false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", false
	}
	return id, true
}

// HashAgentToken is what is stored in place of a token. The token is 32
// random bytes, so a fast hash is enough: there is nothing to guess.
func HashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AgentStore is implemented by a storage backend that can hold agents. Like
// AIBudgetStore it is separate from Storage; callers find it with a type
// assertion.
type AgentStore interface {
	// CreateAgent saves a new agent.
	CreateAgent(ctx context.Context, a Agent) error
	// GetAgent returns the agent, or ErrNotFound.
	GetAgent(ctx context.Context, id string) (Agent, error)
	// ListAgents returns the vhost's agents by name; every agent when vhost
	// is empty.
	ListAgents(ctx context.Context, vhost string) ([]Agent, error)
	// UpdateAgentDevices replaces the agent's device settings and name.
	UpdateAgentDevices(ctx context.Context, id, name string, devices []AgentDevice) error
	// UpdateAgentStatus records what the agent reported.
	UpdateAgentStatus(ctx context.Context, id string, status AgentStatus) error
	// UpdateAgentToken replaces the token hash, which revokes the old token.
	UpdateAgentToken(ctx context.Context, id, tokenHash string) error
	// DeleteAgent removes the agent; ErrNotFound when there is none.
	DeleteAgent(ctx context.Context, id string) error
	// DeleteAgents removes every agent of the vhost.
	DeleteAgents(ctx context.Context, vhost string) error
}
