// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package environment links Hermod instances as environments -- a staging
// instance and the production instance it promotes to -- and moves a workflow
// version from one to the other.
//
// Only the workflow travels. Each instance keeps its own sources, sinks and
// secrets: a promotion rewrites the workflow's connection references to the
// production connections they were matched with, and production checks every
// one exists before it stores anything. Production decides: a promotion
// arrives as a request that an admin there approves or rejects, under rules
// production sets.
package environment

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// Role is what an instance is for.
type Role string

const (
	RoleDevelopment Role = "development"
	RoleStaging     Role = "staging"
	RoleProduction  Role = "production"
)

// Valid reports whether r is one of the roles above. The empty role is
// valid: an instance nobody has described yet.
func (r Role) Valid() bool {
	switch r {
	case "", RoleDevelopment, RoleStaging, RoleProduction:
		return true
	}
	return false
}

// Instance describes this Hermod.
type Instance struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Approval policies a production instance can require.
const (
	ApproveAnyAdminExceptSender = "admin_except_sender"
	ApproveAnyAdmin             = "any_admin"
	ApproveNone                 = "none"
)

// Rules are what a receiving instance requires of a promotion.
type Rules struct {
	// Approval is one of the Approve* policies.
	Approval string `json:"approval"`
	// AutoRollback restores the previous version when the promoted one fails
	// within RollbackWindow of being deployed.
	AutoRollback bool `json:"auto_rollback"`
	// MinSoakMinutes is how long a version must have existed on the sending
	// instance before it may be promoted. 0 is no minimum.
	MinSoakMinutes int `json:"min_soak_minutes"`
	// ByVHost overrides Approval and MinSoakMinutes for promotions into one
	// vhost, keyed by its name.
	ByVHost map[string]VHostRules `json:"by_vhost,omitempty"`
}

// VHostRules are what one vhost requires instead of the instance's rules.
type VHostRules struct {
	Approval       string `json:"approval"`
	MinSoakMinutes int    `json:"min_soak_minutes"`
}

// maxVHostRules bounds how many vhosts can have rules of their own.
const maxVHostRules = 200

// For returns the rules a promotion into vhost is held to.
func (r Rules) For(vhost string) Rules {
	out := Rules{Approval: r.Approval, AutoRollback: r.AutoRollback, MinSoakMinutes: r.MinSoakMinutes}
	if v, ok := r.ByVHost[vhost]; ok {
		out.Approval, out.MinSoakMinutes = v.Approval, v.MinSoakMinutes
	}
	return out
}

// DefaultRules is what an instance enforces until an admin changes it.
func DefaultRules() Rules {
	return Rules{Approval: ApproveAnyAdminExceptSender, AutoRollback: true}
}

// Validate reports what is wrong with r.
func (r Rules) Validate() error {
	if err := validRule(r.Approval, r.MinSoakMinutes); err != nil {
		return err
	}
	if len(r.ByVHost) > maxVHostRules {
		return fmt.Errorf("at most %d vhosts can have rules of their own", maxVHostRules)
	}
	for vhost, v := range r.ByVHost {
		if err := ValidateVHostName(vhost); err != nil {
			return err
		}
		if err := validRule(v.Approval, v.MinSoakMinutes); err != nil {
			return fmt.Errorf("vhost %s: %w", vhost, err)
		}
	}
	return nil
}

func validRule(approval string, minSoak int) error {
	switch approval {
	case ApproveAnyAdminExceptSender, ApproveAnyAdmin, ApproveNone:
	default:
		return errors.New("approval must be admin_except_sender, any_admin or none")
	}
	if minSoak < 0 || minSoak > 60*24*30 {
		return errors.New("the minimum soak time must be between 0 and 30 days")
	}
	return nil
}

var vhostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ValidateVHostName checks a vhost name a link or a rule names: a letter or
// digit, then up to 63 letters, digits, dots, dashes or underscores.
func ValidateVHostName(name string) error {
	if !vhostName.MatchString(name) {
		return fmt.Errorf("%w: %q is not a vhost name; use letters, digits, dots, dashes and underscores, starting with a letter or digit", ErrInvalid, name)
	}
	return nil
}

// RollbackWindow is how long after a deploy auto-rollback watches it.
const RollbackWindow = 10 * time.Minute

