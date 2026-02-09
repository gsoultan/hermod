package dynamics365

import (
	"bytes"
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
	"github.com/user/hermod/pkg/evaluator"
)

type Config struct {
	Resource     string `json:"resource"`
	TenantID     string `json:"tenant_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Entity       string `json:"entity"`    // Entity Set Name
	Operation    string `json:"operation"` // create, update, upsert, delete
	ExternalID   string `json:"external_id"`
}

type Sink struct {
	config Config
	logger hermod.Logger
	client *http.Client
	token  string
	expiry time.Time
	mu     sync.RWMutex
}

func NewSink(config Config, logger hermod.Logger) *Sink {
	return &Sink{
		config: config,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Sink) authenticate(ctx context.Context) error {
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

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if err := s.authenticate(ctx); err != nil {
		return err
	}

	s.mu.RLock()
	token := s.token
	s.mu.RUnlock()

	data := msg.Data()
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("%s/api/data/v9.2/%s", strings.TrimSuffix(s.config.Resource, "/"), s.config.Entity)
	method := "POST"

	switch s.config.Operation {
	case "update":
		id := fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, s.config.ExternalID))
		if id == "" || id == "<nil>" {
			return fmt.Errorf("dynamics 365: external id field %s not found in data", s.config.ExternalID)
		}
		apiURL = fmt.Sprintf("%s(%s)", apiURL, id)
		method = "PATCH"
	case "upsert":
		id := evaluator.GetMsgValByPath(msg, s.config.ExternalID)
		if id != nil && fmt.Sprintf("%v", id) != "" {
			apiURL = fmt.Sprintf("%s(%v)", apiURL, id)
			method = "PATCH"
		}
		// If ID is not present, it will use POST to apiURL which is create (standard OData behavior if we don't handle it specifically)
		// But in D365, PATCH with ID handles both update and create if the record doesn't exist (if configured so)
	case "delete":
		id := fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, s.config.ExternalID))
		if id == "" || id == "<nil>" {
			return fmt.Errorf("dynamics 365: external id field %s not found in data", s.config.ExternalID)
		}
		apiURL = fmt.Sprintf("%s(%s)", apiURL, id)
		method = "DELETE"
		payload = nil
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-MaxVersion", "4.0")
	req.Header.Set("OData-Version", "4.0")

	if s.config.Operation == "upsert" {
		// To support upsert with PATCH, we might need If-Match or other headers depending on D365 configuration
		// For now, keep it simple.
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
		return fmt.Errorf("dynamics 365: unauthorized")
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dynamics 365 api error (%d): %s", resp.StatusCode, string(body))
	}

	if s.logger != nil {
		s.logger.Info("Dynamics 365: record written", "entity", s.config.Entity, "operation", s.config.Operation, "message_id", msg.ID())
	}
	return nil
}

func (s *Sink) Ping(ctx context.Context) error {
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

func (s *Sink) Close() error {
	return nil
}

func (s *Sink) Name() string {
	return "dynamics365"
}
