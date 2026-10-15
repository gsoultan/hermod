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
