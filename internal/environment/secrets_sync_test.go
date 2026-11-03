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
	"slices"
	"strings"
	"sync"
	"testing"
)

// memVault is one instance's vhost secrets, with values, as the Secrets page
// keeps them.
type memVault struct {
	mu     sync.Mutex
	values map[string]string // vhost/name -> value
	envs   map[string]bool
}

func newVault() *memVault {
	return &memVault{values: map[string]string{}, envs: map[string]bool{}}
}

func (v *memVault) HasSecret(_ context.Context, vhost, name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.values[vhost+"/"+name]
	return ok
}

func (v *memVault) HasEnv(name string) bool { return v.envs[name] }

func (v *memVault) CreateSecret(_ context.Context, vhost, name, value, _ string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.values[vhost+"/"+name]; ok {
		return false, nil
	}
	v.values[vhost+"/"+name] = value
	return true, nil
}

// crmKey reads the default vhost's CRM_KEY, the one secret these tests use.
func (v *memVault) crmKey() (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	val, ok := v.values["default/CRM_KEY"]
	return val, ok
}

// vaulted is promotable with a real secret store on production, which lacks
// CRM_KEY.
func vaulted(t *testing.T) (*pair, Link, *memVault) {
	t.Helper()
	p, l := promotable(t)
	vault := newVault()
	vault.envs["REGION"] = true
	p.prd.secrets = vault
	return p, l, vault
}

