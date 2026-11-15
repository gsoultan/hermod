// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

// checkStepSecrets refuses a workflow one of whose steps reads a secret or an
// env variable that answers nothing, and gives no fallback for it.
//
// An expression has nowhere to report a failure, so secret("API_KEY") for a
// secret that does not exist evaluated to an empty string: the header went out
// as "Authorization: Bearer ", for every message, and nothing was logged. A
// connector's setting that names a missing secret already fails the connector
// (resolveSecrets). This is the same rule for a step, applied before the
// workflow starts rather than once per message.
//
// A reference with a fallback, secret("API_KEY", "anonymous"), is left alone:
// it answers the fallback. So is a lookup that fails outright, such as a
// secret manager that cannot be reached: that says nothing about whether the
// secret exists, and refusing to start every workflow while Vault restarts
// would turn one outage into two. Source and sink nodes carry no settings of
// their own to read; their connectors are checked when they are built.
func (r *Registry) checkStepSecrets(ctx context.Context, wf storage.Workflow) error {
	mgr := r.vhostSecrets()
	var errs []error
	for _, node := range wf.Nodes {
		if node.Type == "source" || node.Type == "sink" {
			continue
		}
		var missing []string
		walkConfigStrings(node.Config, func(value string) {
			for _, ref := range secrets.RequiredReferences(value) {
				if name := ref.Kind + " " + ref.Name; !slices.Contains(missing, name) && answersNothing(ctx, mgr, wf.VHost, ref) {
					missing = append(missing, name)
				}
			}
		})
		if len(missing) == 0 {
			continue
		}
		slices.Sort(missing)
		errs = append(errs, fmt.Errorf("step %s reads %v, which vhost %q does not have: "+
			"add it under the vhost's secrets, or give the reference a fallback such as secret(\"NAME\", \"default\")",
			node.ID, missing, wf.VHost))
	}
	return errors.Join(errs...)
}

// answersNothing reports whether ref resolves to an empty value for vhost. A
// lookup that fails is not an answer.
func answersNothing(ctx context.Context, mgr secrets.ScopedManager, vhost string, ref secrets.Ref) bool {
	var (
		value string
		err   error
	)
	if ref.Kind == "env" {
		value, err = secrets.ReadEnv(ctx, mgr, vhost, ref.Name)
	} else {
		value, err = mgr.GetScoped(ctx, vhost, ref.Name)
	}
	return err == nil && value == ""
}

// walkConfigStrings calls visit with every string in a node's settings, at
// any depth. The engine's own caches, written under a leading underscore, are
// not settings.
func walkConfigStrings(v any, visit func(string)) {
	switch x := v.(type) {
	case string:
		visit(x)
	case map[string]any:
		for key, item := range x {
			if len(key) > 0 && key[0] == '_' {
				continue
			}
			walkConfigStrings(item, visit)
		}
	case []any:
		for _, item := range x {
			walkConfigStrings(item, visit)
		}
	}
}
