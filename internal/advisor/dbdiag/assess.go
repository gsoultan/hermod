// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbdiag

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	// longQuerySeconds is how long a statement runs before it is named.
	longQuerySeconds = 300
	// idleTxSeconds is how long a transaction may sit idle before it is.
	idleTxSeconds = 60
)

// assess turns a snapshot into a report. dialedPort is the port Hermod
// connects to; a server listening on another one is behind a proxy.
func assess(s snapshot, dialedPort int) Report {
	for i := range s.Sessions {
		s.Sessions[i].Hermod = strings.HasPrefix(s.Sessions[i].App, "hermod")
	}
	r := Report{
		Route: Route{
			ServerVersion: s.ServerVersion, ServerPort: s.ServerPort, DialedPort: dialedPort,
			ThroughPooler: dialedPort > 0 && s.ServerPort > 0 && dialedPort != s.ServerPort,
		},
		Pooler: s.Pooler,
	}
	byPID := map[int]Session{}
	for _, ss := range s.Sessions {
		byPID[ss.PID] = ss
		if ss.Hermod {
			r.Hermod = append(r.Hermod, ss)
		} else if !ss.Hidden {
			r.Others = append(r.Others, ss)
		}
	}
	longestFirst := func(a, b Session) int { return cmp.Compare(b.QuerySeconds, a.QuerySeconds) }
	slices.SortFunc(r.Hermod, longestFirst)
	slices.SortFunc(r.Others, longestFirst)

	r.Checks = []Check{
		locksCheck(s.Sessions, byPID),
		longQueriesCheck(s.Sessions),
		waitsCheck(s.Sessions),
		idleTxCheck(s.Sessions),
		limitsCheck(s),
	}
	if r.Route.ThroughPooler || s.Pooler != nil {
		r.Checks = append(r.Checks, poolerCheck(s.Pooler))
	}
	r.Limited = limited(s.Sessions)
	return r
}

func who(ss Session) string {
	if ss.Hermod {
		if ss.App == "hermod" {
			return "Hermod"
		}
		return "Hermod (" + ss.App + ")"
	}
	name := ss.User
	if ss.App != "" {
		name += " · " + ss.App
	}
	return name
}

func doing(ss Session) string {
	if ss.Command == "" {
		return who(ss)
	}
	return who(ss) + ", " + ss.Command
}

