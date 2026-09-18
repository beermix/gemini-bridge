package proxy_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gemini-bridge/internal/google"
	"gemini-bridge/internal/proxy"
)

func TestMapper_ResolveModel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Claude Sonnet variants
		{"claude-3-7-sonnet-20250219", "claude-sonnet-4-6"},
		{"claude-3-5-sonnet-20241022", "claude-sonnet-4-6"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"claude-sonnet-4-5-20250101", "claude-sonnet-4-6"},
		{"claude-3-7-sonnet", "claude-sonnet-4-6"},

		// Claude Opus variants
		{"claude-3-opus-20240229", "claude-opus-4-6-thinking"},
		{"claude-opus-4-6", "claude-opus-4-6-thinking"},
		{"claude-opus-4-5-20250201", "claude-opus-4-6-thinking"},

		// Claude Haiku variants
		{"claude-3-haiku-20240307", "claude-sonnet-4-6"},
		{"claude-haiku-4", "claude-sonnet-4-6"},

		// OpenAI GPT variants
		{"gpt-4o", "gemini-3-flash"},
		{"gpt-4o-mini", "gemini-3-flash"},
		{"gpt-4-turbo", "gemini-3-flash"},
		{"gpt-4", "gemini-3-flash"},
		{"gpt-3.5-turbo", "gemini-3-flash"},
		{"gpt-3.5-turbo-0125", "gemini-3-flash"},

		// Gemini Pro variants
		{"gemini-2.5-pro", "gemini-3.1-pro-low"},
		{"gemini-2.5-pro-preview", "gemini-3.1-pro-low"},
		{"gemini-3.1-pro", "gemini-3.1-pro-low"},
		{"gemini-3.1-pro-preview", "gemini-3.1-pro-low"},

		// Gemini Flash variants
		{"gemini-flash-latest", "gemini-3-flash"},
		{"gemini-flash-lite-latest", "gemini-3-flash"},
		{"gemini-2.5-flash", "gemini-3-flash"},
		{"gemini-3-flash", "gemini-3-flash"},
		{"gemini-3.8-flash", "gemini-3-flash"},
		{"gemini-3.8-flash-high", "gemini-3-flash"},
		{"gemini-3.8-flash-medium", "gemini-3-flash"},
		{"gemini-3.8-flash-low", "gemini-3-flash"},
		{"gemini-3.5-flash-high", "gemini-3-flash"},
		{"gemini-3.5-flash-high-preview", "gemini-3-flash"},
		{"gemini-3.7-flash-high", "gemini-3-flash"},
		{"gemini-3.7-flash-medium", "gemini-3-flash"},
		{"gemini-3.7-flash-low", "gemini-3-flash"},
		{"gemini-3.5-flash-medium", "gemini-3-flash"},
		{"gemini-3.5-flash-low", "gemini-3-flash"},
		{"gemini-3.5-flash-extra-low", "gemini-3-flash"},

		// ChatGPT & OpenAI compatibility variants
		{"chatgpt", "gemini-3-flash"},
		{"chatgpt-4o-latest", "gemini-3-flash"},
		{"o1-mini", "gemini-3-flash"},
		{"o3-mini", "gemini-3-flash"},

		// Passthrough
		{"gemini-pro-agent", "gemini-pro-agent"},
		{"claude-sonnet-4-6-thinking", "claude-sonnet-4-6"},
		{"custom-fine-tuned-model", "custom-fine-tuned-model"},
		{"", "gemini-3-flash"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := proxy.ResolveModel(tt.input)
			if got != tt.expected {
				t.Errorf("ResolveModel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMapper_OpenAIToGemini_Basic(t *testing.T) {
	temp := 0.7
	topP := 0.95
	maxTokens := 2048

	req := &proxy.OpenAIChatRequest{
		Model: "gpt-4o",
		Messages: []proxy.OpenAIMessage{
			{Role: "system", Content: "You are a helpful coding assistant."},
			{Role: "user", Content: "Write a hello world program in Go."},
			{Role: "assistant", Content: "package main\n\nfunc main() {}"},
			{Role: "user", Content: "Add a print statement."},
		},
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   &maxTokens,
		Stop:        []string{"END"},
	}

	internalReq := proxy.MapOpenAIToGemini(req, "test-project-123")
	if internalReq == nil {
		t.Fatal("MapOpenAIToGemini returned nil")
	}

	if internalReq.Project != "test-project-123" {
		t.Errorf("expected project 'test-project-123', got %q", internalReq.Project)
	}
	if internalReq.Model != "gemini-3-flash" {
		t.Errorf("expected model 'gemini-3-flash', got %q", internalReq.Model)
	}

	// System instruction check
	if internalReq.Request.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be populated")
	}
	if len(internalReq.Request.SystemInstruction.Parts) == 0 {
		t.Fatal("expected SystemInstruction parts not to be empty")
	}
	if internalReq.Request.SystemInstruction.Parts[0].Text != "You are a helpful coding assistant." {
		t.Errorf("unexpected system instruction text: %q", internalReq.Request.SystemInstruction.Parts[0].Text)
	}

	// Dialogue contents check (system message should not be in Contents)
	if len(internalReq.Request.Contents) != 3 {
		t.Fatalf("expected 3 conversation turns, got %d", len(internalReq.Request.Contents))
	}
	if internalReq.Request.Contents[0].Role != "user" || internalReq.Request.Contents[0].Parts[0].Text != "Write a hello world program in Go." {
		t.Errorf("unexpected first turn: %+v", internalReq.Request.Contents[0])
	}
	if internalReq.Request.Contents[1].Role != "model" || internalReq.Request.Contents[1].Parts[0].Text != "package main\n\nfunc main() {}" {
		t.Errorf("unexpected second turn: %+v", internalReq.Request.Contents[1])
	}
	if internalReq.Request.Contents[2].Role != "user" || internalReq.Request.Contents[2].Parts[0].Text != "Add a print statement." {
		t.Errorf("unexpected third turn: %+v", internalReq.Request.Contents[2])
	}

	// GenerationConfig check
	cfg := internalReq.Request.GenerationConfig
	if cfg == nil {
		t.Fatal("expected GenerationConfig not nil")
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", cfg.Temperature)
	}
	if cfg.TopP == nil || *cfg.TopP != 0.95 {
		t.Errorf("expected topP 0.95, got %v", cfg.TopP)
	}
	if cfg.MaxOutputTokens == nil || *cfg.MaxOutputTokens != 2048 {
		t.Errorf("expected maxOutputTokens 2048, got %v", cfg.MaxOutputTokens)
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Errorf("unexpected stop sequences: %+v", cfg.StopSequences)
	}
}

func TestMapper_OpenAIToGemini_ToolsAndToolCalls(t *testing.T) {
	req := &proxy.OpenAIChatRequest{
		Model: "gemini-3.1-pro",
		Tools: []proxy.OpenAITool{
			{
				Type: "function",
				Function: proxy.OpenAIFunction{
					Name:        "get_weather",
					Description: "Get current weather in location",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"location": map[string]interface{}{"type": "string"},
						},
						"required": []interface{}{"location"},
					},
				},
			},
		},
		Messages: []proxy.OpenAIMessage{
			{Role: "user", Content: "What is the weather in Moscow?"},
			{
				Role: "assistant",
				ToolCalls: []proxy.OpenAIToolCall{
					{
						ID:   "call_weather_1",
						Type: "function",
						Function: proxy.OpenAIFunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"Moscow"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_weather_1",
				Content:    `{"temp_c": 18, "condition": "Sunny"}`,
			},
		},
	}

	internalReq := proxy.MapOpenAIToGemini(req, "proj-456")
	if internalReq == nil {
		t.Fatal("expected internalReq not nil")
	}

	// Verify Tools
	if len(internalReq.Request.Tools) != 1 {
		t.Fatalf("expected 1 GeminiTool, got %d", len(internalReq.Request.Tools))
	}
	decls := internalReq.Request.Tools[0].FunctionDeclarations
	if len(decls) != 1 || decls[0].Name != "get_weather" {
		t.Fatalf("unexpected function declaration: %+v", decls)
	}

	// Verify Contents
	contents := internalReq.Request.Contents
	if len(contents) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(contents))
	}

	// Turn 1: user
	if contents[0].Role != "user" {
		t.Errorf("turn 1 role = %q, want 'user'", contents[0].Role)
	}

	// Turn 2: assistant with tool call -> role: "model"
	if contents[1].Role != "model" {
		t.Errorf("turn 2 role = %q, want 'model'", contents[1].Role)
	}
	if len(contents[1].Parts) != 1 || contents[1].Parts[0].FunctionCall == nil {
		t.Fatalf("expected FunctionCall part, got %+v", contents[1].Parts)
	}
	fc := contents[1].Parts[0].FunctionCall
	if fc.ID != "call_weather_1" || fc.Name != "get_weather" {
		t.Errorf("unexpected FunctionCall: %+v", fc)
	}
	if fc.Args["location"] != "Moscow" {
		t.Errorf("unexpected FunctionCall Args: %+v", fc.Args)
	}

	// Turn 3: tool result -> role: "user" with FunctionResponse
	if contents[2].Role != "user" {
		t.Errorf("turn 3 role = %q, want 'user'", contents[2].Role)
	}
	if len(contents[2].Parts) != 1 || contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected FunctionResponse part, got %+v", contents[2].Parts)
	}
	fr := contents[2].Parts[0].FunctionResponse
	if fr.ID != "call_weather_1" || fr.Name != "get_weather" {
		t.Errorf("unexpected FunctionResponse: %+v", fr)
	}
	if fr.Response["temp_c"] != float64(18) && fr.Response["temp_c"] != 18 {
		t.Errorf("unexpected FunctionResponse Response: %+v", fr.Response)
	}
}

