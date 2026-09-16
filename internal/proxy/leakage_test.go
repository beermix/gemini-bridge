package proxy

import (
	"testing"
)

func TestTryParseLeakedToolCall(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOk    bool
		wantName  string
		wantArgs  string
	}{
		{
			name:     "strict JSON",
			input:    `call:default_api:read{"file_path":"C:/tmp/a.txt"}`,
			wantOk:   true,
			wantName: "read",
			wantArgs: `{"file_path":"C:/tmp/a.txt"}`,
		},
		{
			name:     "no arguments",
			input:    "call:default_api:Read",
			wantOk:   true,
			wantName: "Read",
			wantArgs: "{}",
		},
		{
			name:     "bare object keys",
			input:    `call:default_api:Read{file_path:"a"}`,
			wantOk:   true,
			wantName: "Read",
			wantArgs: `{"file_path":"a"}`,
		},
		{
			name:     "wrapped in parentheses",
			input:    `call:default_api:search({"query":"test"})`,
			wantOk:   true,
			wantName: "search",
			wantArgs: `{"query":"test"}`,
		},
		{
			name:     "with whitespace padding",
			input:    `   call:default_api:execute{"command":"ls"}   `,
			wantOk:   true,
			wantName: "execute",
			wantArgs: `{"command":"ls"}`,
		},
		{
			name:   "surrounding prose prefix",
			input:  `Use call:default_api:Read{"file_path":"a"}`,
			wantOk: false,
		},
		{
			name:   "malformed arguments",
			input:  `call:default_api:Read{file_path:NOT_JSON}`,
			wantOk: false,
		},
		{
			name:   "array arguments not object",
			input:  `call:default_api:Read["a"]`,
			wantOk: false,
		},
		{
			name:   "normal text",
			input:  "Just regular assistant message text.",
			wantOk: false,
		},
		{
			name:     "wrapped in markdown code fence",
			input:    "```\ncall:default_api:run_command{\"command\":\"cat file.txt\"}\n```",
			wantOk:   true,
			wantName: "run_command",
			wantArgs: `{"command":"cat file.txt"}`,
		},
		{
			name:     "wrapped in markdown code fence with lang",
			input:    "```json\ncall:default_api:run_command{\"command\":\"cat file.txt\"}\n```",
			wantOk:   true,
			wantName: "run_command",
			wantArgs: `{"command":"cat file.txt"}`,
		},
		{
			name:     "wrapped in XML brackets",
			input:    `<call:default_api:fetch_url{"url":"https://google.com"}>`,
			wantOk:   true,
			wantName: "fetch_url",
			wantArgs: `{"url":"https://google.com"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TryParseLeakedToolCall(tt.input)
			if ok != tt.wantOk {
				t.Fatalf("TryParseLeakedToolCall(%q) ok = %v, wantOk = %v", tt.input, ok, tt.wantOk)
			}
			if ok {
				if got.Name != tt.wantName {
					t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
				}
				if got.Arguments != tt.wantArgs {
					t.Errorf("Arguments = %q, want %q", got.Arguments, tt.wantArgs)
				}
			}
		})
	}
}
