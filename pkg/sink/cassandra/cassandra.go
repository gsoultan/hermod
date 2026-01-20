package cassandra

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/user/hermod"
)

// CassandraSink implements the hermod.Sink interface for Cassandra.
type CassandraSink struct {
	hosts    []string
	keyspace string
	session  *gocql.Session
}

func NewCassandraSink(hosts []string, keyspace string) *CassandraSink {
	return &CassandraSink{
		hosts:    hosts,
		keyspace: keyspace,
	}
}

func (s *CassandraSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if s.session == nil {
		if err := s.init(); err != nil {
			return err
		}
	}

	query := fmt.Sprintf("INSERT INTO %s.%s (id, data) VALUES (?, ?)", s.keyspace, msg.Table())
	err := s.session.Query(query, msg.ID(), msg.Payload()).WithContext(ctx).Exec()
	if err != nil {
		return fmt.Errorf("failed to write to cassandra: %w", err)
	}

	return nil
}

func (s *CassandraSink) init() error {
	cluster := gocql.NewCluster(s.hosts...)
	cluster.Keyspace = s.keyspace
	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("failed to create cassandra session: %w", err)
	}
	s.session = session
	return nil
}

func (s *CassandraSink) Ping(ctx context.Context) error {
	if s.session == nil {
		if err := s.init(); err != nil {
			return err
		}
	}
	// Try a simple query to check if session is alive
	return s.session.Query("SELECT now() FROM system.local").WithContext(ctx).Exec()
}

func (s *CassandraSink) Close() error {
	if s.session != nil {
		s.session.Close()
	}
	return nil
}