func TestMapper_OpenAIToGemini_Base64Image(t *testing.T) {
	req := &proxy.OpenAIChatRequest{
		Model: "gpt-4o",
		Messages: []proxy.OpenAIMessage{
			{
				Role: "user",
				Content: []proxy.OpenAIContentPart{
					{Type: "text", Text: "Describe this image."},
					{
						Type: "image_url",
						ImageURL: &proxy.OpenAIImageURL{
							URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
						},
					},
				},
			},
		},
	}

	internalReq := proxy.MapOpenAIToGemini(req, "proj-img")
	if internalReq == nil {
		t.Fatal("expected internalReq not nil")
	}

	if len(internalReq.Request.Contents) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(internalReq.Request.Contents))
	}
	parts := internalReq.Request.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + inlineData), got %d", len(parts))
	}

	if parts[0].Text != "Describe this image." {
		t.Errorf("part 0 text = %q, want 'Describe this image.'", parts[0].Text)
	}
	if parts[1].InlineData == nil {
		t.Fatal("part 1 InlineData is nil")
	}
	if parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("mimeType = %q, want 'image/png'", parts[1].InlineData.MimeType)
	}
	if !strings.HasPrefix(parts[1].InlineData.Data, "iVBORw0KGgo") {
		t.Errorf("unexpected inline data prefix: %q", parts[1].InlineData.Data)
	}
}

