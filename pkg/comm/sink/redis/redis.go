package redis

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/hermod"
)

// RedisSink implements the hermod.Sink interface for Redis (using Streams).
type RedisSink struct {
	addr      string
	password  string
	stream    string
	formatter hermod.Formatter
	client    *redis.Client
	mu        sync.Mutex
	// idempotency reporting (last write outcome)
	lastDedup    bool
	lastConflict bool
}

func NewRedisSink(addr string, password string, stream string, formatter hermod.Formatter) (*RedisSink, error) {
	return &RedisSink{
		addr:      addr,
		password:  password,
		stream:    stream,
		formatter: formatter,
	}, nil
}

func (s *RedisSink) init(ctx context.Context) error {
	s.mu.Lock()
	if s.client != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	cl := redis.NewClient(&redis.Options{
		Addr:     s.addr,
		Password: s.password,
	})

	if err := cl.Ping(ctx).Err(); err != nil {
		cl.Close()
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		cl.Close()
		return nil
	}
	s.client = cl
	return nil
}

func (s *RedisSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	s.mu.Lock()
	cl := s.client
	s.mu.Unlock()
	if cl == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		cl = s.client
		s.mu.Unlock()
	}

	// reset last outcome
	s.mu.Lock()
	s.lastDedup = false
	s.lastConflict = false
	s.mu.Unlock()

	// Idempotency via SETNX with TTL: if message has an ID, use it to dedupe
	if id := msg.ID(); id != "" {
		ttl := 24 * time.Hour
		if v := os.Getenv("HERMOD_IDEMPOTENCY_TTL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				ttl = d
			}
		}
		ns := os.Getenv("HERMOD_IDEMPOTENCY_NAMESPACE")
		if ns == "" {
			ns = "hermod:idemp"
		}
		key := fmt.Sprintf("%s:%s:%s", ns, s.stream, id)
		ok, err := cl.SetNX(ctx, key, "1", ttl).Result()
		if err != nil {
			return fmt.Errorf("redis idempotency setnx error: %w", err)
		}
		if !ok {
			// Duplicate message; treat as handled without re-publishing
			s.mu.Lock()
			s.lastDedup = true
			s.mu.Unlock()
			return nil
		}
	}

	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	err = cl.XAdd(ctx, &redis.XAddArgs{
		Stream: s.stream,
		Values: map[string]any{"data": data},
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to publish to redis stream: %w", err)
	}

	return nil
}

// LastWriteIdempotent reports whether the last Write call resulted in a dedup skip
// or a conflict. Redis sink only reports dedup (conflicts are not applicable).
func (s *RedisSink) LastWriteIdempotent() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastDedup, s.lastConflict
}

func (s *RedisSink) Ping(ctx context.Context) error {
	s.mu.Lock()
	cl := s.client
	s.mu.Unlock()
	if cl == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
		s.mu.Lock()
		cl = s.client
		s.mu.Unlock()
	}
	return cl.Ping(ctx).Err()
}

func (s *RedisSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}
