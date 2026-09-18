package proxy

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	// openerLineRe matches a standalone reasoning-fence opener: <= 3 leading spaces, >= 3 backticks,
	// "thinking" or "reasoning", optional trailing spaces/carriage return.
	openerLineRe = regexp.MustCompile(`(?i)^ {0,3}` + "`" + `{3,}(?:thinking|reasoning)[ \t]*\r?$`)
)

// couldBeOpenerPrefix checks if a partial line could still grow into a standalone reasoning-fence opener.
func couldBeOpenerPrefix(line string) bool {
	s := strings.TrimSuffix(line, "\r")
	trimmed := strings.TrimLeft(s, " ")
	leadingSpaces := len(s) - len(trimmed)
	if leadingSpaces > 3 {
		return false
	}
	ticks := 0
	for _, ch := range trimmed {
		if ch == '`' {
			ticks++
		} else {
			break
		}
	}
	rest := trimmed[ticks:]
	if rest == "" {
		return true // Still consuming backticks
	}
	if ticks < 3 {
		return false
	}
	word := strings.ToLower(strings.TrimRight(rest, " \t"))
	return strings.HasPrefix("thinking", word) || strings.HasPrefix("reasoning", word)
}

// ThinkingFenceStripper is a stateful line-oriented filter that removes rogue ```thinking / ```reasoning
// opener lines leaked inside structured thinking parts.
type ThinkingFenceStripper struct {
	carry       string
	passthrough bool
}

// Push processes a streaming delta and returns clean thinking text.
func (s *ThinkingFenceStripper) Push(chunk string) string {
	var out strings.Builder
	for _, ch := range chunk {
		if s.passthrough {
			out.WriteRune(ch)
			if ch == '\n' {
				s.passthrough = false
			}
			continue
		}
		if ch == '\n' {
			if !openerLineRe.MatchString(s.carry) {
				out.WriteString(s.carry)
				out.WriteByte('\n')
			}
			s.carry = ""
			continue
		}
		s.carry += string(ch)
		if !couldBeOpenerPrefix(s.carry) {
			out.WriteString(s.carry)
			s.carry = ""
			s.passthrough = true
		}
	}
	return out.String()
}

// Flush returns any pending partial line that wasn't an opener line.
func (s *ThinkingFenceStripper) Flush() string {
	carry := s.carry
	s.carry = ""
	s.passthrough = false
	if openerLineRe.MatchString(carry) {
		return ""
	}
	return carry
}

// StripThinkingFenceDelimiters removes standalone ```thinking or ```reasoning lines in non-streaming text.
func StripThinkingFenceDelimiters(text string) string {
	if !strings.Contains(text, "```") {
		return text
	}
	stripper := &ThinkingFenceStripper{}
	res := stripper.Push(text) + stripper.Flush()
	return res
}

var planningLeakKeys = []string{"thought", "call", "_i", "command", "paths", "path"}

// isPlanningLeakPrefix checks whether the accumulated text starts with a candidate JSON planning leak.
func isPlanningLeakPrefix(text string) bool {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	afterBrace := strings.TrimLeft(trimmed[1:], " \t\r\n")
	if afterBrace == "" {
		return len(trimmed) <= 100
	}
	if !strings.HasPrefix(afterBrace, `"`) {
		return false
	}
	nextQuote := strings.Index(afterBrace[1:], `"`)
	if nextQuote == -1 {
		keyPrefix := afterBrace[1:]
		for _, k := range planningLeakKeys {
			if strings.HasPrefix(k, keyPrefix) && len(trimmed) <= 100 {
				return true
			}
		}
		return false
	}
	key := afterBrace[1 : nextQuote+1]
	matched := false
	for _, k := range planningLeakKeys {
		if key == k {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	afterKey := strings.TrimLeft(afterBrace[nextQuote+2:], " \t\r\n")
	if afterKey == "" {
		return len(trimmed) <= 100
	}
	return strings.HasPrefix(afterKey, ":")
}

// splitLeadingJsonObject finds the first balanced JSON object {...} at the start of text.
func splitLeadingJsonObject(text string) (jsonText, rest string, ok bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(trimmed, "{") {
		return "", "", false
	}

	depth := 0
	inString := false
	escaped := false

	for i, ch := range trimmed {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' {
			depth++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return trimmed[:i+1], trimmed[i+1:], true
			}
		}
	}
	return "", "", false
}

// isPlanningLeakObject checks if parsed JSON contains typical planning-leak signatures.
func isPlanningLeakObject(obj map[string]interface{}) bool {
	if obj == nil {
		return false
	}
	if _, ok := obj["thought"]; ok {
		return true
	}
	if _, ok := obj["call"]; ok {
		return true
	}
	if _, ok := obj["_i"]; ok {
		return true
	}
	if _, ok := obj["paths"]; ok {
		return true
	}
	if _, ok := obj["command"]; ok {
		return true
	}
	_, hasPath := obj["path"]
	_, hasContent := obj["content"]
	return hasPath && hasContent
}

// PlanningBuffer intercepts leaked raw JSON planning blocks (e.g. `{"thought": "..."}`)
// at the start of an assistant stream.
type PlanningBuffer struct {
	buffer           string
	decisionMade     bool
	isIntercepted    bool
	strippedPlanning bool
}

// Consume processes streaming text delta.
// It returns the clean text to emit and whether a planning leak was stripped.
func (p *PlanningBuffer) Consume(chunk string, isFinal bool) (cleanText string, strippedLeak bool) {
	if p.decisionMade {
		return chunk, false
	}

	p.buffer += chunk
	if !isPlanningLeakPrefix(p.buffer) {
		p.decisionMade = true
		res := p.buffer
		p.buffer = ""
		return res, false
	}

	jsonText, rest, ok := splitLeadingJsonObject(p.buffer)
	if !ok {
		if isFinal {
			p.decisionMade = true
			trimmed := strings.TrimSpace(p.buffer)
			if strings.Contains(trimmed, `"thought"`) || strings.Contains(trimmed, `"_i"`) || strings.Contains(trimmed, `"paths"`) {
				p.strippedPlanning = true
				p.buffer = ""
				return "", true
			}
			res := p.buffer
			p.buffer = ""
			return res, false
		}
		// Still buffering leading JSON object
		return "", false
	}

	p.decisionMade = true
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err == nil && isPlanningLeakObject(parsed) {
		p.strippedPlanning = true
		p.isIntercepted = true
		p.buffer = ""
		return strings.TrimLeft(rest, " \r\n"), true
	}

	// Not a planning leak; release whole buffer
	res := p.buffer
	p.buffer = ""
	return res, false
}

// StrippedPlanning returns true if a planning leak was stripped.
func (p *PlanningBuffer) StrippedPlanning() bool {
	return p.strippedPlanning
}
