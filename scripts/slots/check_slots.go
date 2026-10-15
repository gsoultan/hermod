// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
