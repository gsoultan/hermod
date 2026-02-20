package dynamics365

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type SourceConfig struct {
	Resource     string `json:"resource"`      // e.g. https://org.crm.dynamics.com
	TenantID     string `json:"tenant_id"`     // Microsoft Entra Tenant ID
	ClientID     string `json:"client_id"`     // App Registration Client ID
	ClientSecret string `json:"client_secret"` // App Registration Client Secret
	Entity       string `json:"entity"`        // OData Entity Set Name (e.g., "accounts")
	PollInterval string `json:"poll_interval"` // e.g., "1m"
	Filter       string `json:"filter"`        // OData $filter expression
	IDField      string `json:"id_field"`      // Field to use for delta tracking (e.g., "modifiedon")
}

type Source struct {
	config SourceConfig
	logger hermod.Logger
	client *http.Client
	token  string
	expiry time.Time
	lastID string
	mu     sync.RWMutex
}

func NewSource(config SourceConfig, logger hermod.Logger) *Source {
	return &Source{
		config: config,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Source) GetState() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]string{"last_id": s.lastID}
}

func (s *Source) SetState(state map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastID = state["last_id"]
}

func (s *Source) authenticate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && time.Now().Before(s.expiry) {
		return nil
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", s.config.TenantID)

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", s.config.ClientID)
	data.Set("client_secret", s.config.ClientSecret)
	data.Set("scope", s.config.Resource+"/.default")

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dynamics 365 auth failed (%d): %s", resp.StatusCode, string(body))
	}

	var auth struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return err
	}

	s.token = auth.AccessToken
	s.expiry = time.Now().Add(time.Duration(auth.ExpiresIn-60) * time.Second)
	return nil
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.authenticate(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	token := s.token
	lastID := s.lastID
	s.mu.RUnlock()

	// Build Web API URL
	apiURL := fmt.Sprintf("%s/api/data/v9.2/%s", strings.TrimSuffix(s.config.Resource, "/"), s.config.Entity)

	params := url.Values{}
	params.Add("$top", "1") // Read one record at a time for the source interface

	filterParts := []string{}
	if s.config.Filter != "" {
		filterParts = append(filterParts, "("+s.config.Filter+")")
	}

	if s.config.IDField != "" && lastID != "" {
		// Assume IDField is a date/time field like modifiedon for delta tracking
		filterParts = append(filterParts, fmt.Sprintf("%s gt %s", s.config.IDField, lastID))
	}

	if len(filterParts) > 0 {
		params.Add("$filter", strings.Join(filterParts, " and "))
	}

	if s.config.IDField != "" {
		params.Add("$orderby", s.config.IDField+" asc")
	}

	fullURL := apiURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-MaxVersion", "4.0")
	req.Header.Set("OData-Version", "4.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		s.mu.Lock()
		s.token = "" // Force re-auth next time
		s.mu.Unlock()
		return nil, fmt.Errorf("dynamics 365: unauthorized")
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dynamics 365 error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Value []map[string]any `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Value) == 0 {
		return nil, nil // No new data
	}

	record := result.Value[0]

	// Update lastID for delta tracking
	if s.config.IDField != "" {
		if val, ok := record[s.config.IDField]; ok {
			s.mu.Lock()
			s.lastID = fmt.Sprintf("%v", val)
			s.mu.Unlock()
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("d365_%d", time.Now().UnixNano()))
	for k, v := range record {
		msg.SetData(k, v)
	}

	return msg, nil
}

func (s *Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	if err := s.authenticate(ctx); err != nil {
		return err
	}

	apiURL := fmt.Sprintf("%s/api/data/v9.2/$metadata", strings.TrimSuffix(s.config.Resource, "/"))
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)

	s.mu.RLock()
	token := s.token
	s.mu.RUnlock()

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("dynamics 365 ping failed: %d", resp.StatusCode)
	}

	return nil
}

func (s *Source) Close() error {
	return nil
}

func (s *Source) Name() string {
	return "dynamics365"
}
