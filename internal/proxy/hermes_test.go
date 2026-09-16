package proxy_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/google"
	"gemini-bridge/internal/proxy"
)

// 1. TestHermes_OpenAI_MultiTurnToolCalling_Streaming verifies Hermes multi-turn tool calling
// with reasoning content, thought_signatures, and token usage details under OpenAI protocol.
func TestHermes_OpenAI_MultiTurnToolCalling_Streaming(t *testing.T) {
	proxy.ClearThoughtSignatures()

	var receivedReqs []*google.GeminiInternalRequest
	var mu sync.Mutex

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var internalReq google.GeminiInternalRequest
		_ = json.Unmarshal(bodyBytes, &internalReq)

		mu.Lock()
		receivedReqs = append(receivedReqs, &internalReq)
		turn := len(receivedReqs)
		mu.Unlock()

		if strings.HasSuffix(r.URL.Path, ":generateContent") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			respJSON := `{"candidates":[{"content":{"parts":[{"text":"Directory contains: file1.txt, file2.txt"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":220,"candidatesTokenCount":15,"totalTokenCount":235}}`
			_, _ = w.Write([]byte(respJSON))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if turn == 1 {
			// Model responds with reasoning, thought signature, and a tool call
			chunk1 := `data: {"candidates":[{"content":{"parts":[{"text":"I should check the files in directory.","thought":true,"thoughtSignature":"sig-hermes-turn1"},{"functionCall":{"id":"call_123","name":"execute_command","args":{"cmd":"ls -la"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":150,"candidatesTokenCount":35,"totalTokenCount":185,"thoughtsTokenCount":25}}` + "\n\n"
			_, _ = w.Write([]byte(chunk1))
		} else {
			// Turn 2: Model finishes with final answer
			chunk2 := `data: {"candidates":[{"content":{"parts":[{"text":"Directory contains: file1.txt, file2.txt"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":220,"candidatesTokenCount":15,"totalTokenCount":235}}` + "\n\n"
			_, _ = w.Write([]byte(chunk2))
		}
	}

	acc := makeDefaultAccount("acc-1", "hermes@test.com")
	srv, proxyHTTP, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "test-key")
	defer proxyHTTP.Close()
	defer upstream.Close()
	_ = srv

	client := proxyHTTP.Client()

	// Turn 1: Hermes requests directory listing with reasoning and stream: true
	reqBodyTurn1 := proxy.OpenAIChatRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []proxy.OpenAIMessage{
			{Role: "system", Content: "You are Hermes autonomous agent."},
			{Role: "user", Content: "List directory contents."},
		},
		Tools: []proxy.OpenAITool{
			{
				Type: "function",
				Function: proxy.OpenAIFunction{
					Name:        "execute_command",
					Description: "Run shell command",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"cmd": map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		},
		Stream:          true,
		ReasoningEffort: "high",
	}

	b1, _ := json.Marshal(reqBodyTurn1)
	httpReq1, _ := http.NewRequest(http.MethodPost, proxyHTTP.URL+"/v1/chat/completions", bytes.NewReader(b1))
	httpReq1.Header.Set("Authorization", "Bearer test-key")
	httpReq1.Header.Set("Content-Type", "application/json")

	resp1, err := client.Do(httpReq1)
	if err != nil {
		t.Fatalf("Turn 1 request failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("Turn 1 expected status 200, got %d", resp1.StatusCode)
	}

	// Read SSE chunks
	scanner := bufio.NewScanner(resp1.Body)
	var accumulatedReasoning string
	var toolCallName, toolCallArgs, toolCallID string
	var reasoningTokensInUsage int

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
			var chunk proxy.OpenAIChatResponse
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err == nil {
				if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
					delta := chunk.Choices[0].Delta
					accumulatedReasoning += delta.ReasoningContent
					if len(delta.ToolCalls) > 0 {
						toolCallName = delta.ToolCalls[0].Function.Name
						toolCallArgs = delta.ToolCalls[0].Function.Arguments
						toolCallID = delta.ToolCalls[0].ID
					}
				}
				if chunk.Usage != nil && chunk.Usage.CompletionTokensDetails != nil {
					reasoningTokensInUsage = chunk.Usage.CompletionTokensDetails.ReasoningTokens
				}
			}
		}
	}

	if accumulatedReasoning != "I should check the files in directory." {
		t.Errorf("expected accumulated reasoning, got %q", accumulatedReasoning)
	}
	if toolCallName != "execute_command" {
		t.Errorf("expected tool execute_command, got %q", toolCallName)
	}
	if !strings.Contains(toolCallArgs, "ls -la") {
		t.Errorf("expected arguments containing ls -la, got %q", toolCallArgs)
	}
	if reasoningTokensInUsage != 25 {
		t.Errorf("expected 25 reasoning tokens in usage, got %d", reasoningTokensInUsage)
	}

	// Verify upstream received model gemini-3.5-flash-high and ThinkingLevel HIGH
	mu.Lock()
	if len(receivedReqs) != 1 {
		t.Fatalf("expected 1 upstream request, got %d", len(receivedReqs))
	}
	if receivedReqs[0].Model != "gemini-3.5-flash-high" {
		t.Errorf("expected model gemini-3.5-flash-high, got %s", receivedReqs[0].Model)
	}
	if receivedReqs[0].Request.GenerationConfig.ThinkingConfig == nil || receivedReqs[0].Request.GenerationConfig.ThinkingConfig.ThinkingLevel != "HIGH" {
		t.Errorf("expected ThinkingLevel HIGH, got %+v", receivedReqs[0].Request.GenerationConfig.ThinkingConfig)
	}
	mu.Unlock()

	// Turn 2: Hermes executes command and sends tool output
	reqBodyTurn2 := proxy.OpenAIChatRequest{
		Model: "gemini-3.8-flash-high",
		Messages: []proxy.OpenAIMessage{
			{Role: "system", Content: "You are Hermes autonomous agent."},
			{Role: "user", Content: "List directory contents."},
			{
				Role: "assistant",
				ToolCalls: []proxy.OpenAIToolCall{
					{
						ID:   toolCallID,
						Type: "function",
						Function: proxy.OpenAIFunctionCall{
							Name:      toolCallName,
							Arguments: toolCallArgs,
						},
					},
				},
			},
			{
				Role:       "tool",
				Name:       toolCallName,
				ToolCallID: toolCallID,
				Content:    "file1.txt\nfile2.txt",
			},
		},
		Stream: false,
	}

	b2, _ := json.Marshal(reqBodyTurn2)
	httpReq2, _ := http.NewRequest(http.MethodPost, proxyHTTP.URL+"/v1/chat/completions", bytes.NewReader(b2))
	httpReq2.Header.Set("Authorization", "Bearer test-key")
	httpReq2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(httpReq2)
	if err != nil {
		t.Fatalf("Turn 2 request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Turn 2 expected status 200, got %d", resp2.StatusCode)
	}

	// Verify that in Turn 2, the assistant's FunctionCall part retained the thought_signature!
	mu.Lock()
	if len(receivedReqs) != 2 {
		t.Fatalf("expected 2 upstream requests, got %d", len(receivedReqs))
	}
	turn2Contents := receivedReqs[1].Request.Contents
	var foundRestoredSig bool
	for _, c := range turn2Contents {
		if c.Role == "model" {
			for _, p := range c.Parts {
				if p.FunctionCall != nil && p.ThoughtSignature == "sig-hermes-turn1" {
					foundRestoredSig = true
					break
				}
			}
		}
	}
	mu.Unlock()

	if !foundRestoredSig {
		t.Error("expected thoughtSignature 'sig-hermes-turn1' to be restored on model function call part in Turn 2")
	}
}

