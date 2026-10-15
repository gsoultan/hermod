// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
