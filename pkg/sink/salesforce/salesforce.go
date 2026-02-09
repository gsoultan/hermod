package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
)

// SalesforceSink implements the hermod.Sink interface for Salesforce Bulk API 2.0.
type SalesforceSink struct {
	instanceURL   string
	accessToken   string
	clientID      string
	clientSecret  string
	username      string
	password      string
	securityToken string
	object        string
	operation     string // insert, update, upsert, delete
	externalID    string // for upsert

	client *http.Client
	mu     sync.RWMutex
}

type authResponse struct {
	AccessToken string `json:"access_token"`
	InstanceURL string `json:"instance_url"`
}

func NewSalesforceSink(clientID, clientSecret, username, password, securityToken, object, operation, externalID string) *SalesforceSink {
	return &SalesforceSink{
		clientID:      clientID,
		clientSecret:  clientSecret,
		username:      username,
		password:      password,
		securityToken: securityToken,
		object:        object,
		operation:     operation,
		externalID:    externalID,
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *SalesforceSink) authenticate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	endpoint := "https://login.salesforce.com/services/oauth2/token"
	if strings.Contains(s.username, ".sandbox") {
		endpoint = "https://test.salesforce.com/services/oauth2/token"
	}

	payload := fmt.Sprintf("grant_type=password&client_id=%s&client_secret=%s&username=%s&password=%s%s",
		s.clientID, s.clientSecret, s.username, s.password, s.securityToken)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(payload))
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
		return fmt.Errorf("salesforce auth failed (%d): %s", resp.StatusCode, string(body))
	}

	var auth authResponse
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return err
	}

	s.accessToken = auth.AccessToken
	s.instanceURL = auth.InstanceURL
	return nil
}

func (s *SalesforceSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}

	if s.accessToken == "" {
		if err := s.authenticate(ctx); err != nil {
			return err
		}
	}

	return s.writeRecord(ctx, msg)
}

func (s *SalesforceSink) writeRecord(ctx context.Context, msg hermod.Message) error {
	data := msg.Data()
	s.mu.RLock()
	instanceURL := s.instanceURL
	accessToken := s.accessToken
	s.mu.RUnlock()

	// REST API SObject endpoint
	url := fmt.Sprintf("%s/services/data/v59.0/sobjects/%s", instanceURL, s.object)
	method := "POST"

	if s.operation == "update" || s.operation == "upsert" {
		idVal := evaluator.GetMsgValByPath(msg, "Id")
		if idVal == nil {
			idVal = evaluator.GetMsgValByPath(msg, "after.Id")
		}
		id, _ := idVal.(string)

		if s.operation == "upsert" && s.externalID != "" {
			extVal := fmt.Sprintf("%v", evaluator.GetMsgValByPath(msg, s.externalID))
			if extVal != "" && extVal != "<nil>" {
				url = fmt.Sprintf("%s/services/data/v59.0/sobjects/%s/%s/%s", instanceURL, s.object, s.externalID, extVal)
				method = "PATCH"
			}
		} else if id != "" {
			url = fmt.Sprintf("%s/services/data/v59.0/sobjects/%s/%s", instanceURL, s.object, id)
			method = "PATCH"
		}
	} else if s.operation == "delete" {
		idVal := evaluator.GetMsgValByPath(msg, "Id")
		if idVal == nil {
			idVal = evaluator.GetMsgValByPath(msg, "before.Id")
		}
		id, _ := idVal.(string)

		if id != "" {
			url = fmt.Sprintf("%s/services/data/v59.0/sobjects/%s/%s", instanceURL, s.object, id)
			method = "DELETE"
			data = nil
		}
	}

	var body io.Reader
	if data != nil {
		jsonData, _ := json.Marshal(data)
		body = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// Token expired, re-auth and retry once
		if err := s.authenticate(ctx); err != nil {
			return err
		}
		return s.writeRecord(ctx, msg)
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("salesforce api error (%d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *SalesforceSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	if s.accessToken == "" {
		if err := s.authenticate(ctx); err != nil {
			return err
		}
	}

	// 1. Create Job
	jobID, err := s.createJob(ctx)
	if err != nil {
		return err
	}

	// 2. Upload Data (CSV format is best for Bulk 2.0)
	if err := s.uploadJobData(ctx, jobID, msgs); err != nil {
		return err
	}

	// 3. Close Job
	return s.closeJob(ctx, jobID)
}

func (s *SalesforceSink) createJob(ctx context.Context) (string, error) {
	s.mu.RLock()
	url := fmt.Sprintf("%s/services/data/v59.0/jobs/ingest", s.instanceURL)
	accessToken := s.accessToken
	s.mu.RUnlock()

	jobSpec := map[string]string{
		"object":      s.object,
		"contentType": "CSV",
		"operation":   s.operation,
		"lineEnding":  "LF",
	}
	if s.operation == "upsert" {
		jobSpec["externalIdFieldName"] = s.externalID
	}

	body, _ := json.Marshal(jobSpec)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	return res.ID, nil
}

func (s *SalesforceSink) uploadJobData(ctx context.Context, jobID string, msgs []hermod.Message) error {
	s.mu.RLock()
	url := fmt.Sprintf("%s/services/data/v59.0/jobs/ingest/%s/batches", s.instanceURL, jobID)
	accessToken := s.accessToken
	s.mu.RUnlock()

	// Convert messages to CSV
	if len(msgs) == 0 {
		return nil
	}

	// Get headers from first message
	var headers []string
	firstData := msgs[0].Data()
	for k := range firstData {
		headers = append(headers, k)
	}

	var csvBuf bytes.Buffer
	csvBuf.WriteString(strings.Join(headers, ",") + "\n")
	for _, msg := range msgs {
		data := msg.Data()
		var row []string
		for _, h := range headers {
			val := data[h]
			row = append(row, fmt.Sprintf("\"%v\"", val)) // Simple quoting
		}
		csvBuf.WriteString(strings.Join(row, ",") + "\n")
	}

	req, _ := http.NewRequestWithContext(ctx, "PUT", url, &csvBuf)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "text/csv")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (s *SalesforceSink) closeJob(ctx context.Context, jobID string) error {
	s.mu.RLock()
	url := fmt.Sprintf("%s/services/data/v59.0/jobs/ingest/%s", s.instanceURL, jobID)
	accessToken := s.accessToken
	s.mu.RUnlock()

	body := []byte(`{"state":"UploadComplete"}`)
	req, _ := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (s *SalesforceSink) Ping(ctx context.Context) error {
	if s.accessToken == "" {
		return s.authenticate(ctx)
	}
	s.mu.RLock()
	url := fmt.Sprintf("%s/services/data/v59.0/", s.instanceURL)
	accessToken := s.accessToken
	s.mu.RUnlock()

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return s.authenticate(ctx)
	}
	return nil
}

func (s *SalesforceSink) Close() error {
	return nil
}
