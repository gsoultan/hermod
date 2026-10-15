// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package state

import (
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
)

type Config struct {
	Type     string
	Path     string
	Address  string
	Password string
	DB       int
	Prefix   string
}

func NewStateStore(cfg Config) (hermod.StateStore, error) {
	switch cfg.Type {
	case "sqlite":
		if cfg.Path == "" {
			cfg.Path = "hermod_state.db"
		}
		return NewSQLiteStateStore(cfg.Path)
	case "redis":
		return NewRedisStateStore(cfg.Address, cfg.Password, cfg.DB, cfg.Prefix, 0), nil
	case "etcd":
		return NewEtcdStateStore(splitEndpoints(cfg.Address), cfg.Prefix, 5*time.Second)
	case "memory":
		return NewSQLiteStateStore(":memory:")
	case "":
		return NewSQLiteStateStore("hermod_state.db")
	default:
		return nil, fmt.Errorf("unsupported state store type: %s", cfg.Type)
	}
}

// splitEndpoints turns a comma-separated address into a list of endpoints.
//
// etcd is a quorum, and Address was wrapped in a one-element slice, so only one
// member of it could ever be named. That leaves the state store unavailable
// whenever that member is, which removes the reason to have chosen etcd — and
// an operator who wrote the list the way every etcd tool accepts one got a
// single endpoint containing commas, which resolves to nothing.
//
// A single address still works and means what it did before.
func splitEndpoints(address string) []string {
	parts := strings.Split(address, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func NewMemoryStore() hermod.StateStore {
	store, _ := NewSQLiteStateStore(":memory:")
	return store
}
