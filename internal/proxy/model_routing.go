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

	// Canonical rolling aliases for Hermes Agent
	if lower == "gemini-flash-latest" || lower == "gemini-flash-lite-latest" {
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
		return "claude-sonnet-4-6"
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
		return "claude-sonnet-4-6"
	}

	// OpenAI ChatGPT / GPT compatibility fallback (routes gracefully to Gemini Flash)
	if strings.HasPrefix(lower, "chatgpt") ||
		strings.HasPrefix(lower, "gpt-4") ||
		strings.HasPrefix(lower, "gpt-3.5") ||
		strings.HasPrefix(lower, "o1") ||
		strings.HasPrefix(lower, "o3") {
		return "gemini-3-flash"
	}

	// Gemini Pro variants
	if strings.HasPrefix(lower, "gemini-2.5-pro") ||
		strings.HasPrefix(lower, "gemini-3.1-pro") {
		return "gemini-3.1-pro-low"
	}

	// All Gemini Flash variants (3.8, 3.7, 3.6, 3.5, 3.0, 2.5, 2.0, low, extra-low, lite, high, medium, etc.)
	// route to active gemini-3-flash (as Google decommissioned 3.5-flash-* engines)
	if strings.Contains(lower, "flash") || strings.Contains(lower, "lite") {
		return "gemini-3-flash"
	}

	// Passthrough for exact internal names (e.g. "gemini-pro-agent", custom models)
	return model
}

// SanitizePromptText strips or renames prompt tokens/tags known to trigger
// Google Cloud Code Assist instruction-hierarchy / prompt security filters
// (such as <system-conventions> which causes upstream throttling to ~20 tps
// or false HTTP 429 RESOURCE_EXHAUSTED errors; see can1357/oh-my-pi#11883 and
// Draculabo/AntigravityManager#313).
func SanitizePromptText(text string) string {
	if !strings.Contains(text, "system-conventions") {
		return text
	}
	text = strings.ReplaceAll(text, "<system-conventions>", "<conventions>")
	text = strings.ReplaceAll(text, "</system-conventions>", "</conventions>")
	return text
}
