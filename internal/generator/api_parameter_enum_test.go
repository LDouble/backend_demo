package generator

import (
	"strings"
	"testing"
)

func TestNormalizeAPIParameterEnum(t *testing.T) {
	for _, test := range []struct {
		name      string
		parameter APIParameter
		want      []string
		wantErr   bool
	}{
		{
			name:      "normalizes string values",
			parameter: APIParameter{Name: "status", Type: "string", Enum: []string{" open ", "closed"}},
			want:      []string{"open", "closed"},
		},
		{
			name:      "rejects non string enum",
			parameter: APIParameter{Name: "status", Type: "integer", Enum: []string{"1"}},
			wantErr:   true,
		},
		{
			name:      "rejects empty enum value",
			parameter: APIParameter{Name: "status", Type: "string", Enum: []string{" "}},
			wantErr:   true,
		},
		{
			name:      "rejects duplicate enum value after normalization",
			parameter: APIParameter{Name: "status", Type: "string", Enum: []string{"open", " open "}},
			wantErr:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameter := test.parameter
			err := normalizeAPIParameter(&parameter, "query")
			if (err != nil) != test.wantErr {
				t.Fatalf("normalizeAPIParameter() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if strings.Join(parameter.Enum, ",") != strings.Join(test.want, ",") {
				t.Fatalf("enum = %v, want %v", parameter.Enum, test.want)
			}
		})
	}
}

func TestOpenAPITemplateRendersParameterEnum(t *testing.T) {
	content, err := render("openapi.yaml.tmpl", templateData{
		Schema: Schema{
			Module: "sample",
			Operations: []APIOperation{{
				OperationID: "ListSamples",
				Method:      "GET",
				Path:        "/api/v1/samples",
				Permission:  "sample:read",
				Summary:     "List samples",
				Query: []APIParameter{{
					Name: "status",
					Type: "string",
					Enum: []string{"open", "closed"},
				}},
				Responses: []APIResponse{{Status: 200, Kind: "success"}},
			}},
		},
		Header: "generated",
		Source: "schemas/sample.yaml",
		OperationRoutes: []operationRoute{{
			Path: "/api/v1/samples",
			Methods: []APIOperation{{
				OperationID: "ListSamples",
				Method:      "GET",
				Path:        "/api/v1/samples",
				Permission:  "sample:read",
				Summary:     "List samples",
				Query: []APIParameter{{
					Name: "status",
					Type: "string",
					Enum: []string{"open", "closed"},
				}},
				Responses: []APIResponse{{Status: 200, Kind: "success"}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := string(content)
	if !strings.Contains(output, "enum:\n              - \"open\"\n              - \"closed\"") {
		t.Fatalf("rendered OpenAPI does not contain enum:\n%s", output)
	}
}
