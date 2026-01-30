package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type ElasticsearchSink struct {
	client    *elasticsearch.Client
	index     string // Template supported
	formatter hermod.Formatter
}

func NewElasticsearchSink(addresses []string, username, password, apiKey, index string, formatter hermod.Formatter) (*ElasticsearchSink, error) {
	cfg := elasticsearch.Config{
		Addresses: addresses,
		Username:  username,
		Password:  password,
		APIKey:    apiKey,
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create elasticsearch client: %w", err)
	}

	return &ElasticsearchSink{
		client:    client,
		index:     index,
		formatter: formatter,
	}, nil
}

func (s *ElasticsearchSink) Write(ctx context.Context, msg hermod.Message) error {
	index, err := s.renderIndex(msg)
	if err != nil {
		return fmt.Errorf("failed to render index: %w", err)
	}

	op := msg.Operation()
	if op == "" {
		op = hermod.OpCreate
	}

	var res *esapi.Response
	switch op {
	case hermod.OpDelete:
		req := esapi.DeleteRequest{
			Index:      index,
			DocumentID: msg.ID(),
			Refresh:    "true",
		}
		res, err = req.Do(ctx, s.client)
	default:
		var data []byte
		if s.formatter != nil {
			data, err = s.formatter.Format(msg)
			if err != nil {
				return fmt.Errorf("failed to format message: %w", err)
			}
		} else {
			data = msg.Payload()
		}

		req := esapi.IndexRequest{
			Index:      index,
			DocumentID: msg.ID(),
			Body:       bytes.NewReader(data),
			Refresh:    "true",
		}
		res, err = req.Do(ctx, s.client)
	}

	if err != nil {
		return fmt.Errorf("failed to execute elasticsearch request: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("elasticsearch request error: %s", res.String())
	}

	return nil
}

func (s *ElasticsearchSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, msg := range msgs {
		if msg == nil {
			continue
		}

		index, err := s.renderIndex(msg)
		if err != nil {
			return fmt.Errorf("failed to render index for message %s: %w", msg.ID(), err)
		}

		op := msg.Operation()
		if op == "" {
			op = hermod.OpCreate
		}

		switch op {
		case hermod.OpDelete:
			buf.WriteString(fmt.Sprintf(`{ "delete" : { "_index" : "%s", "_id" : "%s" } }%s`, index, msg.ID(), "\n"))
		default:
			var data []byte
			if s.formatter != nil {
				data, err = s.formatter.Format(msg)
				if err != nil {
					return fmt.Errorf("failed to format message %s: %w", msg.ID(), err)
				}
			} else {
				data = msg.Payload()
			}
			buf.WriteString(fmt.Sprintf(`{ "index" : { "_index" : "%s", "_id" : "%s" } }%s`, index, msg.ID(), "\n"))
			buf.Write(data)
			buf.WriteByte('\n')
		}
	}

	if buf.Len() == 0 {
		return nil
	}

	res, err := s.client.Bulk(bytes.NewReader(buf.Bytes()), s.client.Bulk.WithContext(ctx), s.client.Bulk.WithRefresh("true"))
	if err != nil {
		return fmt.Errorf("failed to execute bulk request: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("bulk request error: %s", res.String())
	}

	var bulkRes struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
			Error  struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}

	if err := json.NewDecoder(res.Body).Decode(&bulkRes); err != nil {
		return fmt.Errorf("failed to decode bulk response: %w", err)
	}

	if bulkRes.Errors {
		for _, item := range bulkRes.Items {
			for op, details := range item {
				if details.Status >= 300 {
					return fmt.Errorf("bulk item error (%s): %s %s", op, details.Error.Type, details.Error.Reason)
				}
			}
		}
	}

	return nil
}

func (s *ElasticsearchSink) Ping(ctx context.Context) error {
	res, err := s.client.Info(s.client.Info.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("ping error: %s", res.String())
	}
	return nil
}

func (s *ElasticsearchSink) Close() error {
	return nil
}

func (s *ElasticsearchSink) renderIndex(msg hermod.Message) (string, error) {
	if !strings.Contains(s.index, "{{") {
		return s.index, nil
	}

	tmpl, err := template.New("index").Parse(s.index)
	if err != nil {
		return "", err
	}

	data := msg.Data()
	templateData := make(map[string]interface{})
	for k, v := range data {
		templateData[k] = v
	}

	// Ensure system fields are available if not shadowed
	if _, ok := templateData["id"]; !ok {
		templateData["id"] = msg.ID()
	}
	if _, ok := templateData["operation"]; !ok {
		templateData["operation"] = msg.Operation()
	}
	if _, ok := templateData["table"]; !ok {
		templateData["table"] = msg.Table()
	}
	if _, ok := templateData["schema"]; !ok {
		templateData["schema"] = msg.Schema()
	}
	if _, ok := templateData["metadata"]; !ok {
		templateData["metadata"] = msg.Metadata()
	}

	templateData = prepareTemplateData(templateData)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Browse implements the hermod.Browser interface.
func (s *ElasticsearchSink) Browse(ctx context.Context, index string, limit int) ([]hermod.Message, error) {
	if index == "" {
		// If index is not provided, try to use s.index.
		// If it's a template, we might need a dummy message to render it, or just use it as is if it has no templates.
		if strings.Contains(s.index, "{{") {
			// For templates, we can't easily know the index without context.
			// However, ES allows wildcards. Let's try to replace template parts with * for discovery?
			// That might be too complex. Let's just use it as is and let ES fail if it's invalid.
			index = s.index
		} else {
			index = s.index
		}
	}

	searchReq := map[string]interface{}{
		"size": limit,
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
		"sort": []map[string]interface{}{
			{"_doc": "asc"},
		},
	}

	data, err := json.Marshal(searchReq)
	if err != nil {
		return nil, err
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(index),
		s.client.Search.WithBody(bytes.NewReader(data)),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search error: %s", res.String())
	}

	var searchRes struct {
		Hits struct {
			Hits []struct {
				ID     string          `json:"_id"`
				Index  string          `json:"_index"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&searchRes); err != nil {
		return nil, err
	}

	messages := make([]hermod.Message, 0, len(searchRes.Hits.Hits))
	for _, hit := range searchRes.Hits.Hits {
		msg := message.AcquireMessage()
		msg.SetID(hit.ID)
		msg.SetPayload(hit.Source)
		// Store the index in metadata so we can Ack it later
		msg.SetMetadata("_elasticsearch_index", hit.Index)
		messages = append(messages, msg)
	}

	return messages, nil
}

// Read implements the hermod.Source interface.
func (s *ElasticsearchSink) Read(ctx context.Context) (hermod.Message, error) {
	msgs, err := s.Browse(ctx, "", 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return msgs[0], nil
}

// Ack implements the hermod.Source interface.
func (s *ElasticsearchSink) Ack(ctx context.Context, msg hermod.Message) error {
	index := msg.Metadata()["_elasticsearch_index"]
	if index == "" {
		// Fallback to rendered index if possible, though it's risky
		var err error
		index, err = s.renderIndex(msg)
		if err != nil {
			return fmt.Errorf("failed to determine index for Ack: %w", err)
		}
	}

	req := esapi.DeleteRequest{
		Index:      index,
		DocumentID: msg.ID(),
		Refresh:    "true",
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		// If already deleted, it might return 404, which is fine for Ack
		if res.StatusCode == 404 {
			return nil
		}
		return fmt.Errorf("delete error: %s", res.String())
	}
	return nil
}

// DiscoverDatabases implements the hermod.Discoverer interface.
func (s *ElasticsearchSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	res, err := s.client.Info(s.client.Info.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var info map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, err
	}
	if name, ok := info["cluster_name"].(string); ok {
		return []string{name}, nil
	}
	return []string{"elasticsearch"}, nil
}

// DiscoverTables implements the hermod.Discoverer interface.
func (s *ElasticsearchSink) DiscoverTables(ctx context.Context) ([]string, error) {
	res, err := s.client.Cat.Indices(s.client.Cat.Indices.WithContext(ctx), s.client.Cat.Indices.WithFormat("json"))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var indices []map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&indices); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(indices))
	for _, idx := range indices {
		if name, ok := idx["index"].(string); ok {
			// Filter out internal indices starting with .
			if !strings.HasPrefix(name, ".") {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

func prepareTemplateData(data map[string]interface{}) map[string]interface{} {
	// Clone data to avoid modifying the original message data
	newData := make(map[string]interface{}, len(data))
	for k, v := range data {
		newData[k] = v
	}

	// Try to unmarshal 'after' and 'before' if they are JSON strings
	for _, key := range []string{"after", "before"} {
		if val, ok := newData[key]; ok {
			var nested map[string]interface{}
			if str, ok := val.(string); ok && strings.HasPrefix(strings.TrimSpace(str), "{") {
				if err := json.Unmarshal([]byte(str), &nested); err == nil {
					newData[key] = nested
				}
			} else if b, ok := val.([]byte); ok && len(b) > 0 && b[0] == '{' {
				if err := json.Unmarshal(b, &nested); err == nil {
					newData[key] = nested
				}
			} else if m, ok := val.(map[string]interface{}); ok {
				nested = m
			}

			// If we have unmarshaled/nested data, and it's 'after', also flatten it to the root
			// for easier access (if not colliding with existing fields).
			if key == "after" && nested != nil {
				for nk, nv := range nested {
					if _, exists := newData[nk]; !exists {
						newData[nk] = nv
					}
				}
			}
		}
	}
	return newData
}
