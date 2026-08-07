package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// SchemaDialect is the JSON Schema dialect the generated document declares.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// Schema is the machine-readable description of this build's protocol surface.
//
// It is generated from the same factory tables that decoding and capability
// negotiation read, so a payload shape cannot drift from what is published: there
// is no second place to update. A consumer outside this repository — the VS Code
// extension is the first one coming — needs the shapes, not just the kind names
// that `OperationKinds` and `EventKinds` already give it.
type Schema struct {
	Dialect string `json:"$schema"`
	Title   string `json:"title"`
	// Version is the protocol version these shapes belong to. A consumer that
	// negotiated a different version must not assume these shapes apply.
	Version int `json:"protocol_version"`
	// Operations and Events map a kind to the schema of its payload or data.
	// Envelope carries the shapes that wrap them.
	Envelope   map[string]*TypeSchema `json:"envelope"`
	Operations map[string]*TypeSchema `json:"operations"`
	Events     map[string]*TypeSchema `json:"events"`
}

// TypeSchema is the subset of JSON Schema the protocol needs. It is deliberately
// small: the protocol is structs of scalars, slices and nested structs, and a
// generator that only emits what those need cannot emit something misleading.
type TypeSchema struct {
	Type string `json:"type,omitempty"`
	// Format carries "date-time" for timestamps, which is the one semantic a
	// consumer cannot infer from the Go type being a string.
	Format               string                 `json:"format,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Properties           map[string]*TypeSchema `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	Items                *TypeSchema            `json:"items,omitempty"`
	AdditionalProperties *bool                  `json:"additionalProperties,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
}

// GenerateSchema builds the document. It is pure: the same build always produces
// the same bytes, which is what makes a committed copy a drift check.
func GenerateSchema() *Schema {
	schema := &Schema{
		Dialect: SchemaDialect, Title: "codehelper runtime protocol", Version: Version,
		Envelope:   map[string]*TypeSchema{},
		Operations: map[string]*TypeSchema{},
		Events:     map[string]*TypeSchema{},
	}
	for _, entry := range operationPayloads {
		schema.Operations[string(entry.kind)] = schemaOf(reflect.TypeOf(entry.newPayload()))
	}
	for _, entry := range eventData {
		schema.Events[string(entry.kind)] = schemaOf(reflect.TypeOf(entry.newData()))
	}
	schema.Envelope["operation"] = envelopeSchema(
		"operation", "kind", operationKindStrings(), "payload",
		"The operation envelope. payload is the schema in operations[kind].",
	)
	schema.Envelope["event"] = eventEnvelopeSchema()
	schema.Envelope["problem"] = schemaOf(reflect.TypeOf(&Problem{}))
	schema.Envelope["readiness"] = schemaOf(reflect.TypeOf(&Readiness{}))
	schema.Envelope["session_profile_snapshot"] = schemaOf(
		reflect.TypeOf(&SessionProfileSnapshot{}),
	)
	schema.Envelope["session_profile_patch"] = schemaOf(reflect.TypeOf(&SessionProfilePatch{}))
	schema.Envelope["session_profile_update"] = schemaOf(
		reflect.TypeOf(&SessionProfileUpdateResult{}),
	)
	return schema
}

// MarshalSchema renders the document as indented JSON with a trailing newline,
// which is the form the committed copy takes.
func MarshalSchema() ([]byte, error) {
	data, err := json.MarshalIndent(GenerateSchema(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func eventEnvelopeSchema() *TypeSchema {
	schema := envelopeSchema(
		"event", "kind", eventKindStrings(), "data",
		"The event envelope. data is the schema in events[kind].",
	)
	for _, name := range []string{
		"operation_id", "thread_id", "turn_id", "item_id",
	} {
		schema.Properties[name] = &TypeSchema{Type: "string"}
		schema.Required = append(schema.Required, name)
	}
	schema.Properties["sequence"] = &TypeSchema{Type: "integer"}
	schema.Required = append(schema.Required, "sequence")
	sort.Strings(schema.Required)
	return schema
}

func envelopeSchema(name, kindField string, kinds []string, bodyField, description string) *TypeSchema {
	deny := false
	return &TypeSchema{
		Type: "object", Description: description,
		Properties: map[string]*TypeSchema{
			"version":    {Type: "integer"},
			"id":         {Type: "string"},
			kindField:    {Type: "string", Enum: kinds},
			"created_at": {Type: "string", Format: "date-time"},
			bodyField: {
				Type:        "object",
				Description: fmt.Sprintf("see %ss[%s]", name, kindField),
			},
		},
		Required:             []string{"version", "id", kindField, "created_at", bodyField},
		AdditionalProperties: &deny,
	}
}

func operationKindStrings() []string {
	kinds := OperationKinds()
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, string(kind))
	}
	return out
}

func eventKindStrings() []string {
	kinds := EventKinds()
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		out = append(out, string(kind))
	}
	return out
}

var timeType = reflect.TypeOf(time.Time{})
var rawMessageType = reflect.TypeOf(json.RawMessage{})
var editorContextKindType = reflect.TypeOf(EditorContextKind(""))
var editorContextSourceType = reflect.TypeOf(EditorContextSource(""))

func schemaOf(goType reflect.Type) *TypeSchema {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	switch {
	case goType == timeType:
		return &TypeSchema{Type: "string", Format: "date-time"}
	case goType == rawMessageType:
		// Raw JSON is exactly that: a tool's arguments are the tool's business.
		return &TypeSchema{Description: "arbitrary JSON"}
	case goType == editorContextKindType:
		return &TypeSchema{Type: "string", Enum: []string{
			string(EditorContextFile), string(EditorContextSelection),
			string(EditorContextSymbol), string(EditorContextDiagnostics),
		}}
	case goType == editorContextSourceType:
		return &TypeSchema{Type: "string", Enum: []string{
			string(EditorContextSourceComposer),
			string(EditorContextSourceSelectionCommand),
			string(EditorContextSourceCodeAction),
		}}
	}
	switch goType.Kind() {
	case reflect.String:
		return &TypeSchema{Type: "string"}
	case reflect.Bool:
		return &TypeSchema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &TypeSchema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &TypeSchema{Type: "number"}
	case reflect.Slice, reflect.Array:
		if goType.Elem().Kind() == reflect.Uint8 {
			// A byte slice marshals as base64, not as an array of numbers.
			return &TypeSchema{Type: "string", Description: "base64"}
		}
		return &TypeSchema{Type: "array", Items: schemaOf(goType.Elem())}
	case reflect.Map:
		return &TypeSchema{Type: "object", Description: "map of " + goType.Elem().String()}
	case reflect.Struct:
		return structSchema(goType)
	case reflect.Interface:
		return &TypeSchema{Description: "arbitrary JSON"}
	default:
		return &TypeSchema{Description: "unsupported Go kind " + goType.Kind().String()}
	}
}

func structSchema(goType reflect.Type) *TypeSchema {
	// Decoding rejects unknown fields, so the schema says so rather than leaving a
	// consumer to discover it from an error.
	deny := false
	schema := &TypeSchema{
		Type: "object", Properties: map[string]*TypeSchema{}, AdditionalProperties: &deny,
	}
	var required []string
	for index := range goType.NumField() {
		field := goType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, omitEmpty, skip := fieldName(field)
		if skip {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := structSchema(field.Type)
			for key, value := range embedded.Properties {
				schema.Properties[key] = value
			}
			required = append(required, embedded.Required...)
			continue
		}
		schema.Properties[name] = schemaOf(field.Type)
		if !omitEmpty && field.Type.Kind() != reflect.Pointer {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	schema.Required = required
	return schema
}

// fieldName reads the json tag. skip is true for fields the wire never carries.
func fieldName(field reflect.StructField) (name string, omitEmpty, skip bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	name = field.Name
	if tag != "" {
		parts := splitTag(tag)
		if parts[0] != "" {
			name = parts[0]
		}
		for _, option := range parts[1:] {
			if option == "omitempty" {
				omitEmpty = true
			}
		}
	}
	if field.Anonymous && tag == "" {
		return "", omitEmpty, false
	}
	return name, omitEmpty, false
}

func splitTag(tag string) []string {
	parts := []string{}
	current := ""
	for _, character := range tag {
		if character == ',' {
			parts = append(parts, current)
			current = ""
			continue
		}
		current += string(character)
	}
	return append(parts, current)
}
