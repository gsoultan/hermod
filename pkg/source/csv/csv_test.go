package csv

import (
	"context"
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

	ctx := context.Background()

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

	ctx := context.Background()

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

	msg, err := source.Sample(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	data := msg.Data()
	if data["id"] != "1" {
		t.Errorf("Expected id 1, got %v", data["id"])
	}
}
