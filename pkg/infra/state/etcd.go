// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"time"

	"github.com/gsoultan/hermod"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func NewEtcdStateStore(endpoints []string, prefix string, timeout time.Duration) (hermod.StateStore, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &EtcdStateStore{
		client:  cli,
		prefix:  prefix,
		timeout: timeout,
	}, nil
}

type EtcdStateStore struct {
	client  *clientv3.Client
	prefix  string
	timeout time.Duration
}

func (s *EtcdStateStore) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	resp, err := s.client.Get(ctx, s.prefix+key)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, nil
	}
	return resp.Kvs[0].Value, nil
}

func (s *EtcdStateStore) Set(ctx context.Context, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.Put(ctx, s.prefix+key, string(value))
	return err
}

func (s *EtcdStateStore) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.Delete(ctx, s.prefix+key)
	return err
}

func (s *EtcdStateStore) Close() error {
	return s.client.Close()
}
