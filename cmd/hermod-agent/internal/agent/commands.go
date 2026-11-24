// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"context"
	"errors"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/reader"
	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// maxCommandBytes and maxReplyWait bound one command, whatever the server
// asks for.
const (
	maxCommandBytes = 64 << 10
	maxReplyWait    = 60 * time.Second
)

// command writes a server's command to a claimed device. It checks for
// itself that the device allows commands: the server's word is not enough.
func (r *readers) command(ctx context.Context, c *pb.Command) *pb.CommandResult {
	fail := func(msg string, written bool) *pb.CommandResult {
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_FAILED, Error: msg, Written: written}
	}
	r.mu.Lock()
	run := r.running[c.GetDeviceId()]
	r.mu.Unlock()
	switch {
	case run == nil:
		return fail("device "+c.GetDeviceId()+" is not claimed or not enabled on this agent", false)
	case !run.settings.GetAllowCommands():
		return fail("commands are not allowed for device "+c.GetDeviceId(), false)
	case len(c.GetPayload()) == 0:
		return fail("the command is empty", false)
	case len(c.GetPayload()) > maxCommandBytes:
		return fail("the command is larger than 64 KiB", false)
	}
	if run.modbus != nil {
		// A register or coil write; the device's echo is its answer.
		if err := run.modbus.Write(ctx, c.GetPayload()); err != nil {
			var we *reader.WrittenError
			written := errors.As(err, &we)
			r.log.Warn("command failed", "device", c.GetDeviceId(), "command", c.GetCommandId(), "written", written, "err", err)
			return fail(err.Error(), written)
		}
		r.log.Info("command written", "device", c.GetDeviceId(), "command", c.GetCommandId())
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_WRITTEN, Written: true}
	}
	wait := min(time.Duration(c.GetReplyTimeoutMs())*time.Millisecond, maxReplyWait)
	reply, err := run.link.Send(ctx, c.GetPayload(), wait)
	if err != nil {
		var we *reader.WrittenError
		written := errors.As(err, &we)
		r.log.Warn("command failed", "device", c.GetDeviceId(), "command", c.GetCommandId(), "written", written, "err", err)
		return fail(err.Error(), written)
	}
	r.log.Info("command written", "device", c.GetDeviceId(), "command", c.GetCommandId(), "bytes", len(c.GetPayload()), "replied", reply != nil)
	if wait > 0 {
		return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_REPLIED, Reply: reply, Written: true}
	}
	return &pb.CommandResult{CommandId: c.GetCommandId(), Status: pb.CommandStatus_COMMAND_STATUS_WRITTEN, Written: true}
}
