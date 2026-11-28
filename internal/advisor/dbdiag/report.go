// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package dbdiag reads what is happening on a PostgreSQL database -- who holds
// its connections, what they wait on, how close it is to its limits, and what
// PgBouncer says when it sits in front -- and says it in plain words. It only
// reads: nothing is cancelled, changed or created.
package dbdiag

import "time"

// Status is how a check came out.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarn    Status = "warn"
	StatusErr     Status = "err"
	StatusUnknown Status = "unknown"
)

// Check is one thing Hermod looked at.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Value  string `json:"value"`
}

// Session is one client connection on the database. Command is the
// statement's leading keywords only: the text itself can hold personal data
// in its literals and is never reported.
type Session struct {
	PID          int     `json:"pid"`
	User         string  `json:"user"`
	App          string  `json:"app"`
	State        string  `json:"state"`
	WaitType     string  `json:"wait_type,omitempty"`
	WaitEvent    string  `json:"wait_event,omitempty"`
	Command      string  `json:"command,omitempty"`
	QuerySeconds float64 `json:"query_seconds"`
	XactSeconds  float64 `json:"xact_seconds"`
	StateSeconds float64 `json:"state_seconds"`
	BlockedBy    []int   `json:"blocked_by,omitempty"`
	// Hidden means the role may not see this session's state or query.
	Hidden bool `json:"hidden,omitempty"`
	// Hermod marks Hermod's own connections, named by application_name.
	Hermod bool `json:"hermod"`
}

// Route is how Hermod reaches the database.
type Route struct {
	ThroughPooler bool   `json:"through_pooler"`
	ServerPort    int    `json:"server_port"`
	DialedPort    int    `json:"dialed_port"`
	ServerVersion string `json:"server_version"`
}

// PoolerStats is PgBouncer's view of the pool Hermod uses, when its console
// can be read.
type PoolerStats struct {
	Readable bool `json:"readable"`
	// Reason says why it could not be read.
	Reason         string  `json:"reason,omitempty"`
	PoolSize       int     `json:"pool_size"`
	ClientActive   int     `json:"cl_active"`
	ClientWaiting  int     `json:"cl_waiting"`
	ServerActive   int     `json:"sv_active"`
	ServerIdle     int     `json:"sv_idle"`
	MaxWaitSeconds float64 `json:"maxwait_seconds"`
}

// Report is everything one diagnosis found.
type Report struct {
	Route  Route        `json:"route"`
	Checks []Check      `json:"checks"`
	Hermod []Session    `json:"hermod"`
	Others []Session    `json:"others"`
	Pooler *PoolerStats `json:"pooler,omitempty"`
	// Limited says what the role could not see, and the grant that would
	// show it. Empty when it saw everything.
	Limited   string    `json:"limited,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// snapshot is what was read from the server, before any judgement.
type snapshot struct {
	ServerVersion string
	ServerPort    int
	MaxConns      int
	Reserved      int
	InUse         int
	// RoleLimit and DBLimit are rolconnlimit and datconnlimit, -1 for none;
	// OwnUser and DBConns are the connections counted against them.
	RoleLimit, DBLimit int
	OwnUser, DBConns   int
	Sessions           []Session
	Pooler             *PoolerStats
}
