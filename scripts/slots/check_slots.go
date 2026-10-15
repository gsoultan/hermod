// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT slot_name, database, active FROM pg_replication_slots")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Replication Slots:")
	for rows.Next() {
		var name, database string
		var active bool
		if err := rows.Scan(&name, &database, &active); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Slot: %s, Database: %s, Active: %v\n", name, database, active)
	}
}
