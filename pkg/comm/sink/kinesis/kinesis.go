// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package kinesis

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/gsoultan/hermod"
)

type KinesisSink struct {
	client     *kinesis.Client
	streamName string
	formatter  hermod.Formatter
	region     string
	accessKey  string
	secretKey  string
	mu         sync.Mutex
}

func NewKinesisSink(region string, streamName string, accessKey, secretKey string, formatter hermod.Formatter) (*KinesisSink, error) {
	return &KinesisSink{
		region:     region,
		streamName: streamName,
		accessKey:  accessKey,
		secretKey:  secretKey,
		formatter:  formatter,
	}, nil
}

func (s *KinesisSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	if s.client != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(s.region),
	}

	if s.accessKey != "" && s.secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(s.accessKey, s.secretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := kinesis.NewFromConfig(cfg)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return nil
	}

	s.client = client
	return nil
}

func (s *KinesisSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
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

	partitionKey := msg.ID()
	if partitionKey == "" {
		partitionKey = "default"
	}

	_, err = s.client.PutRecord(ctx, &kinesis.PutRecordInput{
		Data:         data,
		PartitionKey: aws.String(partitionKey),
		StreamName:   aws.String(s.streamName),
	})
	if err != nil {
		return fmt.Errorf("failed to put record to kinesis: %w", err)
	}

	return nil
}

func (s *KinesisSink) Ping(ctx context.Context) error {
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	_, err := s.client.DescribeStream(ctx, &kinesis.DescribeStreamInput{
		StreamName: aws.String(s.streamName),
	})
	if err != nil {
		return fmt.Errorf("failed to describe kinesis stream: %w", err)
	}
	return nil
}

func (s *KinesisSink) Close() error {
	return nil
}
