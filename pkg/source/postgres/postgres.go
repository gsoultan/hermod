package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// PostgresSource implements the hermod.Source interface for PostgreSQL CDC.
type PostgresSource struct {
	connString      string
	slotName        string
	publicationName string
	tables          []string
	conn            *pgx.Conn
	replConn        *pgconn.PgConn
	typeMap         *pgtype.Map
	relations       map[uint32]*pglogrepl.RelationMessage
}

func NewPostgresSource(connString, slotName, publicationName string, tables []string) *PostgresSource {
	return &PostgresSource{
		connString:      connString,
		slotName:        slotName,
		publicationName: publicationName,
		tables:          tables,
		relations:       make(map[uint32]*pglogrepl.RelationMessage),
	}
}

func (p *PostgresSource) ensurePublication(ctx context.Context) error {
	var exists bool
	err := p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", p.publicationName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if publication exists: %w", err)
	}

	if !exists {
		tablesClause := "ALL TABLES"
		if len(p.tables) > 0 {
			tablesClause = "TABLE " + strings.Join(p.tables, ", ")
		}
		query := fmt.Sprintf("CREATE PUBLICATION %s FOR %s", p.publicationName, tablesClause)
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to create publication: %w", err)
		}
		fmt.Printf("Created publication %s\n", p.publicationName)
		return nil
	}

	// Publication exists, ensure it has the correct tables
	var pubAllTables bool
	err = p.conn.QueryRow(ctx, "SELECT puballtables FROM pg_publication WHERE pubname = $1", p.publicationName).Scan(&pubAllTables)
	if err != nil {
		return fmt.Errorf("failed to check publication details: %w", err)
	}

	if len(p.tables) == 0 {
		if !pubAllTables {
			_, err = p.conn.Exec(ctx, fmt.Sprintf("ALTER PUBLICATION %s SET FOR ALL TABLES", p.publicationName))
			if err != nil {
				return fmt.Errorf("failed to set publication to ALL TABLES: %w", err)
			}
			fmt.Printf("Updated publication %s to ALL TABLES\n", p.publicationName)
		}
		return nil
	}

	if pubAllTables {
		// Switch from ALL TABLES to specific tables
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", p.publicationName, strings.Join(p.tables, ", "))
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to update publication from ALL TABLES to specific tables: %w", err)
		}
		fmt.Printf("Updated publication %s from ALL TABLES to specific tables\n", p.publicationName)
		return nil
	}

	// Check if any tables are missing from the publication
	rows, err := p.conn.Query(ctx, "SELECT schemaname, tablename FROM pg_publication_tables WHERE pubname = $1", p.publicationName)
	if err != nil {
		return fmt.Errorf("failed to get publication tables: %w", err)
	}
	defer rows.Close()

	existingTables := make(map[string]bool)
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return fmt.Errorf("failed to scan publication table: %w", err)
		}
		existingTables[table] = true
		existingTables[schema+"."+table] = true
	}

	needsUpdate := false
	for _, t := range p.tables {
		if !existingTables[t] {
			needsUpdate = true
			break
		}
	}

	if needsUpdate {
		query := fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s", p.publicationName, strings.Join(p.tables, ", "))
		_, err = p.conn.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to update publication tables: %w", err)
		}
		fmt.Printf("Updated publication %s with tables: %s\n", p.publicationName, strings.Join(p.tables, ", "))
	}

	return nil
}

func (p *PostgresSource) ensureReplicationSlot(ctx context.Context) error {
	var exists bool
	err := p.conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", p.slotName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if replication slot exists: %w", err)
	}

	if !exists {
		_, err = p.conn.Exec(ctx, "SELECT pg_create_logical_replication_slot($1, 'pgoutput')", p.slotName)
		if err != nil {
			return fmt.Errorf("failed to create replication slot: %w", err)
		}
		fmt.Printf("Created replication slot %s\n", p.slotName)
	}
	return nil
}

