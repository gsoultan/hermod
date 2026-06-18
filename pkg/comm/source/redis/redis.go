package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// RedisSource implements the hermod.Source interface for Redis Streams.
type RedisSource struct {
	addr     string
	password string
	stream   string
	group    string
	consumer string
	client   *redis.Client
	mu       sync.Mutex
}

func NewRedisSource(addr string, password string, stream string, group string) *RedisSource {
	return &RedisSource{
		addr:     addr,
		password: password,
		stream:   stream,
		group:    group,
		consumer: "hermod-consumer",
	}
}

func (s *RedisSource) init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	client := redis.NewClient(&redis.Options{
		Addr:     s.addr,
		Password: s.password,
	})

	// Create consumer group if it doesn't exist
	err := client.XGroupCreateMkStream(ctx, s.stream, s.group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		client.Close()
		return fmt.Errorf("failed to create redis consumer group: %w", err)
	}

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return err
	}

	s.client = client
	return nil
}

func (s *RedisSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		s.mu.Lock()
		client = s.client
		s.mu.Unlock()
	}

	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    s.group,
		Consumer: s.consumer,
		Streams:  []string{s.stream, ">"},
		Count:    1,
		Block:    time.Second,
	}).Result()

	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil // No new data
		}
		return nil, fmt.Errorf("failed to read from redis stream: %w", err)
	}

	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return nil, nil
	}

	xmsg := streams[0].Messages[0]
	msg := message.AcquireMessage()
	msg.SetID(xmsg.ID)
	applyStreamValues(msg, xmsg.Values)

	msg.SetMetadata("redis_stream", s.stream)
	msg.SetMetadata("redis_group", s.group)

	return msg, nil
}

// applyStreamValues populates a message's fields from a Redis stream entry so
// that downstream transformation/sink nodes can see the data as available
// fields. The common convention stores the JSON-encoded payload under a single
// "data" field; when that field is absent we expose every field/value pair so
// previews still surface usable fields regardless of the producer's layout.
func applyStreamValues(msg *message.DefaultMessage, values map[string]any) {
	if raw, ok := streamDataBytes(values["data"]); ok {
		msg.SetPayload(raw)
		var jsonData map[string]any
		if err := json.Unmarshal(raw, &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter(raw) // Fallback for non-JSON
		}
		return
	}

	// Fallback: surface arbitrary field/value pairs as top-level fields.
	for k, v := range values {
		msg.SetData(k, v)
	}
}

// streamDataBytes normalizes the Redis "data" field, which the client may
// return as either a string or a byte slice, into raw bytes.
func streamDataBytes(v any) ([]byte, bool) {
	switch d := v.(type) {
	case string:
		return []byte(d), true
	case []byte:
		return d, true
	default:
		return nil, false
	}
}

func (s *RedisSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		return errors.New("redis client not initialized")
	}
	return client.XAck(ctx, s.stream, s.group, msg.ID()).Err()
}

func (s *RedisSource) IsReady(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("redis connection failed: %w", err)
	}

	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	var err error
	if client == nil {
		client = redis.NewClient(&redis.Options{
			Addr:     s.addr,
			Password: s.password,
		})
		defer client.Close()
	}

	// Check if stream exists
	exists, err := client.Exists(ctx, s.stream).Result()
	if err != nil {
		return fmt.Errorf("failed to check redis stream '%s': %w", s.stream, err)
	}
	if exists == 0 {
		return fmt.Errorf("redis stream '%s' does not exist", s.stream)
	}

	// Check if it's actually a stream
	typeInfo, err := client.Type(ctx, s.stream).Result()
	if err != nil {
		return fmt.Errorf("failed to check redis key type for '%s': %w", s.stream, err)
	}
	if typeInfo != "stream" {
		return fmt.Errorf("redis key '%s' exists but is not a stream (type is '%s')", s.stream, typeInfo)
	}

	return nil
}

func (s *RedisSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		client = redis.NewClient(&redis.Options{
			Addr:     s.addr,
			Password: s.password,
		})
		defer client.Close()
	}
	return client.Ping(ctx).Err()
}

func (s *RedisSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		s.mu.Lock()
		client = s.client
		s.mu.Unlock()
	}

	// Use XRevRange to get the latest message from the stream without affecting consumer groups
	msgs, err := client.XRevRangeN(ctx, s.stream, "+", "-", 1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to peek redis stream: %w", err)
	}

	if len(msgs) == 0 {
		return nil, errors.New("redis stream is empty")
	}

	xmsg := msgs[0]
	msg := message.AcquireMessage()
	msg.SetID(xmsg.ID)
	applyStreamValues(msg, xmsg.Values)

	msg.SetMetadata("redis_stream", s.stream)
	msg.SetMetadata("sample", "true")

	return msg, nil
}

func (s *RedisSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}