func locksCheck(sessions []Session, byPID map[int]Session) Check {
	c := Check{ID: "locks", Title: "Locks", Status: StatusOK, Detail: "Nothing is waiting on a lock.", Value: "0 waiting"}
	var lines []string
	for _, ss := range sessions {
		if ss.WaitType != "Lock" {
			continue
		}
		line := fmt.Sprintf("%s has waited %s", doing(ss), duration(ss.QuerySeconds))
		if on := holders(ss.BlockedBy, byPID); on != "" {
			line += " on " + on
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		c.Status = StatusWarn
		c.Value = fmt.Sprintf("%d waiting", len(lines))
		c.Detail = strings.Join(lines, "; ") + "."
	}
	return c
}

// holders names the sessions a waiter is blocked by: one by what it runs, more
// as a count, with the longest-running one named.
func holders(pids []int, byPID map[int]Session) string {
	var hs []Session
	for _, pid := range pids {
		if h, ok := byPID[pid]; ok {
			hs = append(hs, h)
		}
	}
	switch len(hs) {
	case 0:
		return ""
	case 1:
		return doing(hs[0]) + " (in a transaction for " + duration(hs[0].XactSeconds) + ")"
	}
	slices.SortFunc(hs, func(a, b Session) int { return cmp.Compare(b.XactSeconds, a.XactSeconds) })
	hermod := 0
	for _, h := range hs {
		if h.Hermod {
			hermod++
		}
	}
	whose := fmt.Sprintf("%d sessions", len(hs))
	if hermod == len(hs) {
		whose = fmt.Sprintf("%d of Hermod's queries", len(hs))
	}
	return fmt.Sprintf("%s, the longest %s (in a transaction for %s)", whose, doing(hs[0]), duration(hs[0].XactSeconds))
}

func longQueriesCheck(sessions []Session) Check {
	c := Check{ID: "long_queries", Title: "Long queries", Status: StatusOK, Detail: "Nothing has run for more than 5 minutes.", Value: "0"}
	var long []Session
	hermod := 0
	for _, ss := range sessions {
		if ss.State == "active" && ss.QuerySeconds >= longQuerySeconds {
			long = append(long, ss)
			if ss.Hermod {
				hermod++
			}
		}
	}
	if len(long) == 0 {
		return c
	}
	slices.SortFunc(long, func(a, b Session) int { return cmp.Compare(b.QuerySeconds, a.QuerySeconds) })
	c.Status = StatusWarn
	c.Value = strconv.Itoa(len(long))
	c.Detail = fmt.Sprintf("%d running for more than 5 minutes", len(long))
	if hermod > 0 {
		c.Detail += fmt.Sprintf(", %d of them Hermod's", hermod)
	}
	c.Detail += fmt.Sprintf(". Longest: %s, %s. Each one holds a connection for as long as it runs.", doing(long[0]), duration(long[0].QuerySeconds))
	return c
}

func waitsCheck(sessions []Session) Check {
	c := Check{ID: "waits", Title: "What queries wait on", Status: StatusOK, Detail: "Nothing is running right now.", Value: "idle"}
	counts := map[string]int{}
	active := 0
	for _, ss := range sessions {
		if ss.State != "active" {
			continue
		}
		active++
		key := "running on CPU"
		if ss.WaitType != "" {
			key = ss.WaitType + ": " + ss.WaitEvent
		}
		counts[key]++
	}
	if active == 0 {
		return c
	}
	top, n := "", 0
	for k, v := range counts {
		if v > n || (v == n && k < top) {
			top, n = k, v
		}
	}
	c.Value = strings.SplitN(top, ":", 2)[0]
	c.Detail = fmt.Sprintf("%d of %d active: %s.", n, active, top)
	switch {
	case strings.HasPrefix(top, "Lock"):
		c.Status = StatusWarn
		c.Detail += " Most of the work is waiting on other sessions, not running."
	case strings.HasPrefix(top, "IO"):
		c.Detail += " The server is busy reading or writing, not stuck."
	case strings.HasPrefix(top, "Client"):
		c.Detail += " Waiting on the client to read or send; the database is not the slow part."
	}
	return c
}

func idleTxCheck(sessions []Session) Check {
	c := Check{ID: "idle_in_transaction", Title: "Idle in transaction", Status: StatusOK, Detail: "No session is holding a transaction open.", Value: "0"}
	var lines []string
	for _, ss := range sessions {
		if strings.HasPrefix(ss.State, "idle in transaction") && ss.StateSeconds >= idleTxSeconds {
			lines = append(lines, fmt.Sprintf("%s, idle for %s", who(ss), duration(ss.StateSeconds)))
		}
	}
	if len(lines) > 0 {
		c.Status = StatusWarn
		c.Value = strconv.Itoa(len(lines))
		c.Detail = strings.Join(lines, "; ") + ". An open transaction holds its locks and stops vacuum from cleaning up."
	}
	return c
}

func limitsCheck(s snapshot) Check {
	avail := s.MaxConns - s.Reserved
	c := Check{ID: "limits", Title: "Server limits", Status: StatusOK, Value: fmt.Sprintf("%d / %d", s.InUse, s.MaxConns)}
	c.Detail = fmt.Sprintf("max_connections %d, %d in use.", s.MaxConns, s.InUse)
	c.Status = level(s.InUse, avail)
	if s.RoleLimit >= 0 {
		c.Detail += fmt.Sprintf(" This role may open %d and has %d.", s.RoleLimit, s.OwnUser)
		c.Status = worst(c.Status, level(s.OwnUser, s.RoleLimit))
	}
	if s.DBLimit >= 0 {
		c.Detail += fmt.Sprintf(" This database allows %d and has %d.", s.DBLimit, s.DBConns)
		c.Status = worst(c.Status, level(s.DBConns, s.DBLimit))
	}
	if s.RoleLimit < 0 && s.DBLimit < 0 {
		c.Detail += " Role and database have no limit of their own."
	}
	if c.Status != StatusOK {
		c.Detail += " New connections are refused at the limit."
	}
	return c
}

func level(used, limit int) Status {
	switch {
	case limit <= 0:
		return StatusOK
	case float64(used) >= 0.95*float64(limit):
		return StatusErr
	case float64(used) >= 0.8*float64(limit):
		return StatusWarn
	}
	return StatusOK
}

func worst(a, b Status) Status {
	rank := map[Status]int{StatusOK: 0, StatusUnknown: 1, StatusWarn: 2, StatusErr: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func poolerCheck(p *PoolerStats) Check {
	c := Check{ID: "pooler", Title: "PgBouncer pool", Status: StatusUnknown, Value: "Not readable"}
	switch {
	case p == nil:
		c.Detail = "Not checked."
	case !p.Readable:
		c.Detail = p.Reason + ". Hermod measures its own share of connections instead, so nothing depends on this. " +
			"Adding this user to PgBouncer's stats_users would also show other clients waiting."
	case p.ClientWaiting > 0:
		c.Status = StatusErr
		c.Value = fmt.Sprintf("%d waiting", p.ClientWaiting)
		c.Detail = fmt.Sprintf("%d clients waiting for a server connection, the longest for %s. %d of %d server connections busy, %d idle.",
			p.ClientWaiting, duration(p.MaxWaitSeconds), p.ServerActive, p.PoolSize, p.ServerIdle)
	default:
		c.Status = StatusOK
		c.Value = fmt.Sprintf("%d / %d", p.ServerActive, p.PoolSize)
		c.Detail = fmt.Sprintf("Nobody waiting. %d of %d server connections busy, %d idle.", p.ServerActive, p.PoolSize, p.ServerIdle)
	}
	return c
}

func limited(sessions []Session) string {
	n := 0
	for _, ss := range sessions {
		if ss.Hidden {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d sessions of other users hide what they are doing from this role, so the checks above may miss them. "+
		"Granting the role pg_monitor (or pg_read_all_stats) lets Hermod see them; it gives read access to statistics only.", n)
}

// duration says a number of seconds the way a person would.
func duration(sec float64) string {
	switch {
	case sec < 1:
		return fmt.Sprintf("%d ms", int(sec*1000))
	case sec < 60:
		return fmt.Sprintf("%d s", int(sec))
	case sec < 3600:
		return fmt.Sprintf("%d min", int(sec/60))
	}
	h := int(sec / 3600)
	if m := int(sec/60) % 60; m > 0 {
		return fmt.Sprintf("%d h %d min", h, m)
	}
	return fmt.Sprintf("%d h", h)
}
