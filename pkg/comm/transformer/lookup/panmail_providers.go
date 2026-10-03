package lookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	sdk "github.com/gsoultan/panmail-sdk"
	"golang.org/x/sync/singleflight"
)

func init() {
	transformer.Register("panmail_providers", &PanmailProvidersTransformer{
		sf: &singleflight.Group{},
	})
}

// PanmailProvidersTransformer writes the sending providers of a panmail tenant
// into the message, through panmail-sdk's ListProviders. Its main use is
// finding the provider id a panmail sink sends with.
type PanmailProvidersTransformer struct {
	sf *singleflight.Group
}

// defaultPanmailProvidersField is where the list goes when the node names no
// target field.
const defaultPanmailProvidersField = "panmail_providers"

// defaultPanmailProvidersTTL bounds how long a list is reused. Providers are
// configuration that changes by hand, and without a cache every message would
// be one more call to the gateway.
const defaultPanmailProvidersTTL = 5 * time.Minute

// panmailProviderTypes maps the editor's short names to the SDK's values.
var panmailProviderTypes = map[string]sdk.ProviderType{
	"smtp":     sdk.ProviderTypeSMTP,
	"sendgrid": sdk.ProviderTypeSendGrid,
	"ses":      sdk.ProviderTypeSES,
	"postmark": sdk.ProviderTypePostmark,
	"mailgun":  sdk.ProviderTypeMailgun,
	"imap":     sdk.ProviderTypeIMAP,
	"pop3":     sdk.ProviderTypePOP3,
}

// shortProviderType is the editor's name for a type: "PROVIDER_TYPE_SMTP" is
// "smtp". A value the SDK has no constant for is lowered the same way, so a
// newer gateway still produces something readable.
func shortProviderType(t sdk.ProviderType) string {
	return strings.ToLower(strings.TrimPrefix(string(t), "PROVIDER_TYPE_"))
}

// panmailProvidersCacheKey digests everything that selects the list. The api
// key is only ever inside the digest: cache keys are iterated and printed, and
// a tenant credential belongs in neither.
func panmailProvidersCacheKey(baseURL, apiKey, name string, kind sdk.ProviderType) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "url\x00%s\x00key\x00%s\x00name\x00%s\x00type\x00%s\x00", baseURL, apiKey, name, kind)
	return "panmail_providers:" + hex.EncodeToString(h.Sum(nil))
}

// panmailProvidersRequest is a node's config, resolved for one message.
type panmailProvidersRequest struct {
	baseURL, apiKey, name, targetField, timeout string
	kind                                        sdk.ProviderType
	ttl                                         lookupTTL
}

func resolvePanmailProvidersRequest(config map[string]any, msg hermod.Message) (panmailProvidersRequest, error) {
	// The gateway and the key decide where a tenant credential is sent, so row
	// data must not be able to choose either. They resolve against no data:
	// {{secret("X")}} reads the secret manager (HERMOD_SECRET_X by default),
	// {{.field}} and {{env.X}} render empty.
	req := panmailProvidersRequest{
		baseURL:     strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, "baseUrl"), msg)),
		apiKey:      strings.TrimSpace(evaluator.ResolveTemplateScoped(core.GetConfigString(config, "apiKey"), msg)),
		targetField: core.GetConfigString(config, "targetField"),
		timeout:     core.GetConfigString(config, "timeout"),
		// A name filter is only a search, so it may come from the row.
		name: evaluator.ResolveTemplateMsg(core.GetConfigString(config, "name"), msg),
	}
	if req.baseURL == "" || req.apiKey == "" {
		return req, errors.New("a gateway url and an api key are required")
	}
	if req.targetField == "" {
		req.targetField = defaultPanmailProvidersField
	}

	if s := strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "providerType"))); s != "" {
		k, known := panmailProviderTypes[s]
		if !known {
			return req, fmt.Errorf("unknown provider type %q", s)
		}
		req.kind = k
	}

	ttl, err := resolveLookupTTL(core.GetConfigString(config, "ttl"), defaultPanmailProvidersTTL)
	if err != nil {
		return req, err
	}
	req.ttl = ttl
	return req, nil
}

// fetch lists the providers and shapes them for the message. Connection
// settings are never copied: the SDK does not surface them.
func (req panmailProvidersRequest) fetch(ctx context.Context) ([]map[string]any, error) {
	opts := []sdk.Option{}
	if req.timeout != "" {
		d, err := time.ParseDuration(req.timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout %q is not a duration (e.g. %q)", req.timeout, "10s")
		}
		opts = append(opts, sdk.WithTimeout(d))
	}
	// sdk.New refuses a url without a scheme, credentials in the url, and plain
	// http away from loopback. Its errors name the url, never the key.
	client, err := sdk.New(req.baseURL, req.apiKey, opts...)
	if err != nil {
		return nil, err
	}
	providers, err := client.ListProviders(ctx, sdk.ProviderFilter{Name: req.name, Type: req.kind})
	if err != nil {
		return nil, err
	}

	list := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		domains := p.AllowedDomains
		if domains == nil {
			domains = []string{}
		}
		list = append(list, map[string]any{
			"id":             p.ID,
			"name":           p.Name,
			"type":           shortProviderType(p.Type),
			"allowedDomains": domains,
		})
	}
	return list, nil
}

func (t *PanmailProvidersTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	registry, ok := ctx.Value(hermod.RegistryKey).(interface {
		GetLookupCache(key string) (any, bool)
		SetLookupCache(key string, value any, ttl time.Duration)
	})
	if !ok {
		return msg, errors.New("registry not found in context")
	}

	req, err := resolvePanmailProvidersRequest(config, msg)
	if err != nil {
		return msg, fmt.Errorf("panmail_providers: %w", err)
	}

	cacheKey := panmailProvidersCacheKey(req.baseURL, req.apiKey, req.name, req.kind)
	if req.ttl.cache {
		if cached, found := registry.GetLookupCache(cacheKey); found {
			msg.SetData(req.targetField, cached)
			return msg, nil
		}
	}

	res, err, _ := t.sf.Do(cacheKey, func() (any, error) { return req.fetch(ctx) })
	if err != nil {
		return msg, fmt.Errorf("panmail_providers: %w", err)
	}

	if req.ttl.cache {
		registry.SetLookupCache(cacheKey, res, req.ttl.duration)
	}
	msg.SetData(req.targetField, res)
	return msg, nil
}
