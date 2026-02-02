package sap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type SourceConfig struct {
	Host         string `json:"host"`
	Client       string `json:"client"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	Service      string `json:"service"` // OData Service
	Entity       string `json:"entity"`  // OData Entity
	PollInterval string `json:"poll_interval"`
	Filter       string `json:"filter,omitempty"`   // OData $filter
	IDField      string `json:"id_field,omitempty"` // Field to use for delta tracking
}

type Source struct {
	config SourceConfig
	logger hermod.Logger
	client *http.Client
	lastID string
}

func NewSource(config SourceConfig, logger hermod.Logger) *Source {
	return &Source{
		config: config,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Source) GetState() map[string]string {
	return map[string]string{"last_id": s.lastID}
}

func (s *Source) SetState(state map[string]string) {
	s.lastID = state["last_id"]
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	// Simple polling implementation for OData
	url := fmt.Sprintf("%s/sap/opu/odata/sap/%s/%s", s.config.Host, s.config.Service, s.config.Entity)
	params := []string{"$top=1"} // For now, just poll one at a time or use delta

	// Delta tracking
	if s.config.IDField != "" && s.lastID != "" {
		deltaFilter := fmt.Sprintf("%s gt '%s'", s.config.IDField, s.lastID)
		if s.config.Filter != "" {
			params = append(params, fmt.Sprintf("$filter=(%s) and (%s)", s.config.Filter, deltaFilter))
		} else {
			params = append(params, "$filter="+deltaFilter)
		}
	} else if s.config.Filter != "" {
		params = append(params, "$filter="+s.config.Filter)
	}

	if s.config.Client != "" {
		params = append(params, "sap-client="+s.config.Client)
	}

	// Add format=json
	params = append(params, "$format=json")

	if s.config.IDField != "" {
		params = append(params, "$orderby="+s.config.IDField+" asc")
	}

	if len(params) > 0 {
		url += "?" + params[0]
		for i := 1; i < len(params); i++ {
			url += "&" + params[i]
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if s.config.Username != "" {
		req.SetBasicAuth(s.config.Username, s.config.Password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sap odata source error (%d): %s", resp.StatusCode, string(body))
	}

	var odataResp struct {
		D struct {
			Results []map[string]interface{} `json:"results"`
		} `json:"d"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&odataResp); err != nil {
		return nil, err
	}

	if len(odataResp.D.Results) == 0 {
		return nil, nil // No data
	}

	// Just return the first one for this Read call
	result := odataResp.D.Results[0]

	// Update lastID for delta tracking
	if s.config.IDField != "" {
		if val, ok := result[s.config.IDField]; ok {
			s.lastID = fmt.Sprintf("%v", val)
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sap_%d", time.Now().UnixNano()))
	for k, v := range result {
		msg.SetData(k, v)
	}

	return msg, nil
}

func (s *Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	url := fmt.Sprintf("%s/sap/opu/odata/sap/%s/$metadata", s.config.Host, s.config.Service)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if s.config.Username != "" {
		req.SetBasicAuth(s.config.Username, s.config.Password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sap source ping failed: %d", resp.StatusCode)
	}
	return nil
}

func (s *Source) Close() error {
	return nil
}

func (s *Source) Name() string {
	return "sap"
}
