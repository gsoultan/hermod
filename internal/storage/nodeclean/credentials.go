// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package nodeclean

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// Credential is a password, token or key typed into a step in plain text.
type Credential struct {
	Node   int    // index into the nodes it was found in
	NodeID string // the node's id
	Label  string // the node's label, for a readable secret name
	Field  string // the config key holding it
	Header string // for Field "headers", the header that holds it
	Value  string
}

// credentialFields lists, per step kind, the settings that hold a credential
// AND that the step resolves as a template, so {{secret.NAME}} in them reads
// the vhost's secret. Only those may be moved into a secret: a credential the
// step reads literally (encrypt's inline "key", for one) would stop working.
func credentialFields(kind string) []string {
	switch {
	case kind == "api_lookup":
		return []string{"username", "password", "token", "headers"}
	case kind == "redis_lookup":
		return []string{"password"}
	case strings.HasPrefix(kind, "ai_"), kind == "mimir_ask", kind == "panmail_providers":
		return []string{"apiKey", "fallbackApiKey", "storeApiKey"}
	}
	return nil
}

// nodeKind is the step a node runs: the editor stores a transformation's
// real type in transType, other executors use the node type itself.
func nodeKind(n storage.WorkflowNode) string {
	if t, _ := n.Config["transType"].(string); t != "" {
		return t
	}
	return n.Type
}

// typed reports a value someone typed, as opposed to a reference to a secret
// or a value a redaction already replaced.
func typed(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && !strings.Contains(v, "{{") && v != "[REDACTED]"
}

// sensitiveHeader reports a request header that carries a credential.
func sensitiveHeader(name string) bool {
	k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
	switch k {
	case "authorization", "proxy_authorization", "cookie", "apikey", "x_auth_token":
		return true
	}
	return configsecrets.IsSensitive(k)
}

// TypedCredentials lists every credential typed into a step of nodes.
func TypedCredentials(nodes []storage.WorkflowNode) []Credential {
	var out []Credential
	Rewrite(nodes, func(c Credential) string {
		out = append(out, c)
		return c.Value
	})
	return out
}

// Rewrite returns nodes with each typed credential replaced by what replace
// returns for it. The nodes passed in are not modified.
func Rewrite(nodes []storage.WorkflowNode, replace func(Credential) string) []storage.WorkflowNode {
	out, cloned := nodes, false
	for i, n := range nodes {
		cfg := rewriteNode(i, n, replace)
		if cfg == nil {
			continue
		}
		if !cloned {
			out, cloned = slices.Clone(nodes), true
		}
		out[i].Config = cfg
	}
	return out
}

// rewriteNode returns a copy of n's config with its credentials replaced,
// or nil when nothing changed.
func rewriteNode(i int, n storage.WorkflowNode, replace func(Credential) string) map[string]any {
	if IsConnection(n) {
		return nil
	}
	label, _ := n.Config["label"].(string)
	var cfg map[string]any
	set := func(k string, v any) {
		if cfg == nil {
			cfg = maps.Clone(n.Config)
		}
		cfg[k] = v
	}
	for _, field := range credentialFields(nodeKind(n)) {
		base := Credential{Node: i, NodeID: n.ID, Label: label, Field: field}
		if field == "headers" {
			if h, ok := rewriteHeaders(n.Config[field], base, replace); ok {
				set(field, h)
			}
			continue
		}
		v, _ := n.Config[field].(string)
		if !typed(v) {
			continue
		}
		base.Value = v
		if r := replace(base); r != v {
			set(field, r)
		}
	}
	return cfg
}

// rewriteHeaders handles headers kept as JSON text (what the editor writes)
// or as an object. It returns the new value in the same shape, and whether
// anything changed.
func rewriteHeaders(raw any, base Credential, replace func(Credential) string) (any, bool) {
	var headers map[string]any
	asText := false
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" || json.Unmarshal([]byte(v), &headers) != nil {
			return nil, false
		}
		asText = true
	case map[string]any:
		headers = v
	default:
		return nil, false
	}
	var next map[string]any
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		v, _ := headers[name].(string)
		if !sensitiveHeader(name) || !typed(v) {
			continue
		}
		c := base
		c.Header, c.Value = name, v
		if r := replace(c); r != v {
			if next == nil {
				next = maps.Clone(headers)
			}
			next[name] = r
		}
	}
	if next == nil {
		return nil, false
	}
	if !asText {
		return next, true
	}
	b, err := json.Marshal(next)
	if err != nil {
		return nil, false
	}
	return string(b), true
}

