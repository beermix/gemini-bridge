package proxy

import (
	"reflect"
	"testing"
)

func TestNormalizeSchemaForGoogle(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name: "strips unsupported top-level and nested keywords",
			input: map[string]interface{}{
				"$schema":              "http://json-schema.org/draft-07/schema#",
				"title":                "TestFunctionParams",
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":      "string",
						"pattern":   "^[a-zA-Z0-9]+$",
						"minLength": 1,
						"maxLength": 100,
						"format":    "email",
					},
					"count": map[string]interface{}{
						"type":             "integer",
						"minimum":          1,
						"maximum":          10,
						"exclusiveMinimum": 0,
					},
				},
				"required": []interface{}{"query"},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type": "string",
					},
					"count": map[string]interface{}{
						"type": "integer",
					},
				},
				"required": []interface{}{"query"},
			},
		},
		{
			name: "converts type array with null to scalar type and nullable true",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filter": map[string]interface{}{
						"type": []interface{}{"string", "null"},
					},
					"limit": map[string]interface{}{
						"type": []string{"integer", "null"},
					},
					"tag": map[string]interface{}{
						"type": []interface{}{"null", "string"},
					},
					"single": map[string]interface{}{
						"type": []interface{}{"boolean"},
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filter": map[string]interface{}{
						"type":     "string",
						"nullable": true,
					},
					"limit": map[string]interface{}{
						"type":     "integer",
						"nullable": true,
					},
					"tag": map[string]interface{}{
						"type":     "string",
						"nullable": true,
					},
					"single": map[string]interface{}{
						"type": "boolean",
					},
				},
			},
		},
		{
			name: "converts const to enum",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":  "string",
						"const": "delete",
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{"delete"},
					},
				},
			},
		},
		{
			name: "ensures properties map exists for type object",
			input: map[string]interface{}{
				"type": "object",
			},
			expected: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			name: "normalizes items in array and anyOf",
			input: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"items_list": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":      "string",
									"minLength": 5,
								},
							},
						},
					},
					"nested_choice": map[string]interface{}{
						"anyOf": []interface{}{
							map[string]interface{}{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]interface{}{
									"optionA": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
			expected: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"items_list": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
					"nested_choice": map[string]interface{}{
						"anyOf": []interface{}{
							map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"optionA": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := NormalizeSchemaForGoogle(tt.input)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("NormalizeSchemaForGoogle() mismatch:\nExpected: %#v\nActual:   %#v", tt.expected, actual)
			}
		})
	}
}
