package googleanalytics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"google.golang.org/api/analyticsdata/v1beta"
	"google.golang.org/api/option"
)

type GoogleAnalyticsSource struct {
	propertyID      string
	credentialsJSON string
	metrics         string
	dimensions      string
	pollInterval    time.Duration
	logger          hermod.Logger
	svc             *analyticsdata.Service
	lastFetch       time.Time
	rows            []*analyticsdata.Row
	currentRow      int
}

func NewGoogleAnalyticsSource(propertyID, credentialsJSON, metrics, dimensions string, pollInterval time.Duration) *GoogleAnalyticsSource {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Hour
	}
	return &GoogleAnalyticsSource{
		propertyID:      propertyID,
		credentialsJSON: credentialsJSON,
		metrics:         metrics,
		dimensions:      dimensions,
		pollInterval:    pollInterval,
	}
}

func (s *GoogleAnalyticsSource) SetLogger(logger hermod.Logger) {
	s.logger = logger
}

func (s *GoogleAnalyticsSource) init(ctx context.Context) error {
	if s.svc != nil {
		return nil
	}

	var opts []option.ClientOption
	if s.credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(s.credentialsJSON)))
	}
	opts = append(opts, option.WithScopes(analyticsdata.AnalyticsReadonlyScope))

	svc, err := analyticsdata.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create analytics service: %w", err)
	}
	s.svc = svc
	return nil
}

func (s *GoogleAnalyticsSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	for {
		if s.currentRow < len(s.rows) {
			row := s.rows[s.currentRow]
			s.currentRow++

			data := make(map[string]interface{})

			// Process dimensions
			dimNames := strings.Split(s.dimensions, ",")
			for i, dim := range row.DimensionValues {
				name := "dimension_" + fmt.Sprint(i)
				if i < len(dimNames) {
					name = strings.TrimSpace(dimNames[i])
				}
				data[name] = dim.Value
			}

			// Process metrics
			metricNames := strings.Split(s.metrics, ",")
			for i, metric := range row.MetricValues {
				name := "metric_" + fmt.Sprint(i)
				if i < len(metricNames) {
					name = strings.TrimSpace(metricNames[i])
				}
				data[name] = metric.Value
			}

			payload, _ := json.Marshal(data)
			msg := message.AcquireMessage()
			msg.SetID(fmt.Sprintf("%s-%d-%d", s.propertyID, s.lastFetch.Unix(), s.currentRow))
			msg.SetOperation(hermod.OpCreate)
			msg.SetTable("report")
			msg.SetAfter(payload)
			msg.SetMetadata("source", "googleanalytics")
			msg.SetMetadata("property_id", s.propertyID)
			msg.SetMetadata("fetch_time", s.lastFetch.Format(time.RFC3339))

			return msg, nil
		}

		// Wait for next poll interval if we just finished rows
		if !s.lastFetch.IsZero() {
			elapsed := time.Since(s.lastFetch)
			if elapsed < s.pollInterval {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(s.pollInterval - elapsed):
				}
			}
		}

		// Fetch new report
		if err := s.fetchReport(ctx); err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to fetch analytics report", "error", err)
			}
			// Sleep a bit on error before retrying
			time.Sleep(10 * time.Second)
			continue
		}
	}
}

func (s *GoogleAnalyticsSource) fetchReport(ctx context.Context) error {
	mList := strings.Split(s.metrics, ",")
	var metrics []*analyticsdata.Metric
	for _, m := range mList {
		m = strings.TrimSpace(m)
		if m != "" {
			metrics = append(metrics, &analyticsdata.Metric{Name: m})
		}
	}

	dList := strings.Split(s.dimensions, ",")
	var dimensions []*analyticsdata.Dimension
	for _, d := range dList {
		d = strings.TrimSpace(d)
		if d != "" {
			dimensions = append(dimensions, &analyticsdata.Dimension{Name: d})
		}
	}

	req := &analyticsdata.RunReportRequest{
		DateRanges: []*analyticsdata.DateRange{
			{StartDate: "yesterday", EndDate: "today"},
		},
		Dimensions: dimensions,
		Metrics:    metrics,
	}

	resp, err := s.svc.Properties.RunReport("properties/"+s.propertyID, req).Context(ctx).Do()
	if err != nil {
		return err
	}

	s.rows = resp.Rows
	s.currentRow = 0
	s.lastFetch = time.Now()
	return nil
}

func (s *GoogleAnalyticsSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *GoogleAnalyticsSource) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	// Try a very simple report to ping
	req := &analyticsdata.RunReportRequest{
		DateRanges: []*analyticsdata.DateRange{{StartDate: "today", EndDate: "today"}},
		Metrics:    []*analyticsdata.Metric{{Name: "activeUsers"}},
	}
	_, err := s.svc.Properties.RunReport("properties/"+s.propertyID, req).Context(ctx).Do()
	return err
}

func (s *GoogleAnalyticsSource) Close() error {
	return nil
}

func (s *GoogleAnalyticsSource) GetState() map[string]string {
	return map[string]string{
		"last_fetch": s.lastFetch.Format(time.RFC3339),
	}
}

func (s *GoogleAnalyticsSource) SetState(state map[string]string) {
	if val, ok := state["last_fetch"]; ok {
		s.lastFetch, _ = time.Parse(time.RFC3339, val)
	}
}
