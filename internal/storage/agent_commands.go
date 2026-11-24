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
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// AgentCommand is a command a workflow may send to a device, saved on the
// device by a user. A workflow names the command; it cannot send anything
// else, and only to a device with AllowCommands on.
//
// Payload is text unless Encoding is hex. Text may use the escapes \r, \n,
// \t, \\ and \xHH, and {{field}} placeholders, which are filled from the
// message when the command is sent. Hex is the exact bytes, spaces allowed,
// with no placeholders.
type AgentCommand struct {
	Name     string `json:"name"`
	Payload  string `json:"payload"`
	Encoding string `json:"encoding,omitempty"`
	// ReplyTimeoutMS, when above zero, waits that long for the device's
	// reply, and a command with no reply in time fails.
	ReplyTimeoutMS int `json:"reply_timeout_ms,omitempty"`
}

// Command encodings.
const (
	AgentCommandText = "text"
	AgentCommandHex  = "hex"
)

// Bounds on commands; the agent enforces the same.
const (
	maxAgentCommands      = 32
	MaxAgentCommandBytes  = 64 << 10
	MaxAgentReplyMS       = 60000
	maxAgentPlaceholder   = 256
	maxAgentCommandNameLn = 64
)

var (
	agentCommandName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	agentPlaceholder = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
)

func validateAgentCommands(cmds []AgentCommand) error {
	if len(cmds) > maxAgentCommands {
		return fmt.Errorf("a device may have at most %d commands", maxAgentCommands)
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		if c.Name == "" {
			return errors.New("a command needs a name")
		}
		if len(c.Name) > maxAgentCommandNameLn || !agentCommandName.MatchString(c.Name) {
			return fmt.Errorf("command name %q: use letters, digits, _ . and - (at most %d)", c.Name, maxAgentCommandNameLn)
		}
		if seen[c.Name] {
			return fmt.Errorf("two commands are named %q", c.Name)
		}
		seen[c.Name] = true
		if err := validateAgentCommand(c); err != nil {
			return fmt.Errorf("command %q: %w", c.Name, err)
		}
	}
	return nil
}

func validateAgentCommand(c AgentCommand) error {
	if c.ReplyTimeoutMS < 0 || c.ReplyTimeoutMS > MaxAgentReplyMS {
		return fmt.Errorf("the reply wait must be 0 to %d ms", MaxAgentReplyMS)
	}
	if c.Payload == "" {
		return errors.New("the command is empty")
	}
	if len(c.Payload) > 4*MaxAgentCommandBytes {
		return errors.New("the command is larger than 64 KiB")
	}
	switch c.Encoding {
	case "", AgentCommandText:
		parts, err := parseAgentTemplate(c.Payload)
		if err != nil {
			return err
		}
		n := 0
		for _, p := range parts {
			n += len(p.literal)
		}
		if n > MaxAgentCommandBytes {
			return errors.New("the command is larger than 64 KiB")
		}
		return nil
	case AgentCommandHex:
		if strings.Contains(c.Payload, "{{") {
			return errors.New("a hex command cannot have placeholders")
		}
		b, err := decodeAgentHex(c.Payload)
		if err != nil {
			return err
		}
		if len(b) > MaxAgentCommandBytes {
			return errors.New("the command is larger than 64 KiB")
		}
		return nil
	default:
		return fmt.Errorf("unknown encoding %q (text or hex)", c.Encoding)
	}
}

func decodeAgentHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		return nil, errors.New("the hex command is not valid hex")
	}
	if len(b) == 0 {
		return nil, errors.New("the command is empty")
	}
	return b, nil
}

// templatePart is literal bytes or, when field is set, a placeholder.
type templatePart struct {
	literal []byte
	field   string
}

