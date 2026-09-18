package proxy

import (
	"strings"
	"testing"
)

func TestDeriveAntigravitySessionID(t *testing.T) {
	// Deterministic hash check
	prompt := "You are a helpful assistant"
	s1 := DeriveAntigravitySessionID(prompt)
	s2 := DeriveAntigravitySessionID(prompt)

	if s1 != s2 {
		t.Errorf("expected deterministic session ID for identical prompt, got %q vs %q", s1, s2)
	}

	if !strings.HasPrefix(s1, "-") {
		t.Errorf("expected session ID to start with '-', got %q", s1)
	}

	// Empty prompt fallback
	sEmpty := DeriveAntigravitySessionID("")
	if !strings.HasPrefix(sEmpty, "-") {
		t.Errorf("expected empty prompt session ID to start with '-', got %q", sEmpty)
	}
}

func TestGenerateAntigravityRequestEnvelope(t *testing.T) {
	reqID, sessID, labels := GenerateAntigravityRequestEnvelope("gemini-3-flash", "Hello there")

	if !strings.HasPrefix(reqID, "agent/") {
		t.Errorf("expected reqID to start with 'agent/', got %q", reqID)
	}

	parts := strings.Split(reqID, "/")
	if len(parts) != 5 {
		t.Errorf("expected 5 segments in reqID 'agent/<agentId>/<ts>/<trajectoryId>/<step>', got %d in %q", len(parts), reqID)
	}

	if !strings.HasPrefix(sessID, "-") {
		t.Errorf("expected sessID to start with '-', got %q", sessID)
	}

	if labels["trajectory_id"] != parts[3] {
		t.Errorf("expected labels.trajectory_id %q to match reqID segment %q", labels["trajectory_id"], parts[3])
	}

	if labels["last_step_index"] != "0" {
		t.Errorf("expected last_step_index '0', got %q", labels["last_step_index"])
	}

	if labels["used_claude"] != "false" {
		t.Errorf("expected used_claude 'false' for gemini model, got %q", labels["used_claude"])
	}

	// Test Claude model label
	_, _, claudeLabels := GenerateAntigravityRequestEnvelope("claude-sonnet-4-6", "Write code")
	if claudeLabels["used_claude"] != "true" || claudeLabels["used_claude_conservative"] != "true" {
		t.Errorf("expected used_claude 'true' for claude model, got %+v", claudeLabels)
	}
}