func (p *PostgresSource) Read(ctx context.Context) (hermod.Message, error) {
	if p.replConn == nil {
		if err := p.init(ctx); err != nil {
			return nil, err
		}
	}

	for {
		msg, err := p.replConn.ReceiveMessage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to receive replication message: %w", err)
		}

		switch m := msg.(type) {
		case *pgproto3.CopyData:
			switch m.Data[0] {
			case 'k': // Primary Keepalive Message
				pka, err := pglogrepl.ParsePrimaryKeepaliveMessage(m.Data[1:])
				if err != nil {
					return nil, fmt.Errorf("failed to parse primary keepalive message: %w", err)
				}
				if pka.ReplyRequested {
					// Send standby status update
					err = pglogrepl.SendStandbyStatusUpdate(ctx, p.replConn, pglogrepl.StandbyStatusUpdate{
						WALWritePosition: pka.ServerWALEnd,
						WALFlushPosition: pka.ServerWALEnd,
						WALApplyPosition: pka.ServerWALEnd,
					})
					if err != nil {
						return nil, fmt.Errorf("failed to send standby status update: %w", err)
					}
				}
				continue
			case 'w': // XLogData
				xld, err := pglogrepl.ParseXLogData(m.Data[1:])
				if err != nil {
					return nil, fmt.Errorf("failed to parse xlog data: %w", err)
				}

				logicalMsg, err := pglogrepl.Parse(xld.WALData)
				if err != nil {
					return nil, fmt.Errorf("failed to parse logical replication message: %w", err)
				}

				switch lm := logicalMsg.(type) {
				case *pglogrepl.RelationMessage:
					fmt.Printf("Received RelationMessage: %s (%d)\n", lm.RelationName, lm.RelationID)
					p.relations[lm.RelationID] = lm
					continue
				case *pglogrepl.InsertMessage:
					fmt.Printf("Received InsertMessage for relation %d\n", lm.RelationID)
					res := message.AcquireMessage()
					res.SetID(xld.WALStart.String())
					res.SetOperation(hermod.OpCreate)
					res.SetMetadata("source", "postgres")
					res.SetMetadata("lsn", xld.WALStart.String())

					if rel, ok := p.relations[lm.RelationID]; ok {
						res.SetTable(rel.RelationName)
						res.SetSchema(rel.Namespace)

						data := make(map[string]interface{})
						for i, col := range lm.Tuple.Columns {
							if i < len(rel.Columns) {
								data[rel.Columns[i].Name] = string(col.Data)
							}
						}
						jsonBytes, _ := json.Marshal(data)
						res.SetAfter(jsonBytes)
					} else {
						fmt.Printf("Warning: No relation info for ID %d\n", lm.RelationID)
					}

					return res, nil
				case *pglogrepl.UpdateMessage:
					fmt.Printf("Received UpdateMessage for relation %d\n", lm.RelationID)
					res := message.AcquireMessage()
					res.SetID(xld.WALStart.String())
					res.SetOperation(hermod.OpUpdate)
					res.SetMetadata("source", "postgres")
					res.SetMetadata("lsn", xld.WALStart.String())

					if rel, ok := p.relations[lm.RelationID]; ok {
						res.SetTable(rel.RelationName)
						res.SetSchema(rel.Namespace)

						data := make(map[string]interface{})
						for i, col := range lm.NewTuple.Columns {
							if i < len(rel.Columns) {
								data[rel.Columns[i].Name] = string(col.Data)
							}
						}
						jsonBytes, _ := json.Marshal(data)
						res.SetAfter(jsonBytes)
					} else {
						fmt.Printf("Warning: No relation info for ID %d\n", lm.RelationID)
					}

					return res, nil
				case *pglogrepl.DeleteMessage:
					fmt.Printf("Received DeleteMessage for relation %d\n", lm.RelationID)
					res := message.AcquireMessage()
					res.SetID(xld.WALStart.String())
					res.SetOperation(hermod.OpDelete)
					res.SetMetadata("source", "postgres")
					res.SetMetadata("lsn", xld.WALStart.String())

					if rel, ok := p.relations[lm.RelationID]; ok {
						res.SetTable(rel.RelationName)
						res.SetSchema(rel.Namespace)
					} else {
						fmt.Printf("Warning: No relation info for ID %d\n", lm.RelationID)
					}

					return res, nil
				case *pglogrepl.BeginMessage:
					// fmt.Printf("Received BeginMessage\n")
					continue
				case *pglogrepl.CommitMessage:
					// fmt.Printf("Received CommitMessage\n")
					continue
				case *pglogrepl.OriginMessage, *pglogrepl.TypeMessage:
					continue
				default:
					fmt.Printf("Unknown logical replication message: %T\n", lm)
					continue
				}
			default:
				continue
			}
		default:
			continue
		}
	}
}

