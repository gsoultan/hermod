package milvus

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"github.com/user/hermod"
)

// Config represents the Milvus sink configuration.
type Config struct {
	Address        string `json:"address"`
	CollectionName string `json:"collection_name"`
	PartitionName  string `json:"partition_name"`
	VectorColumn   string `json:"vector_column"`
	IDColumn       string `json:"id_column"`
	Username       string `json:"username"`
	Password       string `json:"password"`
}

// Sink implements the hermod.Sink interface for Milvus.
type Sink struct {
	config Config
	client client.Client
}

// NewSink creates a new Milvus sink.
func NewSink(cfg Config) *Sink {
	return &Sink{
		config: cfg,
	}
}

func (s *Sink) init(ctx context.Context) error {
	c, err := client.NewClient(ctx, client.Config{
		Address:  s.config.Address,
		Username: s.config.Username,
		Password: s.config.Password,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to milvus: %w", err)
	}
	s.client = c
	return nil
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// Prepare data for columnar insert
	var ids []int64
	var stringIDs []string
	var vectors [][]float32

	isStringID := false

	for _, msg := range msgs {
		data := msg.Data()

		// Map ID
		idVal := msg.ID()
		if s.config.IDColumn != "" {
			if val, ok := data[s.config.IDColumn]; ok {
				idVal = fmt.Sprintf("%v", val)
			}
		}

		// Map Vector
		vecVal, ok := data[s.config.VectorColumn]
		if !ok {
			return fmt.Errorf("vector column %s not found in message", s.config.VectorColumn)
		}

		floatVec := toFloat32Slice(vecVal)
		if floatVec == nil {
			return fmt.Errorf("invalid vector format for column %s", s.config.VectorColumn)
		}

		vectors = append(vectors, floatVec)

		// Handle ID types (Milvus supports Int64 or VarChar)
		var i64 int64
		if _, err := fmt.Sscanf(idVal, "%d", &i64); err == nil {
			ids = append(ids, i64)
		} else {
			isStringID = true
			stringIDs = append(stringIDs, idVal)
		}
	}

	var columns []entity.Column
	if isStringID {
		columns = append(columns, entity.NewColumnVarChar(s.config.IDColumn, stringIDs))
	} else if len(ids) > 0 {
		columns = append(columns, entity.NewColumnInt64(s.config.IDColumn, ids))
	} else {
		columns = append(columns, entity.NewColumnVarChar(s.config.IDColumn, stringIDs))
	}

	columns = append(columns, entity.NewColumnFloatVector(s.config.VectorColumn, int(len(vectors[0])), vectors))

	_, err := s.client.Insert(ctx, s.config.CollectionName, s.config.PartitionName, columns...)
	return err
}

func (s *Sink) Ping(ctx context.Context) error {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	// Check if collection exists as a ping
	_, err := s.client.HasCollection(ctx, s.config.CollectionName)
	return err
}

func (s *Sink) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func toFloat32Slice(v any) []float32 {
	switch val := v.(type) {
	case []float32:
		return val
	case []float64:
		res := make([]float32, len(val))
		for i, x := range val {
			res[i] = float32(x)
		}
		return res
	case []any:
		res := make([]float32, len(val))
		for i, x := range val {
			switch num := x.(type) {
			case float64:
				res[i] = float32(num)
			case float32:
				res[i] = num
			case int:
				res[i] = float32(num)
			case int64:
				res[i] = float32(num)
			}
		}
		return res
	}
	return nil
}
