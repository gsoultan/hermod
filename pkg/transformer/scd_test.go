package transformer

import (
	"context"
	"database/sql"
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/message"
	_ "modernc.org/sqlite"
)

type mockRegistry struct {
	db  *sql.DB
	src storage.Source
}

func (m *mockRegistry) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return m.src, nil
}
func (m *mockRegistry) GetOrOpenDB(src storage.Source) (*sql.DB, error) {
	return m.db, nil
}

func TestSCDTransformer_AllTypes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create table
	_, err = db.Exec(`CREATE TABLE dim_users (
		id INTEGER,
		email TEXT,
		name TEXT,
		address TEXT,
		start_date DATETIME,
		end_date DATETIME,
		is_current BOOLEAN,
		old_email TEXT,
		department TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`CREATE TABLE dim_users_history (
		id INTEGER,
		email TEXT,
		name TEXT,
		address TEXT,
		start_date DATETIME,
		end_date DATETIME,
		is_current BOOLEAN,
		old_email TEXT,
		department TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}

	tr := &SCDTransformer{}
	reg := &mockRegistry{
		db: db,
		src: storage.Source{
			ID:   "test",
			Type: "sqlite",
		},
	}
	ctx := context.WithValue(context.Background(), "registry", reg)

	// Test Type 0 (Fixed)
	t.Run("Type 0", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetData("id", 100)
		msg.SetData("email", "user100@example.com")

		config := map[string]interface{}{
			"scdType":        0,
			"targetSourceId": "test",
			"targetTable":    "dim_users",
			"businessKeys":   []string{"id"},
		}

		_, err := tr.Transform(ctx, msg, config)
		if err != nil {
			t.Fatal(err)
		}

		var email string
		db.QueryRow("SELECT email FROM dim_users WHERE id = 100").Scan(&email)
		if email != "user100@example.com" {
			t.Errorf("Expected insert, got %s", email)
		}

		msg.SetData("email", "changed@example.com")
		tr.Transform(ctx, msg, config)
		db.QueryRow("SELECT email FROM dim_users WHERE id = 100").Scan(&email)
		if email != "user100@example.com" {
			t.Errorf("Type 0 should not update, but got %s", email)
		}
	})

	// Test Type 3 (Previous Value)
	t.Run("Type 3", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetData("id", 300)
		msg.SetData("email", "initial@example.com")

		config := map[string]interface{}{
			"scdType":        3,
			"targetSourceId": "test",
			"targetTable":    "dim_users",
			"businessKeys":   []string{"id"},
			"columnMappings": map[string]interface{}{"email": "old_email"},
		}

		tr.Transform(ctx, msg, config)
		msg.SetData("email", "updated@example.com")
		tr.Transform(ctx, msg, config)

		var email, oldEmail string
		db.QueryRow("SELECT email, old_email FROM dim_users WHERE id = 300").Scan(&email, &oldEmail)
		if email != "updated@example.com" || oldEmail != "initial@example.com" {
			t.Errorf("Type 3 failed: email=%s, oldEmail=%s", email, oldEmail)
		}
	})

	// Test Type 4 (History Table)
	t.Run("Type 4", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetData("id", 400)
		msg.SetData("email", "v1@example.com")

		config := map[string]interface{}{
			"scdType":        4,
			"targetSourceId": "test",
			"targetTable":    "dim_users",
			"historyTable":   "dim_users_history",
			"businessKeys":   []string{"id"},
			"compareFields":  []string{"email"},
		}

		tr.Transform(ctx, msg, config)
		msg.SetData("email", "v2@example.com")
		tr.Transform(ctx, msg, config)

		var email string
		db.QueryRow("SELECT email FROM dim_users WHERE id = 400").Scan(&email)
		if email != "v2@example.com" {
			t.Errorf("Main table should have v2, got %s", email)
		}

		var hEmail string
		db.QueryRow("SELECT email FROM dim_users_history WHERE id = 400").Scan(&hEmail)
		if hEmail != "v1@example.com" {
			t.Errorf("History table should have v1, got %s", hEmail)
		}
	})

	// Test Type 6 (Hybrid)
	t.Run("Type 6", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetData("id", 600)
		msg.SetData("email", "user600@example.com")
		msg.SetData("department", "Sales")

		config := map[string]interface{}{
			"scdType":           6,
			"targetSourceId":    "test",
			"targetTable":       "dim_users",
			"businessKeys":      []string{"id"},
			"type1Columns":      []string{"email"},
			"type2Columns":      []string{"department"},
			"startDateColumn":   "start_date",
			"endDateColumn":     "end_date",
			"currentFlagColumn": "is_current",
		}

		// Initial insert
		tr.Transform(ctx, msg, config)

		// Type 2 change (department)
		msg.SetData("department", "Marketing")
		tr.Transform(ctx, msg, config)

		var count int
		db.QueryRow("SELECT COUNT(*) FROM dim_users WHERE id = 600").Scan(&count)
		if count != 2 {
			t.Errorf("Expected 2 rows after Type 2 change, got %d", count)
		}

		// Type 1 change (email) - should update ALL rows for this ID
		msg.SetData("email", "new_email@example.com")
		tr.Transform(ctx, msg, config)

		rows, _ := db.Query("SELECT email FROM dim_users WHERE id = 600")
		for rows.Next() {
			var email string
			rows.Scan(&email)
			if email != "new_email@example.com" {
				t.Errorf("Type 1 change should update all rows, but found %s", email)
			}
		}
		rows.Close()
	})
}