// SecretName is the vhost secret a credential moves into: the workflow, the
// step and the field, so each place has its own and saving the same value
// again lands on the same name.
func SecretName(workflowID string, c Credential) string {
	wf := strings.ReplaceAll(workflowID, "-", "")
	if len(wf) > 8 {
		wf = wf[:8]
	}
	step := c.Label
	if strings.TrimSpace(step) == "" {
		step = c.NodeID
	}
	parts := []string{"WF", wf, step, c.Field}
	if c.Header != "" {
		parts[3] = c.Header
	}
	name := secretIdent(strings.Join(parts, "_"))
	if len(name) > storage.MaxVHostSecretNameLen {
		name = name[:storage.MaxVHostSecretNameLen]
	}
	return name
}

// secretIdent upper-cases s and turns every run of other characters into one
// underscore, so it is a valid secret name.
func secretIdent(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			under = false
			continue
		}
		if !under {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// SecretStore is the part of a vhost secret store MoveCredentials needs.
type SecretStore interface {
	GetVHostSecret(ctx context.Context, vhost, name string) (storage.VHostSecret, error)
	PutVHostSecret(ctx context.Context, secret storage.VHostSecret) error
}

// MoveCredentials stores every credential typed into a step of nodes as a
// secret of vhost and returns the nodes with {{secret.NAME}} in its place,
// plus the names it wrote. The engine resolves the reference for the
// workflow's vhost, a promotion asks production for the value by that name,
// and the workflow JSON holds no credential.
//
// A name already holding the same value is not written again, so a save
// that changes nothing writes nothing. On any error no node is changed.
func MoveCredentials(ctx context.Context, store SecretStore, vhost, workflowID string, nodes []storage.WorkflowNode) ([]storage.WorkflowNode, []string, error) {
	if vhost == "" || vhost == "all" {
		vhost = "default"
	}
	creds := TypedCredentials(nodes)
	if len(creds) == 0 {
		return nodes, nil, nil
	}
	// Two steps may share a label; a step whose label another step also has
	// is named by its id instead, so each place gets its own secret.
	labelled := map[string]map[int]bool{}
	for _, c := range creds {
		key := secretIdent(c.Label)
		if labelled[key] == nil {
			labelled[key] = map[int]bool{}
		}
		labelled[key][c.Node] = true
	}
	names := make([]string, len(creds))
	for i, c := range creds {
		if len(labelled[secretIdent(c.Label)]) > 1 {
			c.Label = c.NodeID
		}
		names[i] = SecretName(workflowID, c)
	}
	var written []string
	for i, c := range creds {
		secret := storage.VHostSecret{VHost: vhost, Name: names[i], Value: c.Value}
		if err := storage.ValidateVHostSecret(secret); err != nil {
			return nodes, nil, fmt.Errorf("step %q setting %s cannot be kept as a secret: %w", c.Label, c.Field, err)
		}
		if have, err := store.GetVHostSecret(ctx, vhost, names[i]); err == nil && have.Value == c.Value {
			continue
		}
		if err := store.PutVHostSecret(ctx, secret); err != nil {
			return nodes, nil, fmt.Errorf("storing secret %s: %w", names[i], err)
		}
		written = append(written, names[i])
	}
	i := 0
	out := Rewrite(nodes, func(Credential) string {
		ref := "{{secret." + names[i] + "}}"
		i++
		return ref
	})
	return out, written, nil
}

// ForWrite is what storage does to a workflow's nodes before writing them:
// it removes what a saved node may not hold and, when store is not nil,
// moves typed step credentials into the vhost's secrets.
func ForWrite(ctx context.Context, store SecretStore, vhost, workflowID string, nodes []storage.WorkflowNode) ([]storage.WorkflowNode, error) {
	nodes, _ = Nodes(nodes)
	if store == nil {
		return nodes, nil
	}
	nodes, _, err := MoveCredentials(ctx, store, vhost, workflowID, nodes)
	return nodes, err
}

// MovedSecrets names the secrets that hold the credentials typed into sent,
// read from saved: what storage kept in their place. A caller that caches
// secret values forgets these after a save.
func MovedSecrets(sent, saved []storage.WorkflowNode) []string {
	byID := make(map[string]storage.WorkflowNode, len(saved))
	for _, n := range saved {
		byID[n.ID] = n
	}
	var names []string
	for _, c := range TypedCredentials(sent) {
		n, ok := byID[c.NodeID]
		if !ok {
			continue
		}
		v, _ := n.Config[c.Field].(string)
		if c.Header != "" {
			var h map[string]any
			_ = json.Unmarshal([]byte(v), &h)
			if obj, ok := n.Config[c.Field].(map[string]any); ok {
				h = obj
			}
			v, _ = h[c.Header].(string)
		}
		if name, ok := strings.CutPrefix(strings.TrimSpace(v), "{{secret."); ok {
			if name, ok = strings.CutSuffix(name, "}}"); ok && storage.ValidVHostSecretName(name) {
				names = append(names, name)
			}
		}
	}
	return names
}
