package redis

import (
	"context"
	"fmt"
	"os"
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
	s.client = redis.NewClient(&redis.Options{
		Addr:     s.addr,
		Password: s.password,
	})
	return s.client.Ping(ctx).Err()
}

func (s *RedisSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	// reset last outcome
	s.lastDedup = false
	s.lastConflict = false

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
		ok, err := s.client.SetNX(ctx, key, "1", ttl).Result()
		if err != nil {
			return fmt.Errorf("redis idempotency setnx error: %w", err)
		}
		if !ok {
			// Duplicate message; treat as handled without re-publishing
			s.lastDedup = true
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

	err = s.client.XAdd(ctx, &redis.XAddArgs{
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
	return s.lastDedup, s.lastConflict
}

func (s *RedisSink) Ping(ctx context.Context) error {
	if s.client == nil {
		return s.init(ctx)
	}
	return s.client.Ping(ctx).Err()
}

func (s *RedisSink) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
