package generator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestNormalizeAPIFieldEnum(t *testing.T) {
	for _, test := range []struct {
		name    string
		field   APIField
		want    []string
		wantErr bool
	}{
		{
			name:  "normalizes values",
			field: APIField{Name: "target_type", Type: "string", Enum: []string{" activity ", "carpool"}},
			want:  []string{"activity", "carpool"},
		},
		{
			name:    "rejects non string enum",
			field:   APIField{Name: "status", Type: "integer", Enum: []string{"1"}},
			wantErr: true,
		},
		{
			name:    "rejects empty value",
			field:   APIField{Name: "status", Type: "string", Enum: []string{" "}},
			wantErr: true,
		},
		{
			name:    "rejects normalized duplicate",
			field:   APIField{Name: "status", Type: "string", Enum: []string{"open", " open "}},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			field := test.field
			err := normalizeAPIField(&field)
			if (err != nil) != test.wantErr {
				t.Fatalf("normalizeAPIField() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if strings.Join(field.Enum, ",") != strings.Join(test.want, ",") {
				t.Fatalf("enum = %v, want %v", field.Enum, test.want)
			}
		})
	}
}

func TestGeneratedOpenAPIBodyFieldEnum(t *testing.T) {
	schema := validSchema()
	schema.Components = map[string]map[string]any{
		"responses": {
			"Success": map[string]any{"description": "success"},
		},
	}
	schema.Operations = []APIOperation{{
		OperationID: "CreateActivity",
		Method:      "POST",
		Path:        "/api/v1/activities",
		Permission:  "activity:create",
		Idempotency: "inherent",
		Summary:     "Create activity",
		Body: &APIObject{Fields: []APIField{{
			Name:     "target_type",
			Type:     "string",
			Required: true,
			Enum:     []string{"activity", "campus_circle_post"},
		}}},
		Responses: []APIResponse{{Status: 200, Kind: "success"}},
	}}
	root := t.TempDir()
	if _, err := Generate(
		context.Background(),
		schema,
		Options{Root: root, Source: "schemas/activity.yaml"},
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "api/modules/activity.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(data),
		"enum:\n                    - \"activity\"\n                    - \"campus_circle_post\"",
	) {
		t.Fatalf("generated module OpenAPI omitted body enum:\n%s", data)
	}

	document, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	operation := document.Paths.Find("/api/v1/activities").Post
	property := operation.RequestBody.Value.Content["application/json"].Schema.Value.Properties["target_type"]
	if property == nil || property.Value == nil || len(property.Value.Enum) != 2 ||
		property.Value.Enum[0] != "activity" ||
		property.Value.Enum[1] != "campus_circle_post" {
		t.Fatalf("request body target_type schema = %#v", property)
	}
}