func TestMapper_ParseGeminiChunk(t *testing.T) {
	// 1. Wrapped response line
	wrapped := []byte(`data: {"response": {"candidates": [{"content": {"parts": [{"text": "Hello world"}]}}]}}`)
	resp, err := proxy.ParseGeminiChunk(wrapped)
	if err != nil {
		t.Fatalf("ParseGeminiChunk wrapped error: %v", err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content.Parts[0].Text != "Hello world" {
		t.Errorf("unexpected wrapped resp: %+v", resp)
	}

	// 2. Bare candidates line
	bare := []byte(`{"candidates": [{"content": {"parts": [{"text": "Bare line"}]}}]}`)
	resp, err = proxy.ParseGeminiChunk(bare)
	if err != nil {
		t.Fatalf("ParseGeminiChunk bare error: %v", err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content.Parts[0].Text != "Bare line" {
		t.Errorf("unexpected bare resp: %+v", resp)
	}

	// 3. [DONE] marker
	done := []byte("data: [DONE]")
	resp, err = proxy.ParseGeminiChunk(done)
	if err != nil {
		t.Fatalf("ParseGeminiChunk done error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil for [DONE], got %+v", resp)
	}

	// 4. Invalid JSON
	invalid := []byte("data: {invalid json}")
	_, err = proxy.ParseGeminiChunk(invalid)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestMapper_FormatOpenAIChunk(t *testing.T) {
	// Test text and thinking content
	cand := &google.Candidate{
		Index: 0,
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{Thought: true, Text: "<think>Planning the response</think>"},
				{Text: "Here is the response."},
			},
		},
		FinishReason: "STOP",
	}
	usage := &google.UsageMetadata{
		PromptTokenCount:     15,
		CandidatesTokenCount: 25,
		TotalTokenCount:      40,
	}

	chunkBytes := proxy.FormatOpenAIChunk("chatcmpl-123", "gpt-4o", cand, usage)
	if len(chunkBytes) == 0 {
		t.Fatal("FormatOpenAIChunk returned empty bytes")
	}

	// Verify SSE formatting: starts with "data: " and ends with "\n\n"
	str := string(chunkBytes)
	if !strings.HasPrefix(str, "data: ") || !strings.HasSuffix(str, "\n\n") {
		t.Fatalf("chunk not formatted as SSE data: %q", str)
	}

	// Unmarshal chunk JSON
	data := bytes.TrimSuffix(bytes.TrimPrefix(chunkBytes, []byte("data: ")), []byte("\n\n"))
	var chunk proxy.OpenAIChatResponse
	if err := json.Unmarshal(data, &chunk); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}

	if chunk.ID != "chatcmpl-123" {
		t.Errorf("chunk ID = %q, want 'chatcmpl-123'", chunk.ID)
	}
	if len(chunk.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(chunk.Choices))
	}
	choice := chunk.Choices[0]
	if choice.Delta == nil {
		t.Fatal("choice Delta is nil")
	}
	if choice.Delta.ReasoningContent != "Planning the response" {
		t.Errorf("delta ReasoningContent = %q, want 'Planning the response'", choice.Delta.ReasoningContent)
	}
	if choice.Delta.Content != "Here is the response." {
		t.Errorf("delta Content = %q, want 'Here is the response.'", choice.Delta.Content)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Errorf("choice FinishReason = %v, want 'stop'", choice.FinishReason)
	}
	if chunk.Usage == nil || chunk.Usage.TotalTokens != 40 {
		t.Errorf("chunk Usage = %+v, want TotalTokens=40", chunk.Usage)
	}
}

func TestMapper_FormatOpenAIChunk_ToolCalls(t *testing.T) {
	cand := &google.Candidate{
		Index: 0,
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{
					FunctionCall: &google.FunctionCall{
						ID:   "call_abc_1",
						Name: "calculator",
						Args: map[string]interface{}{"expr": "2+2"},
					},
				},
			},
		},
		FinishReason: "STOP",
	}

	chunkBytes := proxy.FormatOpenAIChunk("chatcmpl-tools", "gpt-4o", cand, nil)
	data := bytes.TrimSuffix(bytes.TrimPrefix(chunkBytes, []byte("data: ")), []byte("\n\n"))
	var chunk proxy.OpenAIChatResponse
	if err := json.Unmarshal(data, &chunk); err != nil {
		t.Fatalf("unmarshal chunk error: %v", err)
	}

	if len(chunk.Choices) != 1 || chunk.Choices[0].Delta == nil {
		t.Fatal("expected choice with delta")
	}
	tcs := chunk.Choices[0].Delta.ToolCalls
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call in delta, got %d", len(tcs))
	}
	if tcs[0].ID != "call_abc_1" || tcs[0].Function.Name != "calculator" {
		t.Errorf("unexpected tool call: %+v", tcs[0])
	}
	if !strings.Contains(tcs[0].Function.Arguments, "2+2") {
		t.Errorf("unexpected arguments: %q", tcs[0].Function.Arguments)
	}
	// FinishReason for tool call should map to "tool_calls"
	if chunk.Choices[0].FinishReason == nil || *chunk.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %v, want 'tool_calls'", chunk.Choices[0].FinishReason)
	}
}

