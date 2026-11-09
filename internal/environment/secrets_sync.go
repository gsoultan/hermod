// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
)

// Secrets a promotion needs.
//
// A workflow that names {{secret.NAME}} reads NAME from its vhost's secrets.
// Only the name travels between instances, never a value: when a promotion
// arrives, the receiving instance notes which of the names the workflow uses
// its vhost lacks (Promotion.Secrets), and the admin who approves it types
// their values there. A value is created only where the vhost still lacks
// the name -- one set on the Secrets page in the meantime is kept -- and a
// name left blank stays missing, for one served by the environment or a
// secret manager.

// SecretWriter creates a vhost's own secrets. The SecretChecker an instance
// is built with may implement it; one that does not takes no values on
// approval.
type SecretWriter interface {
	// CreateSecret stores the secret unless the vhost already holds the
	// name; created says whether it did. by is who it is recorded as.
	CreateSecret(ctx context.Context, vhost, name, value, by string) (created bool, err error)
}

// connectionSecrets maps each secret the sources and sinks here read to the
// connections that read it ("source orders-db"), and says in which setting.
// One that is not found, or that may says is out of reach, is skipped.
func (s *Service) connectionSecrets(ctx context.Context, sources, sinks []string, may func(vhost string) bool) (map[string][]string, places) {
	uses := map[string][]string{}
	where := places{}
	add := func(kind, name string, cfg map[string]string) {
		for _, secret := range configSecrets(cfg) {
			uses[secret] = append(uses[secret], kind+" "+name)
		}
		where.merge(connectionPlaces(kind, name, cfg))
	}
	for _, id := range sources {
		if src, err := s.store.GetSource(ctx, id); err == nil && may(src.VHost) {
			add("source", src.Name, src.Config)
		}
	}
	for _, id := range sinks {
		if snk, err := s.store.GetSink(ctx, id); err == nil && may(snk.VHost) {
			add("sink", snk.Name, snk.Config)
		}
	}
	return uses, where
}

// secretPlaces says where wf, the sources and sinks it uses here, and the
// ones it brings use each secret.
func (s *Service) secretPlaces(ctx context.Context, wf storage.Workflow, carried []CarriedConnection) places {
	where := stepPlaces(wf)
	sources, sinks := workflowRefs(wf)
	_, conns := s.connectionSecrets(ctx, sources, sinks, func(string) bool { return true })
	where.merge(conns)
	where.merge(carriedPlaces(carried))
	return where
}

