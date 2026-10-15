// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package file

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestCSVSource(t *testing.T) {
	// Create a temporary CSV file
	content := "id,name,age\n1,John,30\n2,Jane,25\n"
	tmpfile, err := os.CreateTemp("", "test*.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	source := NewCSVSource(tmpfile.Name(), ',', true)
	defer source.Close()

	ctx := t.Context()

	// Read first record
	msg1, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read first record: %v", err)
	}
	if msg1 == nil {
		t.Fatal("Expected first message, got nil")
	}

	data1 := msg1.Data()
	if data1["id"] != "1" || data1["name"] != "John" || data1["age"] != "30" {
		t.Errorf("Unexpected data in first message: %v", data1)
	}

	// Read second record
	msg2, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read second record: %v", err)
	}
	if msg2 == nil {
		t.Fatal("Expected second message, got nil")
	}

	data2 := msg2.Data()
	if data2["id"] != "2" || data2["name"] != "Jane" || data2["age"] != "25" {
		t.Errorf("Unexpected data in second message: %v", data2)
	}

	// Read EOF
	msg3, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Expected no error on EOF, got: %v", err)
	}
	if msg3 != nil {
		t.Fatal("Expected nil message on EOF")
	}
}

func TestCSVSourceNoHeader(t *testing.T) {
	// Create a temporary CSV file without header
	content := "1,John,30\n2,Jane,25\n"
	tmpfile, err := os.CreateTemp("", "test_no_header*.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	source := NewCSVSource(tmpfile.Name(), ',', false)
	defer source.Close()

	ctx := t.Context()

	msg1, err := source.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data1 := msg1.Data()
	if data1["column_0"] != "1" || data1["column_1"] != "John" || data1["column_2"] != "30" {
		t.Errorf("Unexpected data: %v", data1)
	}
}

func TestCSVSourceSample(t *testing.T) {
	content := "id,name,age\n1,John,30\n2,Jane,25\n"
	tmpfile, err := os.CreateTemp("", "test_sample*.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	tmpfile.Write([]byte(content))
	tmpfile.Close()

	source := NewCSVSource(tmpfile.Name(), ',', true)
	defer source.Close()

	msg, err := source.Sample(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}

	data := msg.Data()
	if data["id"] != "1" {
		t.Errorf("Expected id 1, got %v", data["id"])
	}
}

func TestHTTPCSVSource(t *testing.T) {
	// Create a mock http server
	content := "id,name,age\n1,John,30\n2,Jane,25\n"
	server := http.NewServeMux()
	server.HandleFunc("/data.csv", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "value" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Write([]byte(content))
	})

	ts := httptest.NewServer(server)
	defer ts.Close()

	headers := map[string]string{"X-Test": "value"}
	source := NewHTTPCSVSource(ts.URL+"/data.csv", ',', true, headers)
	defer source.Close()

	ctx := t.Context()
	msg, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read from http: %v", err)
	}

	if msg == nil {
		t.Fatal("Expected message, got nil")
	}

	data := msg.Data()
	if data["name"] != "John" {
		t.Errorf("Expected John, got %v", data["name"])
	}
}