func TestMapper_FormatOpenAIResponse(t *testing.T) {
	resp := &google.GeminiResponse{
		Candidates: []google.Candidate{
			{
				Index: 0,
				Content: google.GeminiContent{
					Parts: []google.GeminiPart{
						{Text: "Complete answer to your question."},
					},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: google.UsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 15,
			TotalTokenCount:      25,
		},
	}

	openAIResp := proxy.FormatOpenAIResponse("chatcmpl-sync-1", "gpt-4o", resp)
	if openAIResp == nil {
		t.Fatal("FormatOpenAIResponse returned nil")
	}

	if openAIResp.ID != "chatcmpl-sync-1" {
		t.Errorf("ID = %q, want 'chatcmpl-sync-1'", openAIResp.ID)
	}
	if openAIResp.Object != "chat.completion" {
		t.Errorf("Object = %q, want 'chat.completion'", openAIResp.Object)
	}
	if len(openAIResp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(openAIResp.Choices))
	}
	choice := openAIResp.Choices[0]
	if choice.Message == nil || choice.Message.Content != "Complete answer to your question." {
		t.Errorf("unexpected choice message: %+v", choice.Message)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Errorf("unexpected finish_reason: %v", choice.FinishReason)
	}
	if openAIResp.Usage == nil || openAIResp.Usage.TotalTokens != 25 {
		t.Errorf("unexpected usage: %+v", openAIResp.Usage)
	}
}

func TestMapper_AnthropicToGemini_BasicAndSystem(t *testing.T) {
	temp := 0.5
	req := &proxy.AnthropicMessagesRequest{
		Model:  "claude-3-7-sonnet-20250219",
		System: "You are a concise Claude assistant.",
		Messages: []proxy.AnthropicMessage{
			{Role: "user", Content: "Hello!"},
			{Role: "assistant", Content: "Greetings! How can I assist?"},
			{Role: "user", Content: "Tell me a joke."},
		},
		MaxTokens:   1000,
		Temperature: &temp,
	}

	internalReq := proxy.MapAnthropicToGemini(req, "proj-anthropic")
	if internalReq == nil {
		t.Fatal("MapAnthropicToGemini returned nil")
	}

	if internalReq.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want 'claude-sonnet-4-6'", internalReq.Model)
	}

	// System instruction check
	if internalReq.Request.SystemInstruction == nil || len(internalReq.Request.SystemInstruction.Parts) == 0 {
		t.Fatal("expected system instruction")
	}
	if internalReq.Request.SystemInstruction.Parts[0].Text != "You are a concise Claude assistant." {
		t.Errorf("unexpected system instruction: %q", internalReq.Request.SystemInstruction.Parts[0].Text)
	}

	// Dialogue turns check
	if len(internalReq.Request.Contents) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(internalReq.Request.Contents))
	}
	if internalReq.Request.Contents[0].Role != "user" || internalReq.Request.Contents[0].Parts[0].Text != "Hello!" {
		t.Errorf("turn 0 mismatch: %+v", internalReq.Request.Contents[0])
	}
	if internalReq.Request.Contents[1].Role != "model" || internalReq.Request.Contents[1].Parts[0].Text != "Greetings! How can I assist?" {
		t.Errorf("turn 1 mismatch: %+v", internalReq.Request.Contents[1])
	}
	if internalReq.Request.Contents[2].Role != "user" || internalReq.Request.Contents[2].Parts[0].Text != "Tell me a joke." {
		t.Errorf("turn 2 mismatch: %+v", internalReq.Request.Contents[2])
	}
}

func TestMapper_AnthropicToGemini_ToolsAndToolResults(t *testing.T) {
	req := &proxy.AnthropicMessagesRequest{
		Model: "claude-3-5-sonnet",
		Tools: []proxy.AnthropicTool{
			{
				Name:        "search",
				Description: "Search internet",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		Messages: []proxy.AnthropicMessage{
			{Role: "user", Content: "Search for Go 1.27"},
			{
				Role: "assistant",
				Content: []proxy.AnthropicContentBlock{
					{
						Type: "tool_use",
						ID:   "toolu_search_1",
						Name: "search",
						Input: map[string]interface{}{
							"query": "Go 1.27 release notes",
						},
					},
				},
			},
			{
				Role: "user",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_search_1",
						Content:   `{"status":"found","results":["Go 1.27 features"]}`,
					},
				},
			},
		},
	}

	internalReq := proxy.MapAnthropicToGemini(req, "proj-tools")
	if internalReq == nil {
		t.Fatal("expected internalReq not nil")
	}

	// Verify Tools
	if len(internalReq.Request.Tools) != 1 || len(internalReq.Request.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("unexpected tools: %+v", internalReq.Request.Tools)
	}
	decl := internalReq.Request.Tools[0].FunctionDeclarations[0]
	if decl.Name != "search" {
		t.Errorf("decl name = %q, want 'search'", decl.Name)
	}

	// Verify Contents
	contents := internalReq.Request.Contents
	if len(contents) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(contents))
	}

	// Turn 1: assistant tool use -> Gemini role "model" FunctionCall
	if contents[1].Role != "model" || contents[1].Parts[0].FunctionCall == nil {
		t.Fatalf("turn 1 expected model FunctionCall, got %+v", contents[1])
	}
	fc := contents[1].Parts[0].FunctionCall
	if fc.ID != "toolu_search_1" || fc.Name != "search" || fc.Args["query"] != "Go 1.27 release notes" {
		t.Errorf("unexpected FunctionCall: %+v", fc)
	}

	// Turn 2: user tool result -> Gemini role "user" FunctionResponse with resolved Name
	if contents[2].Role != "user" || contents[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("turn 2 expected user FunctionResponse, got %+v", contents[2])
	}
	fr := contents[2].Parts[0].FunctionResponse
	if fr.ID != "toolu_search_1" || fr.Name != "search" {
		t.Errorf("unexpected FunctionResponse: %+v", fr)
	}
}

func TestMapper_AnthropicToGemini_ThinkingAndImages(t *testing.T) {
	req := &proxy.AnthropicMessagesRequest{
		Model: "claude-3-7-sonnet",
		Thinking: &proxy.AnthropicThinkingConfig{
			Type:         "enabled",
			BudgetTokens: 4096,
		},
		Messages: []proxy.AnthropicMessage{
			{
				Role: "user",
				Content: []proxy.AnthropicContentBlock{
					{Type: "text", Text: "Look at this snapshot."},
					{
						Type: "image",
						Source: &proxy.AnthropicImageSource{
							Type:      "base64",
							MediaType: "image/jpeg",
							Data:      "/9j/4AAQSkZJRg==",
						},
					},
				},
			},
		},
	}

	internalReq := proxy.MapAnthropicToGemini(req, "proj-think-img")
	if internalReq == nil {
		t.Fatal("expected internalReq not nil")
	}

	// Check ThinkingConfig
	tc := internalReq.Request.GenerationConfig.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 4096 {
		t.Errorf("unexpected ThinkingConfig: %+v", tc)
	}

	// Check InlineData
	parts := internalReq.Request.Contents[0].Parts
	if len(parts) != 2 || parts[1].InlineData == nil {
		t.Fatalf("expected 2 parts with InlineData, got %+v", parts)
	}
	if parts[1].InlineData.MimeType != "image/jpeg" || parts[1].InlineData.Data != "/9j/4AAQSkZJRg==" {
		t.Errorf("unexpected InlineData: %+v", parts[1].InlineData)
	}
}

