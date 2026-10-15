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
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var currentLSN string
	err = db.QueryRow("SELECT pg_current_wal_lsn()").Scan(&currentLSN)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Current WAL LSN: %s\n", currentLSN)

	var count int
	err = db.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Users table row count: %d\n", count)

	var slotName, database, confirmedFlush string
	err = db.QueryRow("SELECT slot_name, database, confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name = 'cdc_slot_2'").Scan(&slotName, &database, &confirmedFlush)
	if err != nil {
		log.Printf("Slot cdc_slot_2 not found: %v", err)
	} else {
		fmt.Printf("Slot: %s, Database: %s, Confirmed Flush LSN: %s\n", slotName, database, confirmedFlush)
	}
}