func (p *PostgresSource) init(ctx context.Context) error {
	fmt.Printf("Initializing PostgresSource for slot %s and publication %s\n", p.slotName, p.publicationName)
	if p.conn != nil {
		if err := p.conn.Ping(ctx); err == nil {
			if p.replConn != nil {
				return nil // Already initialized and healthy
			}
		} else {
			// Connection is bad, close it and re-init
			p.Close()
		}
	}

	var err error
	p.conn, err = pgx.Connect(ctx, p.connString)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres: %w", err)
	}
	fmt.Println("Connected to Postgres (normal)")

	if err := p.ensurePublication(ctx); err != nil {
		p.Close()
		return err
	}

	if err := p.ensureReplicationSlot(ctx); err != nil {
		p.Close()
		return err
	}

	// Establish replication connection
	connConfig, err := pgconn.ParseConfig(p.connString)
	if err != nil {
		p.Close()
		return fmt.Errorf("failed to parse connection string: %w", err)
	}
	if connConfig.RuntimeParams == nil {
		connConfig.RuntimeParams = make(map[string]string)
	}
	connConfig.RuntimeParams["replication"] = "database"

	p.replConn, err = pgconn.ConnectConfig(ctx, connConfig)
	if err != nil {
		p.Close()
		return fmt.Errorf("failed to connect to postgres (replication): %w", err)
	}
	fmt.Println("Connected to Postgres (replication)")

	p.typeMap = pgtype.NewMap()

	// Start replication
	fmt.Printf("Starting replication for slot %s...\n", p.slotName)
	err = pglogrepl.StartReplication(ctx, p.replConn, p.slotName, 0, pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			"publication_names '" + p.publicationName + "'",
		},
	})
	if err != nil {
		p.Close()
		return fmt.Errorf("failed to start replication: %w", err)
	}
	fmt.Println("Replication started successfully")

	return nil
}

func (p *PostgresSource) Ack(ctx context.Context, msg hermod.Message) error {
	if p.replConn == nil {
		return nil
	}

	lsnStr := msg.Metadata()["lsn"]
	if lsnStr == "" {
		return nil
	}

	lsn, err := pglogrepl.ParseLSN(lsnStr)
	if err != nil {
		return fmt.Errorf("failed to parse LSN: %w", err)
	}

	err = pglogrepl.SendStandbyStatusUpdate(ctx, p.replConn, pglogrepl.StandbyStatusUpdate{
		WALWritePosition: lsn,
		WALFlushPosition: lsn,
		WALApplyPosition: lsn,
	})
	if err != nil {
		return fmt.Errorf("failed to send standby status update: %w", err)
	}

	return nil
}

func (p *PostgresSource) Ping(ctx context.Context) error {
	if p.conn == nil || p.replConn == nil {
		if err := p.init(ctx); err != nil {
			return err
		}
	}
	err := p.conn.Ping(ctx)
	if err != nil {
		// If ping fails, try to re-init once
		p.Close()
		if err := p.init(ctx); err != nil {
			return err
		}
		return p.conn.Ping(ctx)
	}
	return nil
}

func (p *PostgresSource) Close() error {
	var errs []string
	if p.replConn != nil {
		if err := p.replConn.Close(context.Background()); err != nil {
			errs = append(errs, fmt.Sprintf("failed to close replication connection: %v", err))
		}
		p.replConn = nil
	}
	if p.conn != nil {
		if err := p.conn.Close(context.Background()); err != nil {
			errs = append(errs, fmt.Sprintf("failed to close connection: %v", err))
		}
		p.conn = nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing postgres source: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (p *PostgresSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if p.conn == nil {
		var err error
		p.conn, err = pgx.Connect(ctx, p.connString)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to postgres: %w", err)
		}
	}

	rows, err := p.conn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false")
	if err != nil {
		return nil, fmt.Errorf("failed to query databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, nil
}

func (p *PostgresSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if p.conn == nil {
		var err error
		p.conn, err = pgx.Connect(ctx, p.connString)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to postgres: %w", err)
		}
	}

	rows, err := p.conn.Query(ctx, "SELECT schemaname || '.' || tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, nil
}