func TestMapper_FormatAnthropicEvents(t *testing.T) {
	state := &proxy.AnthropicStreamState{
		InputTokens: 20,
	}

	// First chunk with thinking
	cand1 := &google.Candidate{
		Index: 0,
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{Thought: true, Text: "<think>Let me reason first</think>"},
			},
		},
	}

	events1 := proxy.FormatAnthropicEvents("msg_123", "claude-3-7-sonnet", cand1, state)
	if len(events1) == 0 {
		t.Fatal("expected events in chunk 1, got 0")
	}

	// Chunk 1 should have: message_start, content_block_start (thinking), content_block_delta (thinking_delta)
	hasMsgStart := false
	hasThinkingStart := false
	hasThinkingDelta := false
	for _, ev := range events1 {
		s := string(ev)
		if strings.Contains(s, "event: message_start") {
			hasMsgStart = true
		}
		if strings.Contains(s, "event: content_block_start") && strings.Contains(s, `"type":"thinking"`) {
			hasThinkingStart = true
		}
		if strings.Contains(s, "event: content_block_delta") && strings.Contains(s, `"thinking_delta"`) && strings.Contains(s, "Let me reason first") {
			hasThinkingDelta = true
		}
	}
	if !hasMsgStart || !hasThinkingStart || !hasThinkingDelta {
		t.Errorf("events1 missing expected parts: msgStart=%v, thinkingStart=%v, thinkingDelta=%v\n%s",
			hasMsgStart, hasThinkingStart, hasThinkingDelta, string(bytes.Join(events1, nil)))
	}

	// Second chunk with text and finish reason STOP
	cand2 := &google.Candidate{
		Index: 0,
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{Text: "Final answer."},
			},
		},
		FinishReason: "STOP",
	}

	events2 := proxy.FormatAnthropicEvents("msg_123", "claude-3-7-sonnet", cand2, state)
	hasTextStart := false
	hasTextDelta := false
	hasMsgDelta := false
	hasMsgStop := false
	for _, ev := range events2 {
		s := string(ev)
		if strings.Contains(s, "event: content_block_start") && strings.Contains(s, `"type":"text"`) {
			hasTextStart = true
		}
		if strings.Contains(s, "event: content_block_delta") && strings.Contains(s, `"text_delta"`) && strings.Contains(s, "Final answer.") {
			hasTextDelta = true
		}
		if strings.Contains(s, "event: message_delta") && strings.Contains(s, `"end_turn"`) {
			hasMsgDelta = true
		}
		if strings.Contains(s, "event: message_stop") {
			hasMsgStop = true
		}
	}

	if !hasTextStart || !hasTextDelta || !hasMsgDelta || !hasMsgStop {
		t.Errorf("events2 missing expected parts: textStart=%v, textDelta=%v, msgDelta=%v, msgStop=%v\n%s",
			hasTextStart, hasTextDelta, hasMsgDelta, hasMsgStop, string(bytes.Join(events2, nil)))
	}
}

