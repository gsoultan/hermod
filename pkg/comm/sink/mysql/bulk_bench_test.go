// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func benchMsgs(n int) []hermod.Message {
	msgs := make([]hermod.Message, 0, n)
	for i := range n {
		m := message.AcquireMessage()
		m.SetOperation(hermod.OpCreate)
		m.SetData("id", int64(i))
		m.SetData("name", "row-"+strconv.Itoa(i))
		m.SetData("amount", float64(i)*1.5)
		msgs = append(msgs, m)
	}
	return msgs
}

func benchTable(b *testing.B, db *sql.DB, name string) {
	b.Helper()
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+name)
	ddl := fmt.Sprintf("CREATE TABLE %s (id BIGINT NOT NULL PRIMARY KEY, name VARCHAR(255), amount DOUBLE)", name)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		b.Fatalf("create: %v", err)
	}
}

func BenchmarkMySQLWriteBatch(b *testing.B) {
	dsn := os.Getenv("MYSQL_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		b.Skip("integration")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	for _, batch := range []int{100, 500, 2000} {
		for _, mode := range []string{"bulk", "ordered"} {
			b.Run(fmt.Sprintf("rows=%d/%s", batch, mode), func(b *testing.B) {
				benchTable(b, db, "bench_target")
				s := NewMySQLSink(dsn, "bench_target", bulkIntegrationMappings(), true, "", "", "", "auto", false, false)
				defer s.Close()

				original := bulkMinRows
				if mode == "ordered" {
					bulkMinRows = 1 << 30 // never bulk
				}
				defer func() { bulkMinRows = original }()

				msgs := benchMsgs(batch)
				b.ResetTimer()
				for b.Loop() {
					if err := s.WriteBatch(context.Background(), msgs); err != nil {
						b.Fatalf("write: %v", err)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(batch)*float64(b.N)/b.Elapsed().Seconds(), "rows/s")
			})
		}
	}
}
