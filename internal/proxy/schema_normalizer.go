package proxy

import (
	"strings"
)

// unsupportedGoogleSchemaFields contains JSON Schema keywords unsupported by
// Google Cloud Code Assist / Antigravity internal Gemini API endpoints.
var unsupportedGoogleSchemaFields = map[string]bool{
	"$schema":               true,
	"$ref":                  true,
	"$defs":                 true,
	"$dynamicRef":           true,
	"$dynamicAnchor":        true,
	"additionalProperties":  true,
	"unevaluatedProperties": true,
	"unevaluatedItems":      true,
	"patternProperties":     true,
	"minProperties":         true,
	"maxProperties":         true,
	"minLength":             true,
	"maxLength":             true,
	"minimum":               true,
	"maximum":               true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"multipleOf":            true,
	"minItems":              true,
	"maxItems":              true,
	"uniqueItems":           true,
	"pattern":               true,
	"format":                true,
	"dependencies":          true,
	"dependentSchemas":      true,
	"dependentRequired":     true,
	"deprecated":            true,
	"readOnly":              true,
	"writeOnly":             true,
	"$comment":              true,
	"title":                 true,
}

// NormalizeSchemaForGoogle cleanses and normalizes a JSON Schema for Google Cloud Code Assist.
// It removes unsupported schema keywords, handles type arrays (converting ["type", "null"] to
// scalar type and nullable: true), converts "const" to "enum", and ensures that object schemas
// have a valid properties map.
func NormalizeSchemaForGoogle(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}

	result := make(map[string]interface{})

	// Copy and filter unsupported fields
	for k, v := range schema {
		if unsupportedGoogleSchemaFields[k] {
			continue
		}
		result[k] = v
	}

	// Handle type array: ["string", "null"] -> type: "string", nullable: true
	if rawType, ok := result["type"]; ok {
		var typeList []string
		switch t := rawType.(type) {
		case []string:
			typeList = t
		case []interface{}:
			for _, item := range t {
				if s, ok := item.(string); ok {
					typeList = append(typeList, s)
				}
			}
		}

		if len(typeList) > 0 {
			var scalarType string
			hasNull := false
			for _, item := range typeList {
				if strings.ToLower(item) == "null" {
					hasNull = true
				} else if scalarType == "" {
					scalarType = item
				}
			}

			if scalarType != "" {
				result["type"] = scalarType
			}
			if hasNull {
				result["nullable"] = true
			}
		}
	}

	// Convert "const" to "enum"
	if constVal, ok := result["const"]; ok {
		if _, hasEnum := result["enum"]; !hasEnum {
			result["enum"] = []interface{}{constVal}
		}
		delete(result, "const")
	}

	// Ensure type "object" has a valid properties map
	if typeStr, ok := result["type"].(string); ok && typeStr == "object" {
		if _, hasProps := result["properties"]; !hasProps {
			result["properties"] = make(map[string]interface{})
		}
	}

	// Recursively normalize "properties"
	if rawProps, ok := result["properties"]; ok {
		if propsMap, ok := rawProps.(map[string]interface{}); ok {
			normProps := make(map[string]interface{}, len(propsMap))
			for propName, propVal := range propsMap {
				if subSchema, ok := propVal.(map[string]interface{}); ok {
					normProps[propName] = NormalizeSchemaForGoogle(subSchema)
				} else {
					normProps[propName] = propVal
				}
			}
			result["properties"] = normProps
		}
	}

	// Recursively normalize "items"
	if rawItems, ok := result["items"]; ok {
		switch itemsVal := rawItems.(type) {
		case map[string]interface{}:
			result["items"] = NormalizeSchemaForGoogle(itemsVal)
		case []interface{}:
			var normList []interface{}
			for _, elem := range itemsVal {
				if subSchema, ok := elem.(map[string]interface{}); ok {
					normList = append(normList, NormalizeSchemaForGoogle(subSchema))
				} else {
					normList = append(normList, elem)
				}
			}
			result["items"] = normList
		}
	}

	// Recursively normalize combinators: anyOf, allOf, oneOf
	for _, combinator := range []string{"anyOf", "allOf", "oneOf"} {
		if rawList, ok := result[combinator]; ok {
			if list, ok := rawList.([]interface{}); ok {
				var normList []interface{}
				for _, elem := range list {
					if subSchema, ok := elem.(map[string]interface{}); ok {
						normList = append(normList, NormalizeSchemaForGoogle(subSchema))
					} else {
						normList = append(normList, elem)
					}
				}
				result[combinator] = normList
			}
		}
	}

	return result
}