func TestMapper_FormatAnthropicResponse(t *testing.T) {
	resp := &google.GeminiResponse{
		Candidates: []google.Candidate{
			{
				Index: 0,
				Content: google.GeminiContent{
					Parts: []google.GeminiPart{
						{Thought: true, Text: "<think>Hidden reasoning</think>"},
						{Text: "Here is your solution."},
						{
							FunctionCall: &google.FunctionCall{
								ID:   "tool_calc",
								Name: "calc",
								Args: map[string]interface{}{"val": 42},
							},
						},
					},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: google.UsageMetadata{
			PromptTokenCount:     50,
			CandidatesTokenCount: 60,
		},
	}

	anthropicResp := proxy.FormatAnthropicResponse("msg_full_1", "claude-3-7-sonnet", resp)
	if anthropicResp == nil {
		t.Fatal("FormatAnthropicResponse returned nil")
	}

	if anthropicResp.ID != "msg_full_1" || anthropicResp.Role != "assistant" {
		t.Errorf("unexpected ID or Role: %+v", anthropicResp)
	}

	if len(anthropicResp.Content) != 3 {
		t.Fatalf("expected 3 content blocks (thinking, text, tool_use), got %d", len(anthropicResp.Content))
	}

	// Block 0: thinking
	if anthropicResp.Content[0].Type != "thinking" || anthropicResp.Content[0].Thinking != "Hidden reasoning" {
		t.Errorf("block 0 mismatch: %+v", anthropicResp.Content[0])
	}
	// Block 1: text
	if anthropicResp.Content[1].Type != "text" || anthropicResp.Content[1].Text != "Here is your solution." {
		t.Errorf("block 1 mismatch: %+v", anthropicResp.Content[1])
	}
	// Block 2: tool_use
	if anthropicResp.Content[2].Type != "tool_use" || anthropicResp.Content[2].Name != "calc" {
		t.Errorf("block 2 mismatch: %+v", anthropicResp.Content[2])
	}

	// Stop reason should be "tool_use" when tool is called
	if anthropicResp.StopReason == nil || *anthropicResp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %v, want 'tool_use'", anthropicResp.StopReason)
	}

	if anthropicResp.Usage.InputTokens != 50 || anthropicResp.Usage.OutputTokens != 60 {
		t.Errorf("unexpected usage: %+v", anthropicResp.Usage)
	}
}

func TestMapper_OpenAIToGemini_ToolChoiceAndStopVariants(t *testing.T) {
	// 1. ToolChoice string modes
	modes := []struct {
		choice   string
		wantMode string
	}{
		{"auto", "AUTO"},
		{"none", "NONE"},
		{"required", "ANY"},
	}
	dummyTools := []proxy.OpenAITool{
		{Type: "function", Function: proxy.OpenAIFunction{Name: "lookup_user"}},
	}
	for _, tc := range modes {
		req := &proxy.OpenAIChatRequest{
			Model:      "gemini-3-flash",
			Tools:      dummyTools,
			ToolChoice: tc.choice,
		}
		res := proxy.MapOpenAIToGemini(req, "p")
		if res.Request.ToolConfig == nil || res.Request.ToolConfig.FunctionCallingConfig.Mode != tc.wantMode {
			t.Errorf("choice %q expected mode %q", tc.choice, tc.wantMode)
		}
	}

	// 2. ToolChoice named function map
	reqNamed := &proxy.OpenAIChatRequest{
		Model: "gemini-3-flash",
		Tools: dummyTools,
		ToolChoice: map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name": "lookup_user",
			},
		},
	}
	resNamed := proxy.MapOpenAIToGemini(reqNamed, "p")
	if resNamed.Request.ToolConfig == nil || resNamed.Request.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("expected mode ANY for named function choice")
	}
	if len(resNamed.Request.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
		resNamed.Request.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "lookup_user" {
		t.Errorf("expected allowedFunctionNames ['lookup_user'], got %+v",
			resNamed.Request.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
	}

	// 3. Stop as single string
	reqStopStr := &proxy.OpenAIChatRequest{
		Model: "gemini-3-flash",
		Stop:  "STOP_HERE",
	}
	resStopStr := proxy.MapOpenAIToGemini(reqStopStr, "p")
	if len(resStopStr.Request.GenerationConfig.StopSequences) != 1 ||
		resStopStr.Request.GenerationConfig.StopSequences[0] != "STOP_HERE" {
		t.Errorf("unexpected StopSequences for string stop: %+v", resStopStr.Request.GenerationConfig.StopSequences)
	}

	// 4. Stop as []interface{}
	reqStopSlice := &proxy.OpenAIChatRequest{
		Model: "gemini-3-flash",
		Stop:  []interface{}{"STOP_1", "STOP_2"},
	}
	resStopSlice := proxy.MapOpenAIToGemini(reqStopSlice, "p")
	if len(resStopSlice.Request.GenerationConfig.StopSequences) != 2 {
		t.Errorf("expected 2 stop sequences, got %d", len(resStopSlice.Request.GenerationConfig.StopSequences))
	}
}

func TestMapper_OpenAI_NilAndEdgeCases(t *testing.T) {
	// Nil request
	if got := proxy.MapOpenAIToGemini(nil, "p"); got != nil {
		t.Errorf("expected nil for nil req, got %+v", got)
	}
	if got := proxy.FormatOpenAIResponse("1", "m", nil); got != nil {
		t.Errorf("expected nil for nil resp, got %+v", got)
	}

	// Usage-only chunk (cand == nil)
	usage := &google.UsageMetadata{TotalTokenCount: 100}
	chunk := proxy.FormatOpenAIChunk("id", "m", nil, usage)
	data := bytes.TrimSuffix(bytes.TrimPrefix(chunk, []byte("data: ")), []byte("\n\n"))
	var resp proxy.OpenAIChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("failed to unmarshal usage chunk: %v", err)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 100 {
		t.Errorf("unexpected usage in chunk: %+v", resp.Usage)
	}
	if len(resp.Choices) != 0 {
		t.Errorf("expected 0 choices in usage-only chunk, got %d", len(resp.Choices))
	}

	// Finish reason mappings
	candMax := &google.Candidate{FinishReason: "MAX_TOKENS"}
	cBytesMax := proxy.FormatOpenAIChunk("id", "m", candMax, nil)
	if !strings.Contains(string(cBytesMax), `"finish_reason":"length"`) {
		t.Errorf("MAX_TOKENS should map to length, got %s", string(cBytesMax))
	}

	candSafety := &google.Candidate{FinishReason: "SAFETY"}
	cBytesSafety := proxy.FormatOpenAIChunk("id", "m", candSafety, nil)
	if !strings.Contains(string(cBytesSafety), `"finish_reason":"content_filter"`) {
		t.Errorf("SAFETY should map to content_filter, got %s", string(cBytesSafety))
	}
}

func TestMapper_Anthropic_NilAndEdgeCases(t *testing.T) {
	// Nil request
	if got := proxy.MapAnthropicToGemini(nil, "p"); got != nil {
		t.Errorf("expected nil for nil req, got %+v", got)
	}
	if got := proxy.FormatAnthropicResponse("1", "m", nil); got != nil {
		t.Errorf("expected nil for nil resp, got %+v", got)
	}

	// System as array of interfaces
	reqSys := &proxy.AnthropicMessagesRequest{
		Model: "claude-3-7-sonnet",
		System: []interface{}{
			map[string]interface{}{"type": "text", "text": "Part 1"},
			map[string]interface{}{"type": "text", "text": "Part 2"},
		},
	}
	resSys := proxy.MapAnthropicToGemini(reqSys, "p")
	if resSys.Request.SystemInstruction == nil || len(resSys.Request.SystemInstruction.Parts) != 2 {
		t.Fatalf("expected 2 system parts, got %+v", resSys.Request.SystemInstruction)
	}

	// Tool result with IsError: true
	reqErr := &proxy.AnthropicMessagesRequest{
		Model: "claude-3-7-sonnet",
		Messages: []proxy.AnthropicMessage{
			{
				Role: "user",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "t_1",
						Content:   "Command failed with exit code 1",
						IsError:   true,
					},
				},
			},
		},
	}
	resErr := proxy.MapAnthropicToGemini(reqErr, "p")
	if len(resErr.Request.Contents) != 1 || resErr.Request.Contents[0].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected FunctionResponse part, got %+v", resErr.Request.Contents)
	}
	fr := resErr.Request.Contents[0].Parts[0].FunctionResponse
	if fr.Response["is_error"] != true {
		t.Errorf("expected is_error=true, got %+v", fr.Response)
	}

	// FormatAnthropicEvents with tool use
	state := &proxy.AnthropicStreamState{}
	candTool := &google.Candidate{
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{
					FunctionCall: &google.FunctionCall{
						ID:   "call_tool_1",
						Name: "grep",
						Args: map[string]interface{}{"pattern": "func"},
					},
				},
			},
		},
		FinishReason: "STOP",
	}
	events := proxy.FormatAnthropicEvents("msg_tool", "claude-3-7-sonnet", candTool, state)
	joined := string(bytes.Join(events, nil))
	if !strings.Contains(joined, `"type":"tool_use"`) {
		t.Errorf("expected tool_use block start, got: %s", joined)
	}
	if !strings.Contains(joined, `"input_json_delta"`) {
		t.Errorf("expected input_json_delta, got: %s", joined)
	}
	if !strings.Contains(joined, `"stop_reason":"tool_use"`) {
		t.Errorf("expected stop_reason tool_use, got: %s", joined)
	}
}

