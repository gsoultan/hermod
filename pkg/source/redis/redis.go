package redis

import (
	"context"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// RedisSource implements the hermod.Source interface for Redis Streams.
type RedisSource struct {
	addr   string
	stream string
	group  string
}

func NewRedisSource(addr string, password string, stream string, group string) *RedisSource {
	return &RedisSource{
		addr:   addr,
		stream: stream,
		group:  group,
	}
}

func (s *RedisSource) Read(ctx context.Context) (hermod.Message, error) {
	// Simulated implementation for Redis Streams.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(100 * time.Millisecond):
		msg := message.AcquireMessage()
		msg.SetID("redis-stream-1")
		msg.SetMetadata("redis_stream", s.stream)
		return msg, nil
	}
}

func (s *RedisSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *RedisSource) Ping(ctx context.Context) error {
	return nil
}

func (s *RedisSource) Close() error {
	return nil
}