// Link is an instance this one promotes to. Its token is kept apart, in an
// encrypted setting, and never leaves the server.
type Link struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Role      Role      `json:"role"`
	Version   string    `json:"version,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	// CheckedAt and Reachable record the last time the link was tried.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Reachable bool      `json:"reachable"`
	CheckNote string    `json:"check_note,omitempty"`
	// Mapping holds the connections a person matched by hand: a source or
	// sink ID here to the ID of the one that replaces it there. Connections
	// with the same name and type on both sides match without an entry.
	Mapping map[string]string `json:"mapping,omitempty"`
	// VHost is the vhost here whose workflows go through this link; each
	// vhost has at most one link. Empty on a link made before links were per
	// vhost: it serves every vhost that has no link of its own.
	VHost string `json:"vhost"`
	// TargetVHost is the vhost the workflows land in there, and
	// VHostCreated says the other instance created it when the link was made.
	TargetVHost  string `json:"target_vhost,omitempty"`
	VHostCreated bool   `json:"vhost_created,omitempty"`
}

// Peer is an instance allowed to send promotions to this one.
type Peer struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	TokenHash  string    `json:"token_hash"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	// VHost is the one vhost here it may send into. Empty on a peer linked
	// before links were per vhost, which may send into any.
	VHost string `json:"vhost,omitempty"`
	// FromVHost is the vhost it sends from, as it named it; shown, never
	// trusted.
	FromVHost string `json:"from_vhost,omitempty"`
}

// PairingCode is a one-time code an admin hands to the instance that wants
// to link to this one. Only its hash is stored.
type PairingCode struct {
	Hash      string    `json:"hash"`
	Label     string    `json:"label,omitempty"`
	VHost     string    `json:"vhost"`
	CreatedBy string    `json:"created_by"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PairingTTL is how long a pairing code can be redeemed.
const PairingTTL = 10 * time.Minute

// Promotion statuses.
const (
	StatusPending    = "pending"
	StatusDeploying  = "deploying"
	StatusRejected   = "rejected"
	StatusDeployed   = "deployed"
	StatusFailed     = "failed"
	StatusRolledBack = "rolled_back"
)

// Promotion is one workflow version sent to this instance. Its workflow is
// kept in its own setting; see repository.go.
type Promotion struct {
	ID     string `json:"id"`
	PeerID string `json:"peer_id"`
	// ReleaseID groups the promotions sent in one click. They are approved,
	// rejected and rolled back together. Empty for a promotion sent alone.
	ReleaseID    string    `json:"release_id,omitempty"`
	FromEnv      string    `json:"from_env"`
	WorkflowID   string    `json:"workflow_id"`
	WorkflowName string    `json:"workflow_name"`
	VHost        string    `json:"vhost"`
	FromVersion  int       `json:"from_version"`
	Sender       string    `json:"sender"`
	Note         string    `json:"note,omitempty"`
	Start        bool      `json:"start"`
	AutoRollback bool      `json:"auto_rollback"`
	Changes      []Change  `json:"changes,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	DecidedBy    string    `json:"decided_by,omitempty"`
	DecidedAt    time.Time `json:"decided_at,omitzero"`
	// PreviousVersion is the version this instance held before the deploy, 0
	// when the workflow is new here; AppliedVersion is the version the
	// promotion was recorded as.
	PreviousVersion int       `json:"previous_version,omitempty"`
	AppliedVersion  int       `json:"applied_version,omitempty"`
	DeployedAt      time.Time `json:"deployed_at,omitzero"`
	// ErrorsAtDeploy and ProcessedAtDeploy are the workflow's counters when
	// it was deployed, so auto-rollback judges only what came after.
	ErrorsAtDeploy    uint64 `json:"errors_at_deploy,omitempty"`
	ProcessedAtDeploy uint64 `json:"processed_at_deploy,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// Change is one difference between the version being promoted and the one
// the receiving instance runs.
type Change struct {
	Kind   string `json:"kind"` // added, removed, changed
	NodeID string `json:"node_id,omitempty"`
	What   string `json:"what"`
}

// Check is one pre-flight result shown before a promotion is sent.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	// Blocking checks stop the promotion; the others are warnings.
	Blocking bool `json:"blocking"`
}

// PeerWorkflow is the workflow a promotion carries: the stored workflow with
// this instance's runtime columns left behind and its connection references
// rewritten to the receiving instance's IDs.
type PeerWorkflow = storage.Workflow

var (
	// ErrUnsupported is returned when the storage backend has no settings.
	ErrUnsupported = errors.New("this storage backend cannot hold environment settings")
	// ErrInvalid wraps every refusal caused by the request itself.
	ErrInvalid = errors.New("invalid request")
	// ErrNotFound is returned for an unknown link, peer or promotion.
	ErrNotFound = errors.New("not found")
	// ErrBadPairingCode is returned for a code that is wrong, used or expired.
	ErrBadPairingCode = errors.New("the pairing code is wrong, already used or expired")
	// ErrForbidden is returned when the rules do not let this person decide.
	ErrForbidden = errors.New("forbidden")
	// ErrConflict is returned when a promotion is not in a state that allows
	// the action.
	ErrConflict = errors.New("conflict")
)