// 2. TestHermes_Anthropic_MultiTurnToolCalling_Streaming verifies Anthropic protocol
// with streaming, thought signatures, thinking delta events, and token details.
func TestHermes_Anthropic_MultiTurnToolCalling_Streaming(t *testing.T) {
	proxy.ClearThoughtSignatures()

	var receivedReqs []*google.GeminiInternalRequest
	var mu sync.Mutex

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var internalReq google.GeminiInternalRequest
		_ = json.Unmarshal(bodyBytes, &internalReq)

		mu.Lock()
		receivedReqs = append(receivedReqs, &internalReq)
		turn := len(receivedReqs)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if turn == 1 {
			chunk1 := `data: {"candidates":[{"content":{"parts":[{"text":"Thinking about user request...","thought":true,"thoughtSignature":"sig-claude-turn1"},{"functionCall":{"id":"toolu_999","name":"read_config","args":{"path":"/etc/hermes.yaml"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":40,"totalTokenCount":140,"thoughtsTokenCount":30}}` + "\n\n"
			_, _ = w.Write([]byte(chunk1))
		} else {
			chunk2 := `data: {"candidates":[{"content":{"parts":[{"text":"Config loaded successfully."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":160,"candidatesTokenCount":10,"totalTokenCount":170}}` + "\n\n"
			_, _ = w.Write([]byte(chunk2))
		}
	}

	acc := makeDefaultAccount("acc-1", "hermes@test.com")
	srv, proxyHTTP, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "test-key")
	defer proxyHTTP.Close()
	defer upstream.Close()
	_ = srv

	client := proxyHTTP.Client()

	reqBodyTurn1 := proxy.AnthropicMessagesRequest{
		Model: "claude-3-7-sonnet",
		Messages: []proxy.AnthropicMessage{
			{Role: "user", Content: "Read configuration file."},
		},
		Tools: []proxy.AnthropicTool{
			{
				Name:        "read_config",
				Description: "Read config from path",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		Stream:   true,
		Thinking: &proxy.AnthropicThinkingConfig{Type: "adaptive"},
	}

	b1, _ := json.Marshal(reqBodyTurn1)
	httpReq1, _ := http.NewRequest(http.MethodPost, proxyHTTP.URL+"/v1/messages", bytes.NewReader(b1))
	httpReq1.Header.Set("Authorization", "Bearer test-key")
	httpReq1.Header.Set("Content-Type", "application/json")

	resp1, err := client.Do(httpReq1)
	if err != nil {
		t.Fatalf("Anthropic Turn 1 request failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("Anthropic Turn 1 expected status 200, got %d", resp1.StatusCode)
	}

	scanner := bufio.NewScanner(resp1.Body)
	var hasThinkingDelta bool
	var hasSignatureDelta bool
	var hasToolUseStart bool
	var thinkingTokensInUsage int

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &event); err == nil {
				eType, _ := event["type"].(string)
				if eType == "content_block_delta" {
					if delta, ok := event["delta"].(map[string]interface{}); ok {
						dType, _ := delta["type"].(string)
						if dType == "thinking_delta" {
							hasThinkingDelta = true
						}
						if dType == "signature_delta" {
							hasSignatureDelta = true
						}
					}
				} else if eType == "content_block_start" {
					if cb, ok := event["content_block"].(map[string]interface{}); ok {
						if cb["type"] == "tool_use" {
							hasToolUseStart = true
						}
					}
				} else if eType == "message_delta" {
					if usage, ok := event["usage"].(map[string]interface{}); ok {
						if details, ok := usage["output_tokens_details"].(map[string]interface{}); ok {
							if val, ok := details["thinking_tokens"].(float64); ok {
								thinkingTokensInUsage = int(val)
							}
						}
					}
				}
			}
		}
	}

	if !hasThinkingDelta {
		t.Error("expected thinking_delta event in stream")
	}
	if !hasSignatureDelta {
		t.Error("expected signature_delta event in stream")
	}
	if !hasToolUseStart {
		t.Error("expected content_block_start for tool_use in stream")
	}
	if thinkingTokensInUsage != 30 {
		t.Errorf("expected 30 thinking tokens in message_delta usage, got %d", thinkingTokensInUsage)
	}

	// Verify thought signature was saved in proxy cache
	savedSig := proxy.GetThoughtSignature("toolu_999", "read_config")
	if savedSig != "sig-claude-turn1" {
		t.Errorf("expected saved thought signature 'sig-claude-turn1', got %q", savedSig)
	}
}

