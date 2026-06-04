package grpcsource

import (
	"context"
	"fmt"
	"sync"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/user/hermod"
)

// GenericProtoSource wraps another source and decodes its payload using dynamic Protobuf descriptors.
type GenericProtoSource struct {
	ProtoFile   string
	MessageName string
	Source      hermod.Source

	mu         sync.RWMutex
	descriptor *desc.MessageDescriptor
}

// NewGenericProtoSource creates a new GenericProtoSource.
func NewGenericProtoSource(protoFile, messageName string, source hermod.Source) (*GenericProtoSource, error) {
	s := &GenericProtoSource{
		ProtoFile:   protoFile,
		MessageName: messageName,
		Source:      source,
	}

	if err := s.reloadDescriptor(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *GenericProtoSource) reloadDescriptor() error {
	parser := protoparse.Parser{}
	fds, err := parser.ParseFiles(s.ProtoFile)
	if err != nil {
		return fmt.Errorf("failed to parse proto file %s: %w", s.ProtoFile, err)
	}

	var msgDesc *desc.MessageDescriptor
	for _, fd := range fds {
		msgDesc = fd.FindMessage(s.MessageName)
		if msgDesc != nil {
			break
		}
	}

	if msgDesc == nil {
		return fmt.Errorf("message %s not found in %s", s.MessageName, s.ProtoFile)
	}

	s.mu.Lock()
	s.descriptor = msgDesc
	s.mu.Unlock()

	return nil
}

func (s *GenericProtoSource) Read(ctx context.Context) (hermod.Message, error) {
	msg, err := s.Source.Read(ctx)
	if err != nil {
		return nil, err
	}

	payload := msg.Payload()
	if len(payload) == 0 {
		return msg, nil
	}

	s.mu.RLock()
	d := s.descriptor
	s.mu.RUnlock()

	dynMsg := dynamic.NewMessage(d)
	if err := dynMsg.Unmarshal(payload); err != nil {
		return msg, fmt.Errorf("failed to unmarshal dynamic proto: %w", err)
	}

	// Convert dynamic message to data map
	data := s.convertToMap(dynMsg)
	for k, v := range data {
		msg.SetData(k, v)
	}

	return msg, nil
}

func (s *GenericProtoSource) convertToMap(dynMsg *dynamic.Message) map[string]any {
	res := make(map[string]any)
	for _, fd := range dynMsg.GetMessageDescriptor().GetFields() {
		if !dynMsg.HasField(fd) {
			continue
		}
		val := dynMsg.GetField(fd)
		res[fd.GetName()] = s.convertValue(val)
	}
	return res
}

func (s *GenericProtoSource) convertValue(val any) any {
	switch v := val.(type) {
	case *dynamic.Message:
		return s.convertToMap(v)
	case []*dynamic.Message:
		res := make([]map[string]any, len(v))
		for i, m := range v {
			res[i] = s.convertToMap(m)
		}
		return res
	case []any:
		res := make([]any, len(v))
		for i, item := range v {
			res[i] = s.convertValue(item)
		}
		return res
	default:
		return v
	}
}

func (s *GenericProtoSource) Ack(ctx context.Context, msg hermod.Message) error {
	return s.Source.Ack(ctx, msg)
}

func (s *GenericProtoSource) Ping(ctx context.Context) error {
	return s.Source.Ping(ctx)
}

func (s *GenericProtoSource) Close() error {
	return s.Source.Close()
}
