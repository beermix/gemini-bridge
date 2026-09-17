package proxy

import (
	"encoding/json"
	"regexp"
	"strings"
)

var bareKeyRegex = regexp.MustCompile(`([{,]\s*)([A-Za-z_$][a-zA-Z0-9_$-]*)(\s*:)`)

// LeakedToolCall holds the extracted name and JSON string arguments of a leaked tool call.
type LeakedToolCall struct {
	Name      string
	Arguments string
	ArgsMap   map[string]interface{}
}

func isValidToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

// TryParseLeakedToolCall checks if text starts with "call:default_api:" and parses tool name and arguments.
// Matches AntigravityManager tool-call leakage recovery behavior:
// - Must start with "call:default_api:"
// - No surrounding prose allowed
// - Arguments must be a valid JSON object or loose JSON object (bare keys)
// - Empty arguments default to "{}"
func TryParseLeakedToolCall(text string) (*LeakedToolCall, bool) {
	trimmed := strings.TrimSpace(text)

	// Strip markdown code fences if wrapped: ``` ... ``` or ` ... `
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
		if idx := strings.IndexByte(trimmed, '\n'); idx != -1 {
			firstLine := strings.TrimSpace(trimmed[:idx])
			if !strings.HasPrefix(firstLine, "call:") && !strings.HasPrefix(firstLine, "<call:") {
				trimmed = strings.TrimSpace(trimmed[idx+1:])
			}
		}
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	} else if strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`") && len(trimmed) >= 2 {
		trimmed = strings.Trim(trimmed, "`")
		trimmed = strings.TrimSpace(trimmed)
	}

	// Strip enclosing XML brackets if present: <call:default_api:...>
	if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") {
		trimmed = strings.TrimPrefix(trimmed, "<")
		trimmed = strings.TrimSuffix(trimmed, ">")
		trimmed = strings.TrimSuffix(trimmed, "/")
		trimmed = strings.TrimSpace(trimmed)
	}

	var rest string
	if strings.HasPrefix(trimmed, "call:default_api:") {
		rest = strings.TrimPrefix(trimmed, "call:default_api:")
	} else if strings.HasPrefix(trimmed, "default_api:") {
		rest = strings.TrimPrefix(trimmed, "default_api:")
	} else if strings.HasPrefix(trimmed, "call:") {
		rest = strings.TrimPrefix(trimmed, "call:")
	} else {
		return nil, false
	}
	delimIdx := strings.IndexAny(rest, "({[")
	var toolName, argPart string
	if delimIdx == -1 {
		toolName = strings.TrimSpace(rest)
		argPart = ""
	} else {
		toolName = strings.TrimSpace(rest[:delimIdx])
		argPart = strings.TrimSpace(rest[delimIdx:])
	}

	if !isValidToolName(toolName) {
		return nil, false
	}

	// Strip surrounding parentheses if any: e.g. ({...})
	if strings.HasPrefix(argPart, "(") && strings.HasSuffix(argPart, ")") {
		argPart = strings.TrimSpace(argPart[1 : len(argPart)-1])
	}

	if argPart == "" {
		return &LeakedToolCall{
			Name:      toolName,
			Arguments: "{}",
			ArgsMap:   make(map[string]interface{}),
		}, true
	}

	// Must start with { to be a valid object (reject arrays [..] or literals)
	if !strings.HasPrefix(argPart, "{") {
		return nil, false
	}

	// Try strict JSON object first
	var parsed interface{}
	if err := json.Unmarshal([]byte(argPart), &parsed); err == nil {
		if obj, isObj := parsed.(map[string]interface{}); isObj {
			return &LeakedToolCall{
				Name:      toolName,
				Arguments: argPart,
				ArgsMap:   obj,
			}, true
		}
		return nil, false
	}

	// If not strict JSON, attempt bare-key normalization if enclosed in braces
	if strings.HasPrefix(argPart, "{") && strings.HasSuffix(argPart, "}") {
		quoted := bareKeyRegex.ReplaceAllString(argPart, `$1"$2"$3`)
		if err := json.Unmarshal([]byte(quoted), &parsed); err == nil {
			if obj, isObj := parsed.(map[string]interface{}); isObj {
				return &LeakedToolCall{
					Name:      toolName,
					Arguments: quoted,
					ArgsMap:   obj,
				}, true
			}
		}
	}

	return nil, false
}
