// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
