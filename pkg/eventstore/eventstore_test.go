package eventstore

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
)

func TestDriverNormalization(t *testing.T) {
	dbPath := "test_norm.db"
	defer os.Remove(dbPath)
	db, _ := sql.Open("sqlite", dbPath)
	defer db.Close()

	// "pgx" should be normalized to "postgres"
	s, err := NewSQLStore(db, "pgx")
	if err != nil {
		// It might fail because we are using sqlite DB with postgres schema init
		// but we want to check if the error is "unsupported driver"
		if err.Error() == "unsupported driver: pgx" {
			t.Errorf("driver pgx was not normalized")
		}
	} else if s.driver != "postgres" {
		t.Errorf("expected driver to be normalized to postgres, got %s", s.driver)
	}
}

func TestEventStore(t *testing.T) {
	dbPath := "test_eventstore.db"
	defer os.Remove(dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewSQLStore(db, "sqlite")
	if err != nil {
		t.Fatalf("failed to create SQLStore: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Test Write (Sink)
	msg := message.AcquireMessage()
	msg.SetID("123")
	msg.SetTable("orders")
	msg.SetPayload([]byte(`{"amount": 100}`))
	msg.SetMetadata("customer", "alice")

	if err := store.Write(ctx, msg); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	// 2. Test ExpectedVersion (Optimistic Concurrency)
	msg2 := message.AcquireMessage()
	msg2.SetID("123")
	msg2.SetTable("orders")
	msg2.SetPayload([]byte(`{"amount": 200}`))
	msg2.SetMetadata(MetaExpectedVersion, "0") // Previous was 0

	if err := store.Write(ctx, msg2); err != nil {
		t.Fatalf("failed to write with expected version: %v", err)
	}

	// This should FAIL because expected version is wrong (should be 1 now)
	msg3 := message.AcquireMessage()
	msg3.SetID("123")
	msg3.SetTable("orders")
	msg3.SetPayload([]byte(`{"amount": 300}`))
	msg3.SetMetadata(MetaExpectedVersion, "0")

	if err := store.Write(ctx, msg3); err == nil {
		t.Error("expected failure with wrong expected version, but it succeeded")
	}

	// 3. Test Batch Write
	msgB1 := message.AcquireMessage()
	msgB1.SetMetadata(MetaStreamID, "batch-stream")
	msgB1.SetPayload([]byte("b1"))

	msgB2 := message.AcquireMessage()
	msgB2.SetMetadata(MetaStreamID, "batch-stream")
	msgB2.SetPayload([]byte("b2"))

	if err := store.WriteBatch(ctx, []hermod.Message{msgB1, msgB2}); err != nil {
		t.Fatalf("failed to write batch: %v", err)
	}

	// 4. Test Read (Source) with Polling
	source := NewEventStoreSource(store, 0)
	source.SetPollInterval(100 * time.Millisecond)

	// Read first message
	readMsg, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message 1: %v", err)
	}
	if string(readMsg.Payload()) != `{"amount": 100}` {
		t.Errorf("expected payload %s, got %s", `{"amount": 100}`, string(readMsg.Payload()))
	}

	// Read second message
	readMsg2, err := source.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message 2: %v", err)
	}
	if string(readMsg2.Payload()) != `{"amount": 200}` {
		t.Errorf("expected 200, got %s", string(readMsg2.Payload()))
	}

	// 5. Test Filtering by Stream ID
	sourceFiltered := NewEventStoreSource(store, 0)
	sourceFiltered.SetStreamID("batch-stream")

	readMsgB1, err := sourceFiltered.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read batch message 1: %v", err)
	}
	if string(readMsgB1.Payload()) != "b1" {
		t.Errorf("expected b1, got %s", string(readMsgB1.Payload()))
	}

	// 6. Test Stateful
	state := sourceFiltered.GetState()
	if state["last_offset"] == "" {
		t.Error("expected last_offset in state")
	}

	sourceNew := NewEventStoreSource(store, 0)
	sourceNew.SetStreamID("batch-stream")
	sourceNew.SetState(state)

	readMsgB2, err := sourceNew.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read batch message 2 after state restore: %v", err)
	}
	if string(readMsgB2.Payload()) != "b2" {
		t.Errorf("expected b2, got %s", string(readMsgB2.Payload()))
	}
}
