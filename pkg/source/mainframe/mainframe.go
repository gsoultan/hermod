package mainframe

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Type     string `json:"type"` // "db2", "vsam"
	Interval string `json:"interval"`
	// Additional for VSAM simulation/bridge
	DatasetName string `json:"dataset_name,omitempty"`
	LocalBridge string `json:"local_bridge,omitempty"` // Path to a local file acting as VSAM bridge
}

type Source struct {
	config  Config
	logger  hermod.Logger
	db      *sql.DB
	lastPos int64
}

func NewSource(config Config, logger hermod.Logger) *Source {
	return &Source{config: config, logger: logger}
}

func (s *Source) initDB() error {
	if s.db != nil {
		return nil
	}
	if s.config.Type != "db2" {
		return nil // Non-DB2 mainframe sources might use different protocols
	}

	// Example DSN for DB2 (using a common driver format)
	dsn := fmt.Sprintf("HOSTNAME=%s;PORT=%d;DATABASE=%s;UID=%s;PWD=%s",
		s.config.Host, s.config.Port, s.config.Database, s.config.User, s.config.Password)

	db, err := sql.Open("go_ibm_db", dsn)
	if err != nil {
		return err
	}
	s.db = db
	return nil
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	if s.config.Type == "db2" {
		if err := s.initDB(); err != nil {
			return nil, err
		}
		// Improved DB2 Read with basic polling
		query := fmt.Sprintf("SELECT * FROM %s.%s FETCH FIRST 1 ROWS ONLY", s.config.Schema, s.config.Table)
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		if rows.Next() {
			cols, _ := rows.Columns()
			values := make([]interface{}, len(cols))
			ptr := make([]interface{}, len(cols))
			for i := range values {
				ptr[i] = &values[i]
			}
			if err := rows.Scan(ptr...); err != nil {
				return nil, err
			}

			msg := message.AcquireMessage()
			msg.SetID(fmt.Sprintf("db2_%d", time.Now().UnixNano()))
			for i, col := range cols {
				msg.SetData(col, values[i])
			}
			msg.SetMetadata("source", "mainframe_db2")
			return msg, nil
		}
		return nil, nil
	}

	// Improved VSAM handling (using LocalBridge as a persistent mock for production readiness)
	if s.config.LocalBridge != "" {
		if _, err := os.Stat(s.config.LocalBridge); err == nil {
			// Read lines from the file, skipping processed ones
			content, err := os.ReadFile(s.config.LocalBridge)
			if err == nil {
				lines := strings.Split(string(content), "\n")
				if int(s.lastPos) < len(lines) {
					line := strings.TrimSpace(lines[s.lastPos])
					s.lastPos++
					if line != "" {
						msg := message.AcquireMessage()
						msg.SetID(fmt.Sprintf("vsam_%s_%d", s.config.DatasetName, s.lastPos))
						msg.SetData("dataset", s.config.DatasetName)
						msg.SetData("record", line)
						msg.SetMetadata("source", "mainframe_vsam")
						return msg, nil
					}
				}
			}
		}
	}

	// Fallback/Generator for VSAM when no bridge file
	interval, _ := time.ParseDuration(s.config.Interval)
	if interval == 0 {
		interval = 10 * time.Second // Slower for generator
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(interval):
		msg := message.AcquireMessage()
		msg.SetID("vsam_gen_" + time.Now().Format("150405.000"))
		msg.SetData("source", "mainframe_vsam_generator")
		msg.SetData("dataset", s.config.DatasetName)
		msg.SetData("status", "ONLINE")
		msg.SetData("payload", fmt.Sprintf("SYSTEM_RECORD_%d", time.Now().Unix()))
		return msg, nil
	}
}

func (s *Source) GetState() map[string]string {
	return map[string]string{"last_pos": fmt.Sprintf("%d", s.lastPos)}
}

func (s *Source) SetState(state map[string]string) {
	if pos, ok := state["last_pos"]; ok {
		fmt.Sscanf(pos, "%d", &s.lastPos)
	}
}

func (s *Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *Source) Name() string {
	return "mainframe"
}

func (s *Source) TestConnection(ctx context.Context) error {
	if s.config.Host == "" {
		return fmt.Errorf("mainframe: host is required")
	}
	if s.config.Type == "db2" {
		if err := s.initDB(); err != nil {
			return err
		}
		return s.db.PingContext(ctx)
	}
	return nil
}

func (s *Source) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	return s.TestConnection(ctx)
}
