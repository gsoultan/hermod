// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/hermod_sample_db_2?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var pubName string
	err = db.QueryRow("SELECT pubname FROM pg_publication WHERE pubname = 'cdc_slot_2'").Scan(&pubName)
	if err != nil {
		log.Printf("Publication cdc_slot_2 not found: %v", err)
	} else {
		fmt.Printf("Publication %s exists\n", pubName)
	}

	rows, err := db.Query("SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = 'cdc_slot_2'")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("Tables in publication:")
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("- %s.%s\n", schema, table)
	}
}
