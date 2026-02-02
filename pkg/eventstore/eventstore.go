package eventstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

const (
	MetaExpectedVersion = "eventstore_expected_version"
	MetaStreamID        = "eventstore_stream_id"
	MetaEventType       = "eventstore_event_type"
)

// Event represents a single event in the event store.
type Event struct {
	GlobalOffset int64             `json:"global_offset"`
	StreamID     string            `json:"stream_id"`
	StreamOffset int64             `json:"stream_offset"`
	EventType    string            `json:"event_type"`
	Payload      []byte            `json:"payload"`
	Metadata     map[string]string `json:"metadata"`
	Timestamp    time.Time         `json:"timestamp"`
}

// SQLStore is a database-backed implementation of an event store.
type SQLStore struct {
	db           *sql.DB
	driver       string
	logger       hermod.Logger
	streamIDTpl  string
	eventTypeTpl string
	queries      *queryRegistry
}

func NewSQLStore(db *sql.DB, driver string) (*SQLStore, error) {
	if driver == "pgx" {
		driver = "postgres"
	}
	s := &SQLStore{
		db:      db,
		driver:  driver,
		queries: newQueryRegistry(driver),
	}
	if err := s.initSchema(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLStore) SetLogger(logger hermod.Logger) {
	s.logger = logger
}

func (s *SQLStore) SetTemplates(streamID, eventType string) {
	s.streamIDTpl = streamID
	s.eventTypeTpl = eventType
}

func (s *SQLStore) initSchema(ctx context.Context) error {
	query := s.queries.get(QueryInitSchema)
	if query == "" {
		return fmt.Errorf("unsupported driver: %s", s.driver)
	}

	_, err := s.db.ExecContext(ctx, query)
	return err
}

// Write implements hermod.Sink.
func (s *SQLStore) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// WriteBatch implements hermod.BatchSink.
func (s *SQLStore) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, msg := range msgs {
		streamID := s.resolveStreamID(msg)
		eventType := s.resolveEventType(msg)

		var currentOffset int64
		expectedVersion, hasExpected := s.getExpectedVersion(msg)

		if hasExpected {
			currentOffset = expectedVersion + 1
		} else {
			var lastOffset sql.NullInt64
			queryLast := s.queries.get(QueryGetLastOffset)

			err = tx.QueryRowContext(ctx, queryLast, streamID).Scan(&lastOffset)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if lastOffset.Valid {
				currentOffset = lastOffset.Int64 + 1
			} else {
				currentOffset = 0
			}
		}

		metadata := msg.Metadata()
		if metadata == nil {
			metadata = make(map[string]string)
		}
		// Ensure hermod_table and hermod_id are in metadata for restoration
		if msg.Table() != "" {
			metadata["hermod_table"] = msg.Table()
		}
		if msg.ID() != "" {
			metadata["hermod_id"] = msg.ID()
		}

		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			return err
		}

		query := s.queries.get(QueryInsertEvent)
		args := []interface{}{streamID, currentOffset, eventType, msg.Payload(), string(metadataJSON), time.Now()}
		if s.driver == "postgres" {
			args[4] = metadataJSON
		}

		_, err = tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to insert event: %w", err)
		}
	}

	return tx.Commit()
}

func (s *SQLStore) resolveStreamID(msg hermod.Message) string {
	if sid, ok := msg.Metadata()[MetaStreamID]; ok {
		return sid
	}
	if s.streamIDTpl != "" {
		return s.render(s.streamIDTpl, msg)
	}
	streamID := fmt.Sprintf("%s:%s", msg.Table(), msg.ID())
	if msg.ID() == "" {
		streamID = msg.Table()
	}
	return streamID
}

func (s *SQLStore) resolveEventType(msg hermod.Message) string {
	if et, ok := msg.Metadata()[MetaEventType]; ok {
		return et
	}
	if s.eventTypeTpl != "" {
		return s.render(s.eventTypeTpl, msg)
	}
	return string(msg.Operation())
}

func (s *SQLStore) render(tplStr string, msg hermod.Message) string {
	if !strings.Contains(tplStr, "{{") {
		return tplStr
	}
	tmpl, err := template.New("tpl").Parse(tplStr)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("failed to parse template", "tpl", tplStr, "error", err)
		}
		return tplStr
	}
	var buf bytes.Buffer
	data := map[string]interface{}{
		"id":        msg.ID(),
		"table":     msg.Table(),
		"operation": msg.Operation(),
		"metadata":  msg.Metadata(),
		"payload":   string(msg.Payload()),
	}
	// Add message data fields
	for k, v := range msg.Data() {
		data[k] = v
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		if s.logger != nil {
			s.logger.Error("failed to execute template", "tpl", tplStr, "error", err)
		}
		return tplStr
	}
	return buf.String()
}

