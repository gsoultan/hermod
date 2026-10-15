// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package worker

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// ProbeResult is what Probe found at an address, in one word.
type ProbeResult string

const (
	// ProbeOK: a hermod-ml worker answered and took the token.
	ProbeOK ProbeResult = "ok"
	// ProbeBadAddress: the address is not an http or https URL Hermod can call.
	ProbeBadAddress ProbeResult = "bad_address"
	// ProbeUnreachable: nothing answered there (refused, no such host, timed
	// out).
	ProbeUnreachable ProbeResult = "unreachable"
	// ProbeNotAWorker: something answered, but not a hermod-ml worker.
	ProbeNotAWorker ProbeResult = "not_a_worker"
	// ProbeUnauthorized: the worker refused the token.
	ProbeUnauthorized ProbeResult = "unauthorized"
	// ProbeError: the worker answered with an error of its own.
	ProbeError ProbeResult = "error"
)

// ProbeReport is what Probe found, for a person: the result, a sentence, and
// what the worker can train.
type ProbeReport struct {
	Result  ProbeResult `json:"result"`
	Message string      `json:"message"`
	// DeepLearning says whether the worker can train a neural network, and
	// Frameworks which ones: the "-dl" image has both.
	DeepLearning bool     `json:"deep_learning"`
	Frameworks   []string `json:"frameworks,omitempty"`
	Algorithms   []string `json:"algorithms,omitempty"`
}

// deepFrameworks are the neural-network algorithms and the framework each
// trains with (ml/worker/hermod_ml/deep.py).
var deepFrameworks = []struct{ algorithm, framework string }{
	{"pytorch_mlp", "PyTorch"},
	{"keras_mlp", "TensorFlow"},
}

// ValidateURL checks a worker address and returns it without surrounding
// space or a trailing slash. It must be an absolute http or https URL with a
// host, and carry no credentials, query or fragment: the token goes in its own
// field, never in a URL that ends up in logs.
func ValidateURL(raw string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	if s == "" {
		return "", errors.New("the worker's address is empty")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "", errors.New("the worker's address must start with http:// or https://, like http://hermod-ml:8090")
	}
	if u.User != nil {
		return "", errors.New("the worker's address must not hold a user or password: put the token in its own field")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#") {
		return "", errors.New("the worker's address must not have a query (?) or a fragment (#)")
	}
	return s, nil
}

// Probe calls the worker at baseURL with token the way Hermod will: its
// readiness check, which needs no token, then its capabilities, which do. hc
// may be nil for Hermod's data client, which New uses too, so a test reaches
// exactly what training will. The report never repeats the token.
func Probe(ctx context.Context, baseURL, token string, hc *http.Client) ProbeReport {
	u, err := ValidateURL(baseURL)
	if err != nil {
		return ProbeReport{Result: ProbeBadAddress, Message: err.Error()}
	}
	c := New(u, token, hc)
	if err := c.Ready(ctx); err != nil {
		if isReply(err) {
			return ProbeReport{Result: ProbeNotAWorker,
				Message: "Something answered at this address, but it is not a hermod-ml worker: " + err.Error()}
		}
		return ProbeReport{Result: ProbeUnreachable,
			Message: "Nothing answered at this address: " + err.Error()}
	}
	caps, err := c.Capabilities(ctx)
	switch {
	case errors.Is(err, ErrUnauthorized):
		return ProbeReport{Result: ProbeUnauthorized,
			Message: "The worker refused the token: it must be the worker's HERMOD_ML_TOKEN."}
	case errors.Is(err, ErrNotFound), errors.Is(err, errNotJSON):
		return ProbeReport{Result: ProbeNotAWorker,
			Message: "Something answers health checks at this address, but it is not a hermod-ml worker " +
				"(or one too old to say what it can train)."}
	case err != nil && isReply(err):
		return ProbeReport{Result: ProbeError, Message: err.Error()}
	case err != nil:
		return ProbeReport{Result: ProbeUnreachable, Message: "The worker stopped answering: " + err.Error()}
	}
	r := ProbeReport{Result: ProbeOK, Algorithms: caps.Algorithms}
	for _, d := range deepFrameworks {
		if slices.Contains(caps.Algorithms, d.algorithm) {
			r.Frameworks = append(r.Frameworks, d.framework)
		}
	}
	r.DeepLearning = len(r.Frameworks) > 0
	if r.DeepLearning {
		r.Message = "Connected. The worker can train models, deep learning (" + strings.Join(r.Frameworks, ", ") + ") too."
	} else {
		r.Message = "Connected. The worker can train models; deep learning (PyTorch, TensorFlow) needs its -dl image."
	}
	return r
}

// isReply reports whether err is the worker's answer rather than a failure
// to reach it.
func isReply(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrBusy) || errors.Is(err, ErrUnauthorized) ||
		errors.As(err, new(*statusError))
}