// 3. TestHermes_ReasoningEffort_And_ThinkingToggle verifies explicit toggle combinations
func TestHermes_ReasoningEffort_And_ThinkingToggle(t *testing.T) {
	// A) OpenAI reasoning_effort: "none"
	reqNone := &proxy.OpenAIChatRequest{
		Model:           "gemini-3.8-flash-high",
		ReasoningEffort: "none",
		Messages:        []proxy.OpenAIMessage{{Role: "user", Content: "Hello"}},
	}
	geminiNone := proxy.MapOpenAIToGemini(reqNone, "test-proj")
	if geminiNone.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be set")
	}
	if *geminiNone.Request.GenerationConfig.ThinkingConfig.IncludeThoughts != false {
		t.Errorf("expected IncludeThoughts false for reasoning_effort=none")
	}
	if *geminiNone.Request.GenerationConfig.ThinkingConfig.ThinkingBudget != 0 {
		t.Errorf("expected ThinkingBudget 0 for reasoning_effort=none")
	}

	// B) OpenAI reasoning_effort: "low" -> ThinkingLevel LOW
	reqLow := &proxy.OpenAIChatRequest{
		Model:           "gemini-3.8-flash",
		ReasoningEffort: "low",
		Messages:        []proxy.OpenAIMessage{{Role: "user", Content: "Hello"}},
	}
	geminiLow := proxy.MapOpenAIToGemini(reqLow, "test-proj")
	if geminiLow.Request.GenerationConfig.ThinkingConfig.ThinkingLevel != "LOW" {
		t.Errorf("expected ThinkingLevel LOW, got %s", geminiLow.Request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}

	// C) Anthropic thinking: {"type": "disabled"} on claude-3-7-sonnet
	reqAnthropicDisabled := &proxy.AnthropicMessagesRequest{
		Model:    "claude-3-7-sonnet",
		Thinking: &proxy.AnthropicThinkingConfig{Type: "disabled"},
		Messages: []proxy.AnthropicMessage{{Role: "user", Content: "Hi"}},
	}
	geminiAnthropicDisabled := proxy.MapAnthropicToGemini(reqAnthropicDisabled, "test-proj")
	if geminiAnthropicDisabled.Request.GenerationConfig.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig")
	}
	if *geminiAnthropicDisabled.Request.GenerationConfig.ThinkingConfig.IncludeThoughts != false {
		t.Errorf("expected IncludeThoughts false for Anthropic thinking disabled")
	}

	// D) Anthropic output_config: {"effort": "medium"}
	reqAnthropicEffort := &proxy.AnthropicMessagesRequest{
		Model:        "claude-3-5-sonnet",
		OutputConfig: &proxy.AnthropicOutputConfig{Effort: "medium"},
		Messages:     []proxy.AnthropicMessage{{Role: "user", Content: "Hi"}},
	}
	geminiAnthropicEffort := proxy.MapAnthropicToGemini(reqAnthropicEffort, "test-proj")
	if geminiAnthropicEffort.Request.GenerationConfig.ThinkingConfig.ThinkingLevel != "MEDIUM" {
		t.Errorf("expected ThinkingLevel MEDIUM, got %s", geminiAnthropicEffort.Request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}
}

