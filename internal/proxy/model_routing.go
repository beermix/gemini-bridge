package proxy

import (
	"strings"
)

// ResolveModel maps incoming model aliases from OpenAI or Anthropic protocols
// to internal Google Cloud Code model identifiers.
func ResolveModel(requestedModel string) string {
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		return "gemini-3-flash"
	}
	lower := strings.ToLower(model)

	// Hermes default / agy alias (maps to Gemini Flash)
	if lower == "agy" || lower == "default" {
		return "gemini-3-flash"
	}

	// GPT-OSS variants
	if strings.HasPrefix(lower, "gpt-oss") {
		return "claude-opus-4-6-thinking"
	}

	// Claude Sonnet variants
	if strings.HasPrefix(lower, "claude-3-7-sonnet") ||
		strings.HasPrefix(lower, "claude-3-5-sonnet") ||
		strings.HasPrefix(lower, "claude-sonnet-4-6") ||
		strings.HasPrefix(lower, "claude-sonnet-4-5") {
		return "claude-sonnet-4-6-thinking"
	}

	// Claude Opus variants
	if strings.HasPrefix(lower, "claude-3-opus") ||
		strings.HasPrefix(lower, "claude-opus-4-6") ||
		strings.HasPrefix(lower, "claude-opus-4-5") {
		return "claude-opus-4-6-thinking"
	}

	// Claude Haiku variants
	if strings.HasPrefix(lower, "claude-3-haiku") ||
		strings.HasPrefix(lower, "claude-haiku-4") {
		return "claude-sonnet-4-6-thinking"
	}

	// OpenAI GPT variants
	if strings.HasPrefix(lower, "gpt-4o") ||
		strings.HasPrefix(lower, "gpt-4") ||
		strings.HasPrefix(lower, "gpt-3.5-turbo") {
		return "gemini-3-flash"
	}

	// Gemini Pro variants
	if strings.HasPrefix(lower, "gemini-2.5-pro") ||
		strings.HasPrefix(lower, "gemini-3.1-pro") {
		return "gemini-3.1-pro-high"
	}

	// Gemini Flash variants (including Hermes 3.6, 3.7, 3.8 flash)
	if strings.Contains(lower, "low") || strings.Contains(lower, "lite") {
		return "gemini-3.5-flash-lite"
	}
	if strings.Contains(lower, "high") && strings.Contains(lower, "flash") {
		return "gemini-3.5-flash-high"
	}
	if strings.HasPrefix(lower, "gemini-3.8-flash") ||
		strings.HasPrefix(lower, "gemini-3.7-flash") ||
		strings.HasPrefix(lower, "gemini-3.6-flash") ||
		strings.HasPrefix(lower, "gemini-3.5-flash") ||
		strings.HasPrefix(lower, "gemini-3-flash") ||
		strings.HasPrefix(lower, "gemini-2.5-flash") {
		return "gemini-3-flash"
	}

	// Passthrough for exact internal names (e.g. "gemini-pro-agent", custom models)
	return model
}
