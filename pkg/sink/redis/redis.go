package redis

import (
	"context"
	"fmt"

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
		Values: map[string]interface{}{"data": data},
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to publish to redis stream: %w", err)
	}

	return nil
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
