package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/hermod"
)

// RedisSink implements the hermod.Sink interface for Redis (using Streams).
// This is a placeholder that simulates Redis Stream publishing.
type RedisSink struct {
	addr      string
	stream    string
	formatter hermod.Formatter
}

func NewRedisSink(addr string, password string, stream string, formatter hermod.Formatter) (*RedisSink, error) {
	// In a real implementation, we would connect to Redis here.
	return &RedisSink{
		addr:      addr,
		stream:    stream,
		formatter: formatter,
	}, nil
}

func (s *RedisSink) Write(ctx context.Context, msg hermod.Message) error {
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data, err = json.Marshal(map[string]interface{}{
			"id":        msg.ID(),
			"operation": msg.Operation(),
			"table":     msg.Table(),
			"schema":    msg.Schema(),
			"before":    json.RawMessage(msg.Before()),
			"after":     json.RawMessage(msg.After()),
			"metadata":  msg.Metadata(),
		})
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	// In a real implementation:
	// s.client.XAdd(ctx, &redis.XAddArgs{
	//     Stream: s.stream,
	//     Values: map[string]interface{}{"data": data},
	// })

	fmt.Printf("RedisSink [%s]: Published message to stream %s: %s\n", s.addr, s.stream, string(data))
	return nil
}

func (s *RedisSink) Ping(ctx context.Context) error {
	// In a real implementation, we would ping Redis.
	return nil
}

func (s *RedisSink) Close() error {
	fmt.Printf("Closing RedisSink [%s]\n", s.addr)
	return nil
}