func TestMapper_ReviewFindings_Verification(t *testing.T) {
	// 1. Whitespace and newlines preserved in cleanThinkingTags
	candWS := &google.Candidate{
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{Thought: true, Text: "<think> thinking with spaces </think>"},
				{Text: " world\n\n"},
			},
		},
	}
	chunkBytes := proxy.FormatOpenAIChunk("id_ws", "gpt-4o", candWS, nil)
	data := bytes.TrimSuffix(bytes.TrimPrefix(chunkBytes, []byte("data: ")), []byte("\n\n"))
	var chunkResp proxy.OpenAIChatResponse
	if err := json.Unmarshal(data, &chunkResp); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}
	if len(chunkResp.Choices) != 1 || chunkResp.Choices[0].Delta == nil {
		t.Fatal("expected 1 choice with delta")
	}
	delta := chunkResp.Choices[0].Delta
	if delta.ReasoningContent != " thinking with spaces " {
		t.Errorf("delta.ReasoningContent = %q, want %q", delta.ReasoningContent, " thinking with spaces ")
	}
	if delta.Content != " world\n\n" {
		t.Errorf("delta.Content = %q, want %q", delta.Content, " world\n\n")
	}

	// 2. Fallback ID when part.FunctionCall.ID == "" in FormatOpenAIChunk and FormatOpenAIResponse
	candEmptyID := &google.Candidate{
		Content: google.GeminiContent{
			Parts: []google.GeminiPart{
				{
					FunctionCall: &google.FunctionCall{
						ID:   "", // empty ID from Gemini
						Name: "calculator",
						Args: map[string]interface{}{"op": "add"},
					},
				},
			},
		},
	}
	chunkNoID := proxy.FormatOpenAIChunk("id_noid", "gpt-4o", candEmptyID, nil)
	dataNoID := bytes.TrimSuffix(bytes.TrimPrefix(chunkNoID, []byte("data: ")), []byte("\n\n"))
	var respNoID proxy.OpenAIChatResponse
	if err := json.Unmarshal(dataNoID, &respNoID); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}
	if len(respNoID.Choices[0].Delta.ToolCalls) != 1 {
		t.Fatal("expected 1 tool call in delta")
	}
	if respNoID.Choices[0].Delta.ToolCalls[0].ID == "" {
		t.Errorf("expected non-empty fallback tool call ID in chunk")
	}

	respObj := &google.GeminiResponse{
		Candidates: []google.Candidate{
			{
				Content: google.GeminiContent{
					Parts: []google.GeminiPart{
						{Thought: true, Text: "Reasoning here"},
						{Text: "Answer text"},
						{
							FunctionCall: &google.FunctionCall{
								ID:   "", // empty ID
								Name: "calculator",
								Args: map[string]interface{}{"op": "add"},
							},
						},
					},
				},
			},
		},
	}
	fullResp := proxy.FormatOpenAIResponse("resp_noid", "gpt-4o", respObj)
	if fullResp == nil || len(fullResp.Choices) != 1 {
		t.Fatal("expected fullResp with 1 choice")
	}
	msg := fullResp.Choices[0].Message
	if msg == nil {
		t.Fatal("message is nil")
	}
	if msg.ReasoningContent != "Reasoning here" {
		t.Errorf("msg.ReasoningContent = %q, want %q", msg.ReasoningContent, "Reasoning here")
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID == "" {
		t.Errorf("expected non-empty fallback ID in non-streaming response tool call: %+v", msg.ToolCalls)
	}

	// 3. ToolConfig only set when len(req.Tools) > 0
	reqNoTools := &proxy.OpenAIChatRequest{
		Model:      "gemini-3-flash",
		ToolChoice: "auto",
	}
	resNoTools := proxy.MapOpenAIToGemini(reqNoTools, "p")
	if resNoTools.Request.ToolConfig != nil {
		t.Errorf("expected ToolConfig to be nil when Tools is empty, got %+v", resNoTools.Request.ToolConfig)
	}

	reqWithTools := &proxy.OpenAIChatRequest{
		Model:      "gemini-3-flash",
		ToolChoice: "auto",
		Tools: []proxy.OpenAITool{
			{Type: "function", Function: proxy.OpenAIFunction{Name: "fn"}},
		},
	}
	resWithTools := proxy.MapOpenAIToGemini(reqWithTools, "p")
	if resWithTools.Request.ToolConfig == nil {
		t.Errorf("expected ToolConfig not nil when Tools present")
	}

	// 4. AnthropicMessagesRequest ToolChoice field
	anthropicJSON := []byte(`{"model":"claude-3-7-sonnet","max_tokens":100,"tool_choice":{"type":"auto"}}`)
	var anthReq proxy.AnthropicMessagesRequest
	if err := json.Unmarshal(anthropicJSON, &anthReq); err != nil {
		t.Fatalf("failed to unmarshal AnthropicMessagesRequest with tool_choice: %v", err)
	}
	if anthReq.ToolChoice == nil {
		t.Errorf("expected ToolChoice to be populated")
	}

	// 5. ParseGeminiChunk SSE comment skipping (: comment / heartbeat)
	commentSSE := []byte(": ping heartbeat\n")
	respComment, err := proxy.ParseGeminiChunk(commentSSE)
	if err != nil {
		t.Fatalf("ParseGeminiChunk comment error: %v", err)
	}
	if respComment != nil {
		t.Errorf("expected nil for comment line, got %+v", respComment)
	}
}

