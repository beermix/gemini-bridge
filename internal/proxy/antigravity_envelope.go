package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"time"
)

func newUUIDv4() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40 // Version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// DeriveAntigravitySessionID derives a deterministic signed decimal session ID from the first user prompt,
// or generates a random bounded int63 session ID formatted with a "-" prefix.
func DeriveAntigravitySessionID(firstPrompt string) string {
	trimmed := strings.TrimSpace(firstPrompt)
	if trimmed != "" {
		h := sha256.Sum256([]byte(trimmed))
		val := binary.BigEndian.Uint64(h[:8]) & 0x7FFFFFFFFFFFFFFF
		return fmt.Sprintf("-%d", val)
	}

	n, err := rand.Int(rand.Reader, big.NewInt(9000000000000000000))
	if err != nil {
		return fmt.Sprintf("-%d", time.Now().UnixNano()&0x7FFFFFFFFFFFFFFF)
	}
	return fmt.Sprintf("-%d", n.Int64())
}

// GenerateAntigravityRequestEnvelope creates Antigravity-compatible RequestID, SessionID, and Labels
// mimicking official Google Antigravity Hub requests.
func GenerateAntigravityRequestEnvelope(model string, firstPrompt string) (requestID string, sessionID string, labels map[string]string) {
	agentID := newUUIDv4()
	trajectoryID := newUUIDv4()
	step := 1

	requestID = fmt.Sprintf("agent/%s/%d/%s/%d", agentID, time.Now().UnixMilli(), trajectoryID, step)
	sessionID = DeriveAntigravitySessionID(firstPrompt)

	isClaude := strings.Contains(strings.ToLower(model), "claude")
	usageLabel := "false"
	if isClaude {
		usageLabel = "true"
	}

	labels = map[string]string{
		"trajectory_id":            trajectoryID,
		"last_step_index":          "0",
		"used_claude":              usageLabel,
		"used_claude_conservative": usageLabel,
	}

	return requestID, sessionID, labels
}
