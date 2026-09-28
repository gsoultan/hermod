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

	// The gateway and the key decide where a tenant credential is sent, so row
	// data must not be able to choose either. They resolve against no data:
	// {{env.X}} and {{secret("X")}} work, {{.field}} renders empty.
	baseURL := strings.TrimSpace(evaluator.ResolveTemplate(core.GetConfigString(config, "baseUrl"), nil))
	apiKey := strings.TrimSpace(evaluator.ResolveTemplate(core.GetConfigString(config, "apiKey"), nil))
	if baseURL == "" || apiKey == "" {
		return msg, errors.New("panmail_providers: a gateway url and an api key are required")
	}

	targetField := core.GetConfigString(config, "targetField")
	if targetField == "" {
		targetField = defaultPanmailProvidersField
	}

	var kind sdk.ProviderType
	if s := strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "providerType"))); s != "" {
		k, known := panmailProviderTypes[s]
		if !known {
			return msg, fmt.Errorf("panmail_providers: unknown provider type %q", s)
		}
		kind = k
	}

	// A name filter is only a search, so it may come from the row.
	name := evaluator.ResolveTemplateMsg(core.GetConfigString(config, "name"), msg)

	ttl, err := resolveLookupTTL(core.GetConfigString(config, "ttl"), defaultPanmailProvidersTTL)
	if err != nil {
		return msg, fmt.Errorf("panmail_providers: %w", err)
	}

	cacheKey := panmailProvidersCacheKey(baseURL, apiKey, name, kind)
	if ttl.cache {
		if cached, found := registry.GetLookupCache(cacheKey); found {
			msg.SetData(targetField, cached)
			return msg, nil
		}
	}

	res, err, _ := t.sf.Do(cacheKey, func() (any, error) {
		opts := []sdk.Option{}
		if s := core.GetConfigString(config, "timeout"); s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				return nil, fmt.Errorf("timeout %q is not a duration (e.g. %q)", s, "10s")
			}
			opts = append(opts, sdk.WithTimeout(d))
		}
		// sdk.New refuses a url without a scheme, credentials in the url, and
		// plain http away from loopback. The errors name the url, never the key.
		client, err := sdk.New(baseURL, apiKey, opts...)
		if err != nil {
			return nil, err
		}
		providers, err := client.ListProviders(ctx, sdk.ProviderFilter{Name: name, Type: kind})
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
	})
	if err != nil {
		return msg, fmt.Errorf("panmail_providers: %w", err)
	}

	if ttl.cache {
		registry.SetLookupCache(cacheKey, res, ttl.duration)
	}
	msg.SetData(targetField, res)
	return msg, nil
}