func TestMapper_OpenAIToGemini_DuplicateToolCallID(t *testing.T) {
	req := &proxy.OpenAIChatRequest{
		Model: "gemini-3-flash-medium",
		Messages: []proxy.OpenAIMessage{
			{Role: "user", Content: "Run step 1"},
			{
				Role: "assistant",
				ToolCalls: []proxy.OpenAIToolCall{
					{
						ID:   "call_dup_123",
						Type: "function",
						Function: proxy.OpenAIFunctionCall{
							Name:      "terminal",
							Arguments: `{"command":"echo 1"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_dup_123",
				Content:    `{"output":"1"}`,
			},
			{Role: "user", Content: "Run step 2"},
			{
				Role: "assistant",
				ToolCalls: []proxy.OpenAIToolCall{
					{
						ID:   "call_dup_123", // Reused ID in later turn
						Type: "function",
						Function: proxy.OpenAIFunctionCall{
							Name:      "write_file",
							Arguments: `{"path":"/tmp/a"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_dup_123",
				Content:    `{"status":"ok"}`,
			},
		},
	}

	res := proxy.MapOpenAIToGemini(req, "test-proj")
	contents := res.Request.Contents

	// Expect 7 turns:
	// Turn 0: user "Run step 1"
	// Turn 1: assistant tool call 1
	// Turn 2: tool result 1
	// Turn 3: model InterruptedResponsePlaceholder (separating tool result from subsequent user text)
	// Turn 4: user "Run step 2"
	// Turn 5: assistant tool call 2
	// Turn 6: tool result 2
	if len(contents) != 7 {
		t.Fatalf("expected 7 turns, got %d", len(contents))
	}

	// Turn 1: assistant tool call 1 -> ID should be "call_dup_123", Name "terminal"
	fc1 := contents[1].Parts[0].FunctionCall
	if fc1 == nil || fc1.ID != "call_dup_123" || fc1.Name != "terminal" {
		t.Errorf("expected fc1.ID == 'call_dup_123' and Name == 'terminal', got %+v", fc1)
	}

	// Turn 2: tool result 1 -> ID should match "call_dup_123", Name "terminal"
	fr1 := contents[2].Parts[0].FunctionResponse
	if fr1 == nil || fr1.ID != "call_dup_123" || fr1.Name != "terminal" {
		t.Errorf("expected fr1.ID == 'call_dup_123' and Name == 'terminal', got %+v", fr1)
	}

	// Turn 3: model InterruptedResponsePlaceholder
	if contents[3].Role != "model" || len(contents[3].Parts) == 0 || contents[3].Parts[0].Text != proxy.InterruptedResponsePlaceholder {
		t.Errorf("expected turn 3 to be model with InterruptedResponsePlaceholder, got %+v", contents[3])
	}

	// Turn 5: assistant tool call 2 -> ID should be remapped to "call_dup_123_d2", Name "write_file"
	fc2 := contents[5].Parts[0].FunctionCall
	if fc2 == nil || fc2.ID != "call_dup_123_d2" || fc2.Name != "write_file" {
		t.Errorf("expected fc2.ID == 'call_dup_123_d2' and Name == 'write_file', got %+v", fc2)
	}

	// Turn 6: tool result 2 -> ID should match remapped "call_dup_123_d2", Name "write_file"
	fr2 := contents[6].Parts[0].FunctionResponse
	if fr2 == nil || fr2.ID != "call_dup_123_d2" || fr2.Name != "write_file" {
		t.Errorf("expected fr2.ID == 'call_dup_123_d2' and Name == 'write_file', got %+v", fr2)
	}
}

func TestMapper_AnthropicToGemini_DuplicateToolCallID(t *testing.T) {
	req := &proxy.AnthropicMessagesRequest{
		Model: "claude-3-7-sonnet",
		Messages: []proxy.AnthropicMessage{
			{Role: "user", Content: "Run step 1"},
			{
				Role: "assistant",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:  "tool_use",
						ID:    "toolu_dup_1",
						Name:  "terminal",
						Input: map[string]interface{}{"command": "echo 1"},
					},
				},
			},
			{
				Role: "user",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_dup_1",
						Content:   `{"output":"1"}`,
					},
				},
			},
			{
				Role: "assistant",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:  "tool_use",
						ID:    "toolu_dup_1", // Reused ID in later turn
						Name:  "write_file",
						Input: map[string]interface{}{"path": "/tmp/a"},
					},
				},
			},
			{
				Role: "user",
				Content: []proxy.AnthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "toolu_dup_1",
						Content:   `{"status":"ok"}`,
					},
				},
			},
		},
	}

	res := proxy.MapAnthropicToGemini(req, "test-proj")
	contents := res.Request.Contents

	if len(contents) != 5 {
		t.Fatalf("expected 5 turns, got %d", len(contents))
	}

	fc1 := contents[1].Parts[0].FunctionCall
	if fc1 == nil || fc1.ID != "toolu_dup_1" {
		t.Errorf("expected fc1.ID == 'toolu_dup_1', got %+v", fc1)
	}

	fr1 := contents[2].Parts[0].FunctionResponse
	if fr1 == nil || fr1.ID != "toolu_dup_1" {
		t.Errorf("expected fr1.ID == 'toolu_dup_1', got %+v", fr1)
	}

	fc2 := contents[3].Parts[0].FunctionCall
	if fc2 == nil || fc2.ID != "toolu_dup_1_d2" {
		t.Errorf("expected fc2.ID == 'toolu_dup_1_d2', got %+v", fc2)
	}

	fr2 := contents[4].Parts[0].FunctionResponse
	if fr2 == nil || fr2.ID != "toolu_dup_1_d2" {
		t.Errorf("expected fr2.ID == 'toolu_dup_1_d2', got %+v", fr2)
	}
}



