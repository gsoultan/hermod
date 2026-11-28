// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package dbactivity

import (
	"strings"
	"sync"
)

var (
	databases sync.Map // key -> *Database
	pools     sync.Map // pool (a *sql.DB, a *pgxpool.Pool, ...) -> *Database
)

// Key names a physical database the way its server sees it. Any driver can
// use it; empty parts are kept, so two keys only match when every part does.
func Key(driver, host, port, database, user string) string {
	return strings.Join([]string{strings.ToLower(driver), strings.ToLower(host), port, database, user}, "|")
}

// For is the Database for key, created on first use. Every caller with the
// same key shares it for the life of the process.
func For(key string) *Database {
	v, ok := databases.Load(key)
	if !ok {
		v, _ = databases.LoadOrStore(key, newDatabase(key))
	}
	d, _ := v.(*Database)
	return d
}

// Bind records that pool reaches d, so a step holding only the pool can find
// what else is running on its database.
func Bind(pool any, d *Database) {
	if pool != nil && d != nil {
		pools.Store(pool, d)
	}
}

// Unbind forgets a pool that has been closed.
func Unbind(pool any) {
	if pool != nil {
		pools.Delete(pool)
	}
}

// OfPool is the Database pool was bound to, nil when it was not.
func OfPool(pool any) *Database {
	if pool == nil {
		return nil
	}
	v, _ := pools.Load(pool)
	d, _ := v.(*Database)
	return d
}