// sendOrders promotes ordersWorkflow and returns the promotion as production
// holds it.
func sendOrders(t *testing.T, p *pair, l Link) Promotion {
	t.Helper()
	sent, err := p.stg.Promote(context.Background(), l.ID, "wf-orders", "gembit", PromoteOptions{Start: true})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	got, err := p.prd.promotion(context.Background(), sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPreviewListsTheSecretsProductionLacks(t *testing.T) {
	p, l, _ := vaulted(t)
	pv, err := p.stg.Preview(context.Background(), l.ID, "wf-orders", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pv.MissingSecrets, []string{"CRM_KEY"}) {
		t.Fatalf("missing secrets = %v, want [CRM_KEY]", pv.MissingSecrets)
	}
	if c, _ := checkNamed(pv.Checks, "Secrets exist"); c.OK || c.Blocking || !strings.Contains(c.Detail, "approv") {
		t.Fatalf("check = %+v, want a warning saying the approver fills them in", c)
	}
}

func TestAPromotionNamesTheSecretsProductionLacks(t *testing.T) {
	p, l, vault := vaulted(t)
	got := sendOrders(t, p, l)
	if !slices.Equal(got.Secrets, []string{"CRM_KEY"}) {
		t.Fatalf("promotion secrets = %v, want [CRM_KEY]", got.Secrets)
	}
	if len(vault.values) != 0 {
		t.Fatal("a secret was created before anyone approved")
	}
}

func TestApproveCreatesTheSecretsTheApproverFilledIn(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent := sendOrders(t, p, l)

	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "prod-crm-key"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.Status != StatusDeployed || !slices.Equal(got.SecretsCopied, []string{"CRM_KEY"}) || len(got.SecretsMissing) != 0 {
		t.Fatalf("promotion = %+v", got)
	}
	if v, _ := vault.crmKey(); v != "prod-crm-key" {
		t.Fatalf("production's CRM_KEY = %q", v)
	}
	for k, v := range p.prdStore.settings {
		if strings.Contains(v, "prod-crm-key") {
			t.Fatalf("setting %s holds the secret's value", k)
		}
	}
}

func TestApproveNeverReplacesASecretProductionAlreadyHas(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent := sendOrders(t, p, l)
	// Someone sets it on the Secrets page while the promotion waits.
	vault.values["default/CRM_KEY"] = "set-on-the-secrets-page"

	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "typed-on-the-card"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := vault.crmKey(); v != "set-on-the-secrets-page" {
		t.Fatalf("production's CRM_KEY = %q, want the value it already had", v)
	}
	if len(got.SecretsCopied) != 0 || !slices.Equal(got.SecretsKept, []string{"CRM_KEY"}) {
		t.Fatalf("copied %v kept %v", got.SecretsCopied, got.SecretsKept)
	}
}

func TestApproveWithoutValuesDeploysAndSaysWhatIsStillMissing(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent := sendOrders(t, p, l)

	// A blank field is a value nobody typed, not an empty secret.
	got, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDeployed || !slices.Equal(got.SecretsMissing, []string{"CRM_KEY"}) {
		t.Fatalf("promotion = %+v", got)
	}
	if _, ok := vault.crmKey(); ok {
		t.Fatal("a blank value was stored")
	}
}

func TestApproveRefusesASecretThePromotionDoesNotNeed(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent := sendOrders(t, p, l)

	_, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "k", "ADMIN_PASSWORD": "planted"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "ADMIN_PASSWORD") || strings.Contains(err.Error(), "planted") {
		t.Fatalf("err = %v, want a refusal naming ADMIN_PASSWORD and never its value", err)
	}
	if got, _ := p.prd.promotion(ctx, sent.ID); got.Status != StatusPending {
		t.Fatalf("status = %s, want it still pending", got.Status)
	}
	if len(vault.values) != 0 {
		t.Fatal("a secret was created by a refused approval")
	}
}

func TestApproveRefusesAnInvalidSecretValue(t *testing.T) {
	ctx := context.Background()
	p, l, _ := vaulted(t)
	sent := sendOrders(t, p, l)
	_, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": strings.Repeat("x", 64*1024+1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestAnInstanceThatCannotStoreSecretsRefusesValues(t *testing.T) {
	ctx := context.Background()
	p, l := promotable(t)
	delete(p.secrets.secrets, "default/CRM_KEY")
	sent := sendOrders(t, p, l)
	if _, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "k"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestApprovingAReleaseFillsInTheSecretsOfEveryWorkflow(t *testing.T) {
	ctx := context.Background()
	p, _, vault := vaulted(t)
	sent, err := p.stg.SendRelease(ctx, items("wf-orders"), "gembit", PromoteOptions{})
	if err != nil {
		t.Fatalf("SendRelease: %v", err)
	}
	got, err := p.prd.ApproveWith(ctx, sent[0].Promotions[0].ID, "rina", map[string]string{"CRM_KEY": "prod-crm-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.SecretsCopied, []string{"CRM_KEY"}) {
		t.Fatalf("promotion = %+v", got)
	}
	if v, _ := vault.crmKey(); v != "prod-crm-key" {
		t.Fatalf("production's CRM_KEY = %q", v)
	}
}

// Values typed by the sender, for production.

func TestTheSenderCanTypeProductionsValuesWhenPromoting(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "prod-typed-on-dev"}})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if !slices.Equal(sent.Secrets, []string{"CRM_KEY"}) || !slices.Equal(sent.SecretsFilled, []string{"CRM_KEY"}) {
		t.Fatalf("sent = %+v", sent)
	}
	if _, ok := vault.crmKey(); ok {
		t.Fatal("the secret was created before the promotion was approved")
	}
	for k, v := range p.prdStore.settings {
		if strings.Contains(v, "prod-typed-on-dev") {
			t.Fatalf("setting %s holds the value in the clear", k)
		}
	}
	for k, v := range p.stgStore.settings {
		if strings.Contains(v, "prod-typed-on-dev") {
			t.Fatalf("staging kept the value in %s", k)
		}
	}

	got, err := p.prd.Approve(ctx, sent.ID, "rina")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.SecretsCopied, []string{"CRM_KEY"}) {
		t.Fatalf("promotion = %+v", got)
	}
	if v, _ := vault.crmKey(); v != "prod-typed-on-dev" {
		t.Fatalf("production's CRM_KEY = %q", v)
	}
	if v := p.prdStore.settings[keyPromotionSecrets+sent.ID]; v != "" {
		t.Fatalf("the carried values were kept after the deploy: %q", v)
	}
}