// 4. TestHermes_ResponseFormat_JSON verifies structured outputs
func TestHermes_ResponseFormat_JSON(t *testing.T) {
	// json_object format
	req1 := &proxy.OpenAIChatRequest{
		Model:          "gpt-4o",
		ResponseFormat: map[string]interface{}{"type": "json_object"},
		Messages:       []proxy.OpenAIMessage{{Role: "user", Content: "Output JSON"}},
	}
	gemini1 := proxy.MapOpenAIToGemini(req1, "proj")
	if gemini1.Request.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("expected responseMimeType application/json, got %q", gemini1.Request.GenerationConfig.ResponseMimeType)
	}

	// json_schema format
	req2 := &proxy.OpenAIChatRequest{
		Model: "gpt-4o",
		ResponseFormat: map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
		Messages: []proxy.OpenAIMessage{{Role: "user", Content: "Output Schema JSON"}},
	}
	gemini2 := proxy.MapOpenAIToGemini(req2, "proj")
	if gemini2.Request.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("expected responseMimeType application/json, got %q", gemini2.Request.GenerationConfig.ResponseMimeType)
	}
	if gemini2.Request.GenerationConfig.ResponseSchema == nil {
		t.Error("expected ResponseSchema to be set")
	}
}

// 5. TestHermes_ConcurrentSubagentLeasing verifies concurrent account pool leasing
// without deadlocks or mutex contention under high parallelism.
func TestHermes_ConcurrentSubagentLeasing(t *testing.T) {
	acc1 := makeDefaultAccount("acc-1", "agent1@test.com")
	acc2 := makeDefaultAccount("acc-2", "agent2@test.com")
	acc3 := makeDefaultAccount("acc-3", "agent3@test.com")

	loader := &testMockLoader{accounts: []*account.CloudAccount{acc1, acc2, acc3}}
	pool, err := account.NewPool(loader, "test-dir")
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	const concurrentWorkers = 50
	const leasesPerWorker = 20

	var wg sync.WaitGroup
	errCh := make(chan error, concurrentWorkers*leasesPerWorker)

	for i := 0; i < concurrentWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < leasesPerWorker; j++ {
				leased, lErr := pool.LeaseAccount("gemini-3-flash")
				if lErr != nil {
					errCh <- fmt.Errorf("worker %d lease %d failed: %w", workerID, j, lErr)
					return
				}
				if leased == nil || leased.Token.AccessToken == "" {
					errCh <- fmt.Errorf("worker %d got invalid account: %+v", workerID, leased)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// 6. TestHermes_ClientDisconnect_ContextCancellation verifies that when a client cancels context,
// streaming handles it gracefully without hanging.
func TestHermes_ClientDisconnect_ContextCancellation(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Chunk 1\"}]}}]}\n\n"))
		flusher.Flush()
		// Sleep to simulate slow upstream
		time.Sleep(2 * time.Second)
	}

	acc := makeDefaultAccount("acc-1", "hermes@test.com")
	_, proxyHTTP, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "test-key")
	defer proxyHTTP.Close()
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())

	reqBody := proxy.OpenAIChatRequest{
		Model:    "gemini-3-flash",
		Messages: []proxy.OpenAIMessage{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}
	b, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, proxyHTTP.URL+"/v1/chat/completions", bytes.NewReader(b))
	httpReq.Header.Set("Authorization", "Bearer test-key")
	httpReq.Header.Set("Content-Type", "application/json")

	client := proxyHTTP.Client()

	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Wait for first chunk, then cancel context immediately
	<-upstreamStarted
	cancel()

	// Read should terminate promptly due to cancellation
	buf := make([]byte, 1024)
	_, _ = resp.Body.Read(buf)
}
