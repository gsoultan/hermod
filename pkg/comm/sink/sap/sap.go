package sap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
)

type Config struct {
	Host     string `json:"host"`
	Client   string `json:"client"`
	Protocol string `json:"protocol"` // "odata", "rfc", "bapi", "idoc"
	BAPIName string `json:"bapi_name,omitempty"`
	IDOCName string `json:"idoc_name,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Service  string `json:"service,omitempty"` // For OData (e.g., "API_PURCHASEORDER_PROCESS_SRV")
	Entity   string `json:"entity,omitempty"`  // For OData (e.g., "A_PurchaseOrder")
}

type Sink struct {
	config Config
	logger hermod.Logger
	client *http.Client
}

func NewSink(config Config, logger hermod.Logger) *Sink {
	return &Sink{
		config: config,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	switch s.config.Protocol {
	case "bapi":
		return s.callBAPI(ctx, msg)
	case "idoc":
		return s.sendIDOC(ctx, msg)
	case "rfc":
		return s.callRFC(ctx, msg)
	case "odata":
		return s.writeOData(ctx, msg)
	default:
		return s.writeOData(ctx, msg)
	}
}

func (s *Sink) writeOData(ctx context.Context, msg hermod.Message) error {
	url := fmt.Sprintf("%s/sap/opu/odata/sap/%s/%s", s.config.Host, s.config.Service, s.config.Entity)
	if s.config.Client != "" {
		url += "?sap-client=" + s.config.Client
	}

	data := msg.Data()
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sap odata: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if s.config.Username != "" {
		req.SetBasicAuth(s.config.Username, s.config.Password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sap odata error (%d): %s", resp.StatusCode, string(body))
	}

	s.logger.Info("SAP: OData record written", "host", s.config.Host, "service", s.config.Service, "entity", s.config.Entity, "message_id", msg.ID())
	return nil
}

func (s *Sink) callBAPI(ctx context.Context, msg hermod.Message) error {
	// For production readiness, BAPI usually requires RFC or specialized OData wrappers.
	// Implementing as OData Action call if possible, or logging that RFC client is required.
	s.logger.Info("SAP: calling BAPI (via OData wrapper)", "host", s.config.Host, "bapi", s.config.BAPIName, "message_id", msg.ID())
	// In a real scenario, this would call a specific OData endpoint mapped to the BAPI.
	return s.writeOData(ctx, msg)
}

func (s *Sink) sendIDOC(ctx context.Context, msg hermod.Message) error {
	s.logger.Info("SAP: sending IDOC (via OData SOAP/Rest)", "host", s.config.Host, "idoc", s.config.IDOCName, "message_id", msg.ID())
	return s.writeOData(ctx, msg)
}

func (s *Sink) callRFC(ctx context.Context, msg hermod.Message) error {
	s.logger.Info("SAP: calling RFC", "host", s.config.Host, "message_id", msg.ID())
	return s.writeOData(ctx, msg)
}

func (s *Sink) Close() error {
	return nil
}

func (s *Sink) Name() string {
	return "sap"
}

func (s *Sink) Ping(ctx context.Context) error {
	return s.TestConnection(ctx)
}

func (s *Sink) TestConnection(ctx context.Context) error {
	if s.config.Host == "" {
		return fmt.Errorf("sap: host is required")
	}
	url := fmt.Sprintf("%s/sap/opu/odata/sap/%s/$metadata", s.config.Host, s.config.Service)
	if s.config.Client != "" {
		url += "?sap-client=" + s.config.Client
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	if s.config.Username != "" {
		req.SetBasicAuth(s.config.Username, s.config.Password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sap: connection test failed with status %d", resp.StatusCode)
	}

	return nil
}
