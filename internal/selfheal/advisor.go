// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package selfheal

import (
	"context"
	"errors"
	"strings"

	"github.com/gsoultan/hermod/internal/optimizer"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/llm"
)

// The environment that turns on AI mapping suggestions for the gate. Unset,
// no sample is ever sent to a model.
const (
	EnvProvider = "HERMOD_SELF_HEALING_AI_PROVIDER"
	EnvModel    = "HERMOD_SELF_HEALING_AI_MODEL"
	// EnvAPIKey must be a {{secret.NAME}} reference; it is resolved for
	// the vhost of the workflow being helped.
	EnvAPIKey  = "HERMOD_SELF_HEALING_AI_API_KEY" //nolint:gosec // G101: the name of an environment variable, not a credential.
	EnvBaseURL = "HERMOD_SELF_HEALING_AI_BASE_URL"
)

// WorkflowGetter finds a workflow's vhost.
type WorkflowGetter interface {
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
}

// AdvisorFromEnv returns the mapping advisor the environment configures, or
// nil when it configures none. A plaintext key is refused.
func AdvisorFromEnv(getenv func(string) string, workflows WorkflowGetter) (optimizer.MappingAdvisor, error) {
	provider := strings.TrimSpace(getenv(EnvProvider))
	if provider == "" {
		return nil, nil
	}
	key := strings.TrimSpace(getenv(EnvAPIKey))
	if key != "" && !strings.Contains(key, "{{") {
		return nil, errors.New(EnvAPIKey + ` must be a vhost secret reference such as {{secret.OPENAI_API_KEY}}; AI mapping suggestions are off`)
	}
	conn := map[string]any{
		"provider": provider, "model": getenv(EnvModel), "apiKey": key, "baseUrl": getenv(EnvBaseURL),
	}
	return optimizer.NewLLMMappingAdvisor(func(ctx context.Context, workflowID string) (llm.Provider, string, error) {
		wf, err := workflows.GetWorkflow(ctx, workflowID)
		if err != nil {
			return nil, "", err
		}
		scope := message.AcquireMessage()
		defer message.ReleaseMessage(scope)
		scope.SetVHost(wf.VHost)
		return genai.ProviderFor(conn, scope)
	}), nil
}
