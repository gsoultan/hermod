// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/rediskv"
)

func init() {
	transformer.Register("redis_set", newRedisSetTransformer())
}

// RedisSetTransformer writes a key in Redis and passes the record on
// unchanged, so the steps and sinks after it still run. It writes through
// pkg/infra/rediskv, as the Redis Set sink does.
type RedisSetTransformer struct {
	clients *rediskv.Pool
}

func newRedisSetTransformer() *RedisSetTransformer {
	return &RedisSetTransformer{clients: &rediskv.Pool{}}
}

// defaultRedisSetTimeout bounds one write, including the dial on first use.
const defaultRedisSetTimeout = 5 * time.Second

// redisConnKeys are the settings that choose where a write goes and with what
// credentials. They resolve secrets and env for the record's vhost and never
// row data, so a payload cannot choose the server or the password.
var redisConnKeys = map[string]bool{"addr": true, "username": true, "password": true}

// redisSetting reads one setting as text. The editor saves the database as a
// number and the switches as booleans, which GetConfigString reads as "".
func redisSetting(config map[string]any, msg hermod.Message, key string) string {
	var s string
	switch v := config[key].(type) {
	case string:
		s = v
	case bool:
		s = strconv.FormatBool(v)
	case float64:
		s = strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		s = strconv.Itoa(v)
	}
	if redisConnKeys[key] {
		return evaluator.ResolveTemplateScoped(s, msg)
	}
	return s
}

func (t *RedisSetTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	// A misconfiguration fails whatever onError says: passthrough is for a
	// Redis that could not be reached, not for a node that cannot work.
	settings, err := rediskv.ParseSettings(func(k string) string { return redisSetting(config, msg, k) })
	if err != nil {
		return msg, err
	}
	timeout := defaultRedisSetTimeout
	if s := strings.TrimSpace(redisSetting(config, msg, "timeout")); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return msg, fmt.Errorf("redis set: timeout %q is not a positive duration such as %q", s, "2s")
		}
		timeout = d
	}

	op, err := settings.Build(msg, func(m hermod.Message) ([]byte, error) { return json.Marshal(m.Data()) })
	if err != nil {
		return msg, err
	}

	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := rediskv.Apply(writeCtx, t.clients.Get(settings.Conn), op); err != nil {
		// A cancelled record is not a failed write.
		if ctx.Err() != nil {
			return msg, ctx.Err()
		}
		if strings.EqualFold(strings.TrimSpace(redisSetting(config, msg, "onError")), "passthrough") {
			return msg, nil
		}
		return msg, err
	}
	return msg, nil
}
