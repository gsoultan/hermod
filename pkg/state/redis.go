package state

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/hermod"
)

type RedisStateStore struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

func NewRedisStateStore(addr, password string, db int, prefix string, ttl time.Duration) hermod.StateStore {
	return &RedisStateStore{
		client: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
			DB:       db,
		}),
		prefix: prefix,
		ttl:    ttl,
	}
}

func (s *RedisStateStore) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := s.client.Get(ctx, s.prefix+key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return val, err
}

func (s *RedisStateStore) Set(ctx context.Context, key string, value []byte) error {
	return s.client.Set(ctx, s.prefix+key, value, s.ttl).Err()
}

func (s *RedisStateStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, s.prefix+key).Err()
}
