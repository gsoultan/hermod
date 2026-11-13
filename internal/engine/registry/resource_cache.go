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
	"maps"

	"github.com/gsoultan/hermod/internal/storage"
)

// ForgetChangedResources drops each cached source and sink whose stored record
// no longer matches the cached copy in its vhost, type or config, so the next
// read takes it from storage. Records missing from the lists are left alone:
// a list can be partial, and an absent record is no evidence of a change.
//
// The cache is only emptied by an edit made through this registry, and a
// vhost rename moves every source and sink to the new name in storage without
// one. A workflow restarted under the new name then built its connectors from
// the cached copies, still under the old name, so any connector reading a vhost
// secret looked under a name that held none, the restart failed and the
// workflow stayed stopped. The same is true of an edit made on the control
// plane for a worker running in its own process. The worker calls this with
// the records it reads each sync, before it compares and restarts workflows.
func (r *Registry) ForgetChangedResources(sources map[string]storage.Source, sinks map[string]storage.Sink) {
	r.sourceCacheMu.Lock()
	defer r.sourceCacheMu.Unlock()
	for id, cached := range r.sourceCache {
		if stored, ok := sources[id]; ok && connectorChanged(cached.VHost, cached.Type, cached.Config, stored.VHost, stored.Type, stored.Config) {
			delete(r.sourceCache, id)
		}
	}
	for id, cached := range r.sinkCache {
		if stored, ok := sinks[id]; ok && connectorChanged(cached.VHost, cached.Type, cached.Config, stored.VHost, stored.Type, stored.Config) {
			delete(r.sinkCache, id)
		}
	}
}

// connectorChanged compares only what a connector is built from. A source's
// state and status change while it runs and are no reason to read it again.
func connectorChanged(vhost, typ string, config map[string]string, nextVHost, nextType string, nextConfig map[string]string) bool {
	return vhost != nextVHost || typ != nextType || !maps.Equal(config, nextConfig)
}
