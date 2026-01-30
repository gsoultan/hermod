package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
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
		if err == redis.Nil {
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

	if data, ok := xmsg.Values["data"].(string); ok {
		msg.SetPayload([]byte(data))
		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(data), &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter([]byte(data)) // Fallback for non-JSON
		}
	} else if dataBytes, ok := xmsg.Values["data"].([]byte); ok {
		msg.SetPayload(dataBytes)
		// Try to unmarshal JSON into Data() for dynamic structure
		var jsonData map[string]interface{}
		if err := json.Unmarshal(dataBytes, &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter(dataBytes) // Fallback for non-JSON
		}
	}

	msg.SetMetadata("redis_stream", s.stream)
	msg.SetMetadata("redis_group", s.group)

	return msg, nil
}

func (s *RedisSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client == nil {
		return fmt.Errorf("redis client not initialized")
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
		return nil, fmt.Errorf("redis stream is empty")
	}

	xmsg := msgs[0]
	msg := message.AcquireMessage()
	msg.SetID(xmsg.ID)

	if data, ok := xmsg.Values["data"].(string); ok {
		msg.SetPayload([]byte(data))
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(data), &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter([]byte(data))
		}
	} else if dataBytes, ok := xmsg.Values["data"].([]byte); ok {
		msg.SetPayload(dataBytes)
		var jsonData map[string]interface{}
		if err := json.Unmarshal(dataBytes, &jsonData); err == nil {
			for k, v := range jsonData {
				msg.SetData(k, v)
			}
		} else {
			msg.SetAfter(dataBytes)
		}
	}

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