// parseAgentTemplate splits a text command into literal bytes, with escapes
// decoded, and placeholders.
func parseAgentTemplate(s string) ([]templatePart, error) {
	var parts []templatePart
	var lit []byte
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				return nil, errors.New("unclosed placeholder: {{ without }}")
			}
			name := strings.TrimSpace(s[i+2 : i+2+end])
			if !agentPlaceholder.MatchString(name) {
				return nil, fmt.Errorf("placeholder name %q may hold only letters, digits, underscores and dots", name)
			}
			if len(lit) > 0 {
				parts = append(parts, templatePart{literal: lit})
				lit = nil
			}
			parts = append(parts, templatePart{field: name})
			i += 2 + end + 2
		case s[i] == '\\':
			b, n, err := agentEscape(s[i:])
			if err != nil {
				return nil, err
			}
			lit = append(lit, b)
			i += n
		default:
			lit = append(lit, s[i])
			i++
		}
	}
	if len(lit) > 0 {
		parts = append(parts, templatePart{literal: lit})
	}
	return parts, nil
}

func agentEscape(s string) (byte, int, error) {
	if len(s) < 2 {
		return 0, 0, errors.New(`escape: a lone \ at the end (write \\ for a backslash)`)
	}
	switch s[1] {
	case 'r':
		return '\r', 2, nil
	case 'n':
		return '\n', 2, nil
	case 't':
		return '\t', 2, nil
	case '\\':
		return '\\', 2, nil
	case 'x':
		if len(s) >= 4 {
			if v, err := strconv.ParseUint(s[2:4], 16, 8); err == nil {
				return byte(v), 4, nil
			}
		}
		return 0, 0, errors.New(`escape: \x needs two hex digits`)
	default:
		return 0, 0, fmt.Errorf(`escape \%c: use \r, \n, \t, \\ or \xHH`, s[1])
	}
}

// RenderAgentCommand returns the bytes to write for c, with its
// placeholders filled from fields. A value must be printable text of at
// most 256 bytes: no line ending or other control character, so a value
// from a message can fill in a number or a name but never add a command.
func RenderAgentCommand(c AgentCommand, fields map[string]any) ([]byte, error) {
	if c.Encoding == AgentCommandHex {
		return decodeAgentHex(c.Payload)
	}
	parts, err := parseAgentTemplate(c.Payload)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, p := range parts {
		if p.field == "" {
			out = append(out, p.literal...)
			continue
		}
		v, ok := lookupField(fields, p.field)
		if !ok {
			return nil, fmt.Errorf("the message has no %q for the command", p.field)
		}
		s := fmt.Sprint(v)
		if err := safeAgentValue(s); err != nil {
			return nil, fmt.Errorf("%q: %w", p.field, err)
		}
		out = append(out, s...)
	}
	if len(out) > MaxAgentCommandBytes {
		return nil, errors.New("the command is larger than 64 KiB")
	}
	return out, nil
}

// lookupField finds a field, following dots into nested objects.
func lookupField(fields map[string]any, name string) (any, bool) {
	if v, ok := fields[name]; ok && v != nil {
		return v, true
	}
	var cur any = fields
	for _, key := range strings.Split(name, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[key]; !ok || cur == nil {
			return nil, false
		}
	}
	return cur, true
}

func safeAgentValue(s string) error {
	if len(s) > maxAgentPlaceholder {
		return fmt.Errorf("the value is longer than %d bytes", maxAgentPlaceholder)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return errors.New("the value has a control character such as a line ending")
		}
	}
	return nil
}

// FindCommand returns the device and the named command a workflow may send
// it, or why it may not.
func (a Agent) FindCommand(deviceID, name string) (AgentDevice, AgentCommand, error) {
	for _, d := range a.Devices {
		if d.DeviceID != deviceID {
			continue
		}
		if !d.Enabled {
			return d, AgentCommand{}, fmt.Errorf("device %q is not enabled", deviceID)
		}
		if !d.AllowCommands {
			return d, AgentCommand{}, fmt.Errorf("commands are not allowed for device %q", deviceID)
		}
		for _, c := range d.Commands {
			if c.Name == name {
				return d, c, nil
			}
		}
		return d, AgentCommand{}, fmt.Errorf("device %q has no command %q", deviceID, name)
	}
	return AgentDevice{}, AgentCommand{}, fmt.Errorf("agent has no device %q", deviceID)
}