// secretsUsed lists every secret wf needs here: the ones its nodes name and
// the ones the sources and sinks it uses read.
func (s *Service) secretsUsed(ctx context.Context, wf storage.Workflow, carried []CarriedConnection) []string {
	names, _ := references(wf)
	sources, sinks := workflowRefs(wf)
	uses, _ := s.connectionSecrets(ctx, sources, sinks, func(string) bool { return true })
	maps.Copy(uses, carriedSecrets(carried))
	for name := range uses {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// missingSecrets lists the secrets wf names that vhost here lacks.
func (s *Service) missingSecrets(ctx context.Context, wf storage.Workflow, carried []CarriedConnection, vhost string) []string {
	if s.secrets == nil {
		return nil
	}
	var out []string
	for _, name := range s.secretsUsed(ctx, wf, carried) {
		if !s.secrets.HasSecret(ctx, vhost, name) {
			out = append(out, name)
		}
	}
	return out
}

// filledSecrets checks the values an approver typed for the promotions
// members of ps and returns them without the blank ones. Every name must be
// one of theirs, and every value one the Secrets page would take.
func (s *Service) filledSecrets(ps []Promotion, members []int, values map[string]string) (map[string]string, error) {
	filled := map[string]string{}
	for name, v := range values {
		if v != "" {
			filled[name] = v
		}
	}
	if len(filled) == 0 {
		return nil, nil
	}
	if _, ok := s.secrets.(SecretWriter); !ok {
		return nil, fmt.Errorf("%w: this instance cannot store secrets; set them where its secret manager keeps them", ErrInvalid)
	}
	needed := map[string]string{} // name -> a vhost that needs it
	for _, j := range members {
		for _, name := range ps[j].Secrets {
			needed[name] = vhostOf(ps[j].VHost)
		}
	}
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(filled)) {
		vhost, ok := needed[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if err := storage.ValidateVHostSecret(storage.VHostSecret{VHost: vhost, Name: name, Value: filled[name]}); err != nil {
			return nil, fmt.Errorf("%w: secret %s: %w", ErrInvalid, name, err)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%w: the promotion does not need these secrets: %s", ErrInvalid, strings.Join(unknown, ", "))
	}
	return filled, nil
}

// fillSecrets creates, in p's vhost, the secrets p needs that were given a
// value and that the vhost still lacks. It reports the ones it created, the
// ones the vhost has by now, and the ones still missing.
func (s *Service) fillSecrets(ctx context.Context, p Promotion, values map[string]string, by string) (created, kept, missing []string, err error) {
	if len(p.Secrets) == 0 || s.secrets == nil {
		return nil, nil, nil, nil
	}
	w, canWrite := s.secrets.(SecretWriter)
	vhost := vhostOf(p.VHost)
	for _, name := range p.Secrets {
		if s.secrets.HasSecret(ctx, vhost, name) {
			kept = append(kept, name)
			continue
		}
		v, given := values[name]
		if !given || !canWrite {
			missing = append(missing, name)
			continue
		}
		made, err := w.CreateSecret(ctx, vhost, name, v, by)
		if err != nil {
			return created, kept, missing, fmt.Errorf("creating secret %s: %w", name, err)
		}
		if made {
			created = append(created, name)
		} else {
			kept = append(kept, name)
		}
	}
	return created, kept, missing, nil
}

// Values the sender types.
//
// The sender may type production's values in the promote dialog instead of
// leaving them to the approver. They go to the receiving instance with the
// submission and are never kept on the sending side. There they wait,
// encrypted, beside the pending promotion, and are created on approval like
// the approver's own -- which win when both are given. A rejected or failed
// promotion drops them.

// maxSentSecrets bounds the values one promotion carries.
const maxSentSecrets = 100

// checkSecretNames refuses a value for a secret none of previews lists as
// missing: it would land nowhere.
func checkSecretNames(previews []Preview, values map[string]string) error {
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if values[name] == "" {
			continue
		}
		if !slices.ContainsFunc(previews, func(p Preview) bool { return slices.Contains(p.MissingSecrets, name) }) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%w: these secrets are not missing where the workflows go, so no value is needed: %s", ErrInvalid, strings.Join(unknown, ", "))
	}
	return nil
}

// valuesFor splits the typed values by preview: each gets the non-blank ones
// for the secrets it lists as missing. A preview whose instance cannot take
// values is refused rather than sent without them.
func valuesFor(previews []Preview, values map[string]string) ([]map[string]string, error) {
	out := make([]map[string]string, len(previews))
	for i, p := range previews {
		for _, name := range p.MissingSecrets {
			v := values[name]
			if v == "" {
				continue
			}
			if !p.takesSecrets {
				return nil, fmt.Errorf("%w: %s cannot take secret values; update it, or set %s there", ErrInvalid, p.LinkName, name)
			}
			if out[i] == nil {
				out[i] = map[string]string{}
			}
			out[i][name] = v
		}
	}
	return out, nil
}

// admitSentSecrets checks the values a submission carries for a workflow
// that lands in vhost here, where it lacks needs. It returns the ones to keep:
// a value for a secret the vhost has is dropped, since it is never replaced.
func (s *Service) admitSentSecrets(ctx context.Context, wf storage.Workflow, carried []CarriedConnection, vhost string, needs []string, sent map[string]string) (map[string]string, error) {
	if len(sent) == 0 {
		return nil, nil
	}
	if _, ok := s.secrets.(SecretWriter); !ok {
		return nil, fmt.Errorf("%w: this instance cannot store secret values", ErrInvalid)
	}
	if len(sent) > maxSentSecrets {
		return nil, fmt.Errorf("%w: a promotion carries at most %d secret values", ErrInvalid, maxSentSecrets)
	}
	named := s.secretsUsed(ctx, wf, carried)
	var unused []string
	keep := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(sent)) {
		if !slices.Contains(named, name) {
			unused = append(unused, name)
			continue
		}
		if err := storage.ValidateVHostSecret(storage.VHostSecret{VHost: vhost, Name: name, Value: sent[name]}); err != nil {
			return nil, fmt.Errorf("%w: secret %s: %w", ErrInvalid, name, err)
		}
		if slices.Contains(needs, name) {
			keep[name] = sent[name]
		}
	}
	if len(unused) > 0 {
		return nil, fmt.Errorf("%w: the workflow does not use these secrets: %s", ErrInvalid, strings.Join(unused, ", "))
	}
	return keep, nil
}

// secretField is a value's key in the stored map. The _secret suffix is what
// makes configsecrets encrypt it.
func secretField(name string) string { return name + "_secret" }

// saveSentSecrets keeps a promotion's values, encrypted, until it is decided.
// One that cannot be encrypted fails the save rather than being stored in the
// clear.
func saveSentSecrets(ctx context.Context, s Settings, id string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	plain := make(map[string]string, len(values))
	for name, v := range values {
		plain[secretField(name)] = v
	}
	enc := configsecrets.Encrypt(plain)
	for k := range plain {
		if !configsecrets.IsEncrypted(enc[k]) {
			return errors.New("the secret values sent with the promotion could not be encrypted, so it was not stored; check the master key")
		}
	}
	return putJSON(ctx, s, keyPromotionSecrets+id, enc)
}

func loadSentSecrets(ctx context.Context, s Settings, id string) (map[string]string, error) {
	stored := map[string]string{}
	if err := getJSON(ctx, s, keyPromotionSecrets+id, &stored); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range configsecrets.Decrypt(stored) {
		if name, ok := strings.CutSuffix(k, "_secret"); ok && v != "" {
			out[name] = v
		}
	}
	return out, nil
}

// dropSentSecrets forgets the values a decided promotion carried.
func dropSentSecrets(ctx context.Context, s Settings, id string) {
	if err := s.SaveSetting(ctx, keyPromotionSecrets+id, ""); err != nil {
		slog.Warn("environment: dropping the secret values a promotion carried", "promotion", id, "error", err)
	}
}

// valuesToCreate is what p's deploy creates from: the sender's values, with
// the approver's on top.
func (s *Service) valuesToCreate(ctx context.Context, p Promotion, approver map[string]string) (map[string]string, error) {
	if len(p.SecretsFilled) == 0 {
		return approver, nil
	}
	sent, err := loadSentSecrets(ctx, s.store, p.ID)
	if err != nil {
		return nil, err
	}
	for _, name := range p.SecretsFilled {
		if _, ok := sent[name]; !ok {
			return nil, fmt.Errorf("the value sent for secret %s could not be read; check the master key", name)
		}
	}
	maps.Copy(sent, approver)
	return sent, nil
}

// deploySecrets creates what p's deploy needs from the sender's values and
// the approver's, then forgets the sender's: they are either stored as
// secrets now or not wanted.
func (s *Service) deploySecrets(ctx context.Context, p Promotion, approver map[string]string, by string) (created, kept, missing []string, err error) {
	values, err := s.valuesToCreate(ctx, p, approver)
	if err != nil {
		return nil, nil, nil, err
	}
	created, kept, missing, err = s.fillSecrets(ctx, p, values, by)
	if err == nil && len(p.SecretsFilled) > 0 {
		dropSentSecrets(ctx, s.store, p.ID)
	}
	return created, kept, missing, err
}
