//go:build integration
// +build integration

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/gsoultan/hermod/internal/storage"
)

// TestFlowRabbitMQToPostgres runs the same transformation chain as the CDC tests
// from the other source Hermod leads with:
//
//	rabbitmq_queue -> db_lookup -> data_conversion -> postgres sink
//
// It is the control case for the message-shape work. A CDC message arrives with
// an operation set and an undecoded payload, so its fields materialise on first
// read; the RabbitMQ source sets no operation and calls SetData per field as it
// reads the body (rabbitmq_queue.go:157-175), so its data map is already
// populated. Those two shapes take different paths through DefaultMessage, and
// only one of them was ever able to lose a transformation's output.
func TestFlowRabbitMQToPostgres(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	amqpURL := os.Getenv("RABBITMQ_URL")
	if amqpURL == "" {
		t.Fatal("integration: HERMOD_INTEGRATION=1 is set but RABBITMQ_URL is not")
	}
	// The lookup and the sink still need PostgreSQL; the source is the variable.
	srcDSN, sinkDSN := flowDSNs(t)
	srcDB := openFlowDB(t, srcDSN)
	sinkDB := openFlowDB(t, sinkDSN)
	provisionFlowFixtures(t, srcDB, sinkDB)

	queue := "flow_orders_q"

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		t.Fatalf("the configured RabbitMQ is not reachable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })

	// Durable, to match what the source declares. A mismatch is refused with
	// PRECONDITION_FAILED on the second declare rather than on the first, which
	// makes it look intermittent.
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		t.Fatalf("declare %s: %v", queue, err)
	}
	if _, err := ch.QueuePurge(queue, false); err != nil {
		t.Fatalf("purge %s: %v", queue, err)
	}
	mustExec(t, sinkDB, "TRUNCATE flow_orders_enriched")

	ms := &mockE2EStorage{
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
		workflows: make(map[string]storage.Workflow),
	}
	ms.sources["mq-src"] = storage.Source{
		ID: "mq-src", Name: "orders queue", Type: "rabbitmq_queue",
		Config: map[string]string{"connection_string": amqpURL, "queue_name": queue},
	}
	ms.sources["lookup-src"] = storage.Source{
		ID: "lookup-src", Name: "customers", Type: "postgres",
		Config: map[string]string{"connection_string": srcDSN, "use_cdc": "false"},
	}

	mappings, _ := json.Marshal([]map[string]any{
		{"source_field": "id", "target_column": "order_id", "is_primary_key": true},
		{"source_field": "customer_id", "target_column": "customer_id"},
		{"source_field": "customer_name", "target_column": "customer_name"},
		{"source_field": "amount", "target_column": "amount"},
		{"source_field": "qty", "target_column": "qty"},
	})
	ms.sinks["pg-sink"] = storage.Sink{
		ID: "pg-sink", Name: "enriched", Type: "postgres",
		Config: map[string]string{
			"connection_string":  sinkDSN,
			"table":              "flow_orders_enriched",
			"use_existing_table": "true",
			"column_mappings":    string(mappings),
		},
	}

	reg := NewRegistry(ms)
	wf := storage.Workflow{
		ID: "flow-wf-mq", Name: "rabbitmq to postgres", Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "n-src", Type: "source", RefID: "mq-src", Config: map[string]any{"label": "Queue"}},
			{ID: "n-lookup", Type: "transformation", Config: map[string]any{
				"transType": "db_lookup", "label": "customer name",
				"sourceId": "lookup-src", "table": "flow_customers",
				"keyColumn": "code", "keyField": "customer_id",
				"valueColumn": "name", "targetField": "customer_name",
			}},
			{ID: "n-amount", Type: "transformation", Config: map[string]any{
				"transType": "data_conversion", "label": "amount to float",
				"field": "amount", "targetType": "float",
			}},
			{ID: "n-qty", Type: "transformation", Config: map[string]any{
				"transType": "data_conversion", "label": "qty to int",
				"field": "qty", "targetType": "int",
			}},
			{ID: "n-sink", Type: "sink", RefID: "pg-sink", Config: map[string]any{
				"label": "Postgres", "sequential": false,
			}},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "n-src", TargetID: "n-lookup"},
			{SourceID: "n-lookup", TargetID: "n-amount"},
			{SourceID: "n-amount", TargetID: "n-qty"},
			{SourceID: "n-qty", TargetID: "n-sink"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	time.Sleep(2 * time.Second)

	// Spaced, so the db_lookup cache actually serves a hit for most of them: a
	// burst races past a cold cache and only exercises the query path.
	const rows = 4
	for i := 1; i <= rows; i++ {
		body := fmt.Sprintf(`{"id":"Q-%d","customer_id":"C-1","amount":"7.25","qty":"2"}`, i)
		if err := ch.PublishWithContext(t.Context(), "", queue, false, false,
			amqp.Publishing{ContentType: "application/json", Body: []byte(body)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		time.Sleep(900 * time.Millisecond)
	}

	deadline := time.Now().Add(45 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		if err := sinkDB.QueryRowContext(t.Context(),
			"SELECT count(*) FROM flow_orders_enriched").Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count >= rows {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if count < rows {
		t.Fatalf("SOURCE: only %d of %d published messages reached the sink", count, rows)
	}

	// Every row, not just the first: the enrichment is what the lookup cache used
	// to drop on every message after the one that populated it.
	var enriched, converted int
	if err := sinkDB.QueryRowContext(t.Context(),
		`SELECT count(*) FILTER (WHERE customer_name = 'ACME Corp'),
		        count(*) FILTER (WHERE amount = 7.25 AND qty = 2)
		 FROM flow_orders_enriched`).Scan(&enriched, &converted); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if enriched != count {
		t.Errorf("DB_LOOKUP: %d of %d rows carry the looked-up customer name", enriched, count)
	}
	if converted != count {
		t.Errorf("DATA_CONVERSION: %d of %d rows have amount=7.25 and qty=2 as numbers", converted, count)
	}
}