func (s *SQLStore) getExpectedVersion(msg hermod.Message) (int64, bool) {
	if v, ok := msg.Metadata()[MetaExpectedVersion]; ok {
		var iv int64
		if _, err := fmt.Sscanf(v, "%d", &iv); err == nil {
			return iv, true
		}
	}
	return 0, false
}

func (s *SQLStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLStore) Close() error {
	return nil // We don't close the DB here as it's passed in
}

// EventStoreSource implements hermod.Source for replaying events.
type EventStoreSource struct {
	store      *SQLStore
	lastOffset int64
	mu         sync.Mutex
	batchSize  int
	buffer     []hermod.Message
	streamID   string
	pollInt    time.Duration
}

func NewEventStoreSource(store *SQLStore, fromOffset int64) *EventStoreSource {
	return &EventStoreSource{
		store:      store,
		lastOffset: fromOffset,
		batchSize:  100,
		pollInt:    1 * time.Second,
	}
}

func (s *EventStoreSource) SetStreamID(streamID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamID = streamID
}

func (s *EventStoreSource) SetPollInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pollInt = interval
}

func (s *EventStoreSource) Read(ctx context.Context) (hermod.Message, error) {
	for {
		s.mu.Lock()
		if len(s.buffer) > 0 {
			msg := s.buffer[0]
			s.buffer = s.buffer[1:]
			if offStr, ok := msg.Metadata()["eventstore_global_offset"]; ok {
				var offset int64
				if _, err := fmt.Sscanf(offStr, "%d", &offset); err == nil {
					s.lastOffset = offset
				}
			}
			s.mu.Unlock()
			return msg, nil
		}

		events, err := s.store.ReadAll(ctx, s.lastOffset+1, s.batchSize, s.streamID)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}

		if len(events) > 0 {
			for _, e := range events {
				msg := s.toMessage(e)
				s.buffer = append(s.buffer, msg)
			}
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.pollInt):
			continue
		}
	}
}

func (s *EventStoreSource) GetState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]string{
		"last_offset": fmt.Sprintf("%d", s.lastOffset),
	}
}

func (s *EventStoreSource) SetState(state map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := state["last_offset"]; ok {
		var offset int64
		if _, err := fmt.Sscanf(v, "%d", &offset); err == nil {
			s.lastOffset = offset
			s.buffer = nil // Clear buffer to restart from new offset
		}
	}
}

func (s *EventStoreSource) toMessage(e Event) hermod.Message {
	msg := message.AcquireMessage()
	// Extract table and ID from streamID "table:id"
	// This is a bit brittle, but works for our simple implementation.
	// In a real system, we'd store table and ID separately.
	msg.SetOperation(hermod.Operation(e.EventType))
	msg.SetPayload(e.Payload)
	for k, v := range e.Metadata {
		msg.SetMetadata(k, v)
	}

	// Try to restore table and ID from metadata if they exist
	if table, ok := e.Metadata["hermod_table"]; ok {
		msg.SetTable(table)
	}
	if id, ok := e.Metadata["hermod_id"]; ok {
		msg.SetID(id)
	}

	// Add some internal metadata
	msg.SetMetadata("eventstore_global_offset", fmt.Sprintf("%d", e.GlobalOffset))
	msg.SetMetadata("eventstore_stream_id", e.StreamID)
	msg.SetMetadata("eventstore_stream_offset", fmt.Sprintf("%d", e.StreamOffset))

	return msg
}

func (s *EventStoreSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *EventStoreSource) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

func (s *EventStoreSource) Close() error {
	return nil
}

func (s *SQLStore) ReadAll(ctx context.Context, fromOffset int64, limit int, streamID string) ([]Event, error) {
	var query string
	var args []interface{}

	query = s.queries.get(QueryReadAll)
	if streamID != "" {
		query += " AND stream_id = ?"
		args = append(args, streamID)
	}

	query += " AND global_offset >= ? ORDER BY global_offset ASC"
	args = append(args, fromOffset)

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	// Adjust placeholders for postgres
	if s.driver == "postgres" {
		for i := 1; ; i++ {
			if !strings.Contains(query, "?") {
				break
			}
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", i), 1)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var metadataRaw []byte
		if err := rows.Scan(&e.GlobalOffset, &e.StreamID, &e.StreamOffset, &e.EventType, &e.Payload, &metadataRaw, &e.Timestamp); err != nil {
			return nil, err
		}
		if len(metadataRaw) > 0 {
			if err := json.Unmarshal(metadataRaw, &e.Metadata); err != nil {
				return nil, err
			}
		}
		events = append(events, e)
	}
	return events, nil
}
