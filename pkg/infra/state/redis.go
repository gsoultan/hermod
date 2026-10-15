// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package state

import (
	"context"
	"errors"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/redis/go-redis/v9"
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
	if errors.Is(err, redis.Nil) {
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
