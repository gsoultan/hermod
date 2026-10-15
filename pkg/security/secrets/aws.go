// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package secrets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// AWSSecretsManager implements secrets.Manager for AWS Secrets Manager.
type AWSSecretsManager struct {
	client *secretsmanager.Client
}

// NewAWSSecretsManager creates a new AWSSecretsManager.
func NewAWSSecretsManager(ctx context.Context, region string) (*AWSSecretsManager, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := secretsmanager.NewFromConfig(cfg)
	return &AWSSecretsManager{
		client: client,
	}, nil
}

// Get retrieves a secret from AWS Secrets Manager.
func (m *AWSSecretsManager) Get(ctx context.Context, key string) (string, error) {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(key),
	}

	result, err := m.client.GetSecretValue(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to get secret from aws: %w", err)
	}

	if result.SecretString != nil {
		return *result.SecretString, nil
	}

	return "", fmt.Errorf("secret %s has no string value", key)
}