func TestTheApproversValueWinsOverTheSenders(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "from-sender"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.ApproveWith(ctx, sent.ID, "rina", map[string]string{"CRM_KEY": "from-approver"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := vault.crmKey(); v != "from-approver" {
		t.Fatalf("production's CRM_KEY = %q", v)
	}
}

func TestSentValuesNeverReplaceASecretProductionHas(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "from-sender"}})
	if err != nil {
		t.Fatal(err)
	}
	vault.values["default/CRM_KEY"] = "already-there"
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); err != nil {
		t.Fatal(err)
	}
	if v, _ := vault.crmKey(); v != "already-there" {
		t.Fatalf("production's CRM_KEY = %q, want it kept", v)
	}
}

func TestRejectingDropsTheSentValues(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "from-sender"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.prd.Reject(ctx, sent.ID, "rina", "no"); err != nil {
		t.Fatal(err)
	}
	if _, ok := vault.crmKey(); ok {
		t.Fatal("a rejected promotion created its secret")
	}
	if v := p.prdStore.settings[keyPromotionSecrets+sent.ID]; v != "" {
		t.Fatalf("a rejected promotion kept its values: %q", v)
	}
}

func TestAFailedDeployDropsTheSentValues(t *testing.T) {
	ctx := context.Background()
	p, l, vault := vaulted(t)
	sent, err := p.stg.Promote(ctx, l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "from-sender"}})
	if err != nil {
		t.Fatal(err)
	}
	delete(p.prdStore.sinks, "p-wh")
	if _, err := p.prd.Approve(ctx, sent.ID, "rina"); err == nil {
		t.Fatal("approve succeeded without the sink")
	}
	if _, ok := vault.crmKey(); ok {
		t.Fatal("a failed promotion created its secret")
	}
	if v := p.prdStore.settings[keyPromotionSecrets+sent.ID]; v != "" {
		t.Fatalf("a failed promotion kept its values: %q", v)
	}
}

func TestPromoteRefusesAValueForASecretProductionDoesNotNeed(t *testing.T) {
	p, l, _ := vaulted(t)
	_, err := p.stg.Promote(context.Background(), l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"OTHER": "planted"}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "OTHER") || strings.Contains(err.Error(), "planted") {
		t.Fatalf("err = %v", err)
	}
}

func TestReceiveRefusesAValueForASecretTheWorkflowDoesNotUse(t *testing.T) {
	ctx := context.Background()
	p, _, _ := vaulted(t)
	peers, _ := p.prd.Peers(ctx)
	wf := ordersWorkflow()
	wf.Nodes[0].RefID, wf.Nodes[1].Config["sourceId"], wf.Nodes[2].RefID = "p-orders", "p-crm", "p-wh"
	_, err := p.prd.Receive(ctx, peers[0], Submission{Workflow: wf, Sender: "gembit", Secrets: map[string]string{"ADMIN_PASSWORD": "planted"}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "ADMIN_PASSWORD") || strings.Contains(err.Error(), "planted") {
		t.Fatalf("err = %v", err)
	}
}

func TestValuesAreNotSentToAnInstanceThatCannotTakeThem(t *testing.T) {
	p, l, _ := vaulted(t)
	p.remote.old = true
	_, err := p.stg.Promote(context.Background(), l.ID, "wf-orders", "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "k"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want a refusal rather than values silently dropped", err)
	}
}

func TestAReleaseCarriesTheSendersValues(t *testing.T) {
	ctx := context.Background()
	p, _, vault := vaulted(t)
	sent, err := p.stg.SendRelease(ctx, items("wf-orders"), "gembit", PromoteOptions{SecretValues: map[string]string{"CRM_KEY": "from-sender"}})
	if err != nil {
		t.Fatalf("SendRelease: %v", err)
	}
	if _, err := p.prd.Approve(ctx, sent[0].Promotions[0].ID, "rina"); err != nil {
		t.Fatal(err)
	}
	if v, _ := vault.crmKey(); v != "from-sender" {
		t.Fatalf("production's CRM_KEY = %q", v)
	}
}
