// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
