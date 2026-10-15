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
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/eventstore"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

func main() {
	// 1. Initialize Database
	db, err := sql.Open("sqlite", "eventstore_example.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 2. Initialize Event Store
	store, err := eventstore.NewSQLStore(db, "sqlite")
	if err != nil {
		log.Fatal(err)
	}

	// 3. Configure Sink (Optional templates)
	store.SetTemplates("orders:{{.id}}", "Order{{.operation | title}}")

	// 4. Create an Engine to write some events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulating writing events
	go func() {
		for i := 1; i <= 5; i++ {
			msg := message.AcquireMessage()
			msg.SetID(strconv.Itoa(i))
			msg.SetTable("orders")
			msg.SetOperation("created")
			msg.SetPayload([]byte(fmt.Sprintf(`{"order_id": %d, "amount": %d}`, i, i*100)))

			if err := store.Write(ctx, msg); err != nil {
				log.Printf("Failed to write event: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// 5. Use EventStoreSource to replay/consume events
	source := eventstore.NewEventStoreSource(store, 0)
	source.SetPollInterval(1 * time.Second)
	// source.SetStreamID("orders:1") // Optional: filter by stream

	fmt.Println("Starting to read events from Event Store...")
	for range 5 {
		msg, err := source.Read(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Received Event: Stream=%s, Type=%s, Payload=%s\n",
			msg.Metadata()["eventstore_stream_id"],
			msg.Operation(),
			string(msg.Payload()))
	}

	fmt.Println("Example completed successfully.")
}
