package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hamba/avro/v2"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/xeipuuv/gojsonschema"
)

type AvroValidator struct {
	schema avro.Schema
}

func NewAvroValidator(schemaStr string) (*AvroValidator, error) {
	s, err := avro.Parse(schemaStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse avro schema: %w", err)
	}
	return &AvroValidator{schema: s}, nil
}

func (v *AvroValidator) Validate(ctx context.Context, data map[string]interface{}) error {
	// Avro validation typically requires encoding to bytes.
	// Since Hermod works with map[string]interface{}, we can attempt to marshal and then unmarshal/validate.
	// Alternatively, we can use a library that validates maps directly against the schema.
	// Hamba Avro supports marshaling maps.

	_, err := avro.Marshal(v.schema, data)
	if err != nil {
		return fmt.Errorf("avro validation failed: %w", err)
	}
	return nil
}

func (v *AvroValidator) Type() SchemaType {
	return Avro
}

type JSONSchemaValidator struct {
	schema *gojsonschema.Schema
}

func NewJSONSchemaValidator(schemaStr string) (*JSONSchemaValidator, error) {
	loader := gojsonschema.NewStringLoader(schemaStr)
	schema, err := gojsonschema.NewSchema(loader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON schema: %w", err)
	}
	return &JSONSchemaValidator{schema: schema}, nil
}

func (v *JSONSchemaValidator) Validate(ctx context.Context, data map[string]interface{}) error {
	loader := gojsonschema.NewGoLoader(data)
	result, err := v.schema.Validate(loader)
	if err != nil {
		return fmt.Errorf("JSON schema validation error: %w", err)
	}
	if !result.Valid() {
		var errs string
		for _, desc := range result.Errors() {
			errs += desc.String() + "; "
		}
		return fmt.Errorf("JSON schema validation failed: %s", errs)
	}
	return nil
}

func (v *JSONSchemaValidator) Type() SchemaType {
	return JSONSchema
}

type ProtobufValidator struct {
	descriptor *desc.MessageDescriptor
}

func NewProtobufValidator(schemaStr string) (*ProtobufValidator, error) {
	// For Protobuf, we assume the schemaStr is a .proto file content.
	// This is a bit complex as protoparse usually expects files.
	parser := protoparse.Parser{
		Accessor: protoparse.FileContentsFromMap(map[string]string{
			"schema.proto": schemaStr,
		}),
	}
	fds, err := parser.ParseFiles("schema.proto")
	if err != nil {
		return nil, fmt.Errorf("failed to parse protobuf schema: %w", err)
	}
	if len(fds) == 0 {
		return nil, fmt.Errorf("no descriptors found in protobuf schema")
	}

	// We assume the first message in the first file is the one to validate against.
	// In a real scenario, we might want to specify the message name.
	msgs := fds[0].GetMessageTypes()
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no message types found in protobuf schema")
	}

	return &ProtobufValidator{descriptor: msgs[0]}, nil
}

func (v *ProtobufValidator) Validate(ctx context.Context, data map[string]interface{}) error {
	msg := dynamic.NewMessage(v.descriptor)
	// We need to convert map to dynamic message.
	// This can be done via JSON if we want to be lazy, or manually.
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data for protobuf validation: %w", err)
	}

	if err := msg.UnmarshalJSON(jsonData); err != nil {
		return fmt.Errorf("protobuf validation failed (unmarshal error): %w", err)
	}

	return nil
}

func (v *ProtobufValidator) Type() SchemaType {
	return Protobuf
}
