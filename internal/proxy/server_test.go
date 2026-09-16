package proxy_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/google"
	"gemini-bridge/internal/proxy"
)

type testMockLoader struct {
	accounts []*account.CloudAccount
	pinnedID string
}

func (m *testMockLoader) LoadAccounts(dir string) ([]*account.CloudAccount, error) {
	out := make([]*account.CloudAccount, len(m.accounts))
	for i, a := range m.accounts {
		cp := *a
		out[i] = &cp
	}
	return out, nil
}

func (m *testMockLoader) SaveAccountsCache(dir string, accs []*account.CloudAccount) error {
	return nil
}

func (m *testMockLoader) LoadPinnedAccount(dir string) (string, error) {
	return m.pinnedID, nil
}

func (m *testMockLoader) SavePinnedAccount(dir string, accountID string) error {
	m.pinnedID = accountID
	return nil
}

func setupTestServer(t *testing.T, upstreamHandler http.HandlerFunc, accounts []*account.CloudAccount, apiKey string) (*proxy.Server, *httptest.Server, *httptest.Server) {
	upstream := httptest.NewServer(upstreamHandler)

	client := google.NewClient(5 * time.Second)
	client.SetBaseURLs(upstream.URL, upstream.URL)

	loader := &testMockLoader{accounts: accounts}
	pool, err := account.NewPool(loader, "test-config")
	if err != nil {
		t.Fatalf("failed to create account pool: %v", err)
	}

	cfg := proxy.ServerConfig{
		Addr:   "127.0.0.1:0",
		APIKey: apiKey,
	}
	srv := proxy.NewServer(cfg, pool, client)
	proxyHTTP := httptest.NewServer(srv.Handler())

	return srv, proxyHTTP, upstream
}

func makeDefaultAccount(id, email string) *account.CloudAccount {
	return &account.CloudAccount{
		ID:       id,
		Email:    email,
		Provider: "google",
		Status:   "active",
		Token: account.CloudToken{
			AccessToken:     "token-" + id,
			TokenType:       "Bearer",
			ProjectID:       "proj-" + id,
			ExpiryTimestamp: time.Now().Unix() + 3600,
		},
	}
}

// 1. TestOpenAI_ChatCompletions_NonStreaming
func TestOpenAI_ChatCompletions_NonStreaming(t *testing.T) {
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			http.Error(w, "invalid path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		var req google.GeminiInternalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model != "gemini-3-flash" {
			http.Error(w, "expected gemini-3-flash, got "+req.Model, http.StatusBadRequest)
			return
		}

		resp := google.GeminiResponse{
			Candidates: []google.Candidate{
				{
					Content: google.GeminiContent{
						Role: "model",
						Parts: []google.GeminiPart{
							{Text: "Hello from Gemini!"},
						},
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
			UsageMetadata: google.UsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
				TotalTokenCount:      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	acc := makeDefaultAccount("acc1", "user1@example.com")
	srv, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":false}`
	resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to make POST request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}

	var chatResp proxy.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if chatResp.Object != "chat.completion" {
		t.Errorf("expected object chat.completion, got %q", chatResp.Object)
	}
	if len(chatResp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(chatResp.Choices))
	}
	choice := chatResp.Choices[0]
	if choice.Message == nil || choice.Message.Content != "Hello from Gemini!" {
		t.Errorf("unexpected choice message content: %+v", choice.Message)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Errorf("expected finish_reason stop, got %v", choice.FinishReason)
	}
	if chatResp.Usage == nil || chatResp.Usage.TotalTokens != 15 {
		t.Errorf("expected usage 15, got %+v", chatResp.Usage)
	}

	stats := srv.GetStats()
	if stats.TotalRequests != 1 || stats.SuccessRequests != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

// 2. TestOpenAI_ChatCompletions_Streaming
func TestOpenAI_ChatCompletions_Streaming(t *testing.T) {
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":streamGenerateContent") || r.URL.Query().Get("alt") != "sse" {
			http.Error(w, "invalid stream path/query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		chunk1 := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"index":0}]}` + "\n\n"
		chunk2 := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":4,"totalTokenCount":9}}` + "\n\n"

		_, _ = w.Write([]byte(chunk1))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte(chunk2))
		flusher.Flush()
	}

	acc := makeDefaultAccount("acc1", "user1@example.com")
	srv, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"Stream test"}],"stream":true}`
	resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to make streaming request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	var receivedChunks []string
	var seenDone bool
	var contentBuilder strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("error reading stream: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			seenDone = true
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			receivedChunks = append(receivedChunks, line)
			var chunk proxy.OpenAIChatResponse
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err == nil {
				if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
					contentBuilder.WriteString(chunk.Choices[0].Delta.Content)
				}
			}
		}
	}

	if !seenDone {
		t.Errorf("expected stream to terminate with data: [DONE]")
	}
	if len(receivedChunks) < 2 {
		t.Errorf("expected at least 2 data chunks, got %d", len(receivedChunks))
	}
	if contentBuilder.String() != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", contentBuilder.String())
	}

	stats := srv.GetStats()
	if stats.TotalRequests != 1 || stats.SuccessRequests != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

// 3. TestOpenAI_ChatCompletions_ToolCalling
func TestOpenAI_ChatCompletions_ToolCalling(t *testing.T) {
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		var req google.GeminiInternalRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if len(req.Request.Tools) == 0 || len(req.Request.Tools[0].FunctionDeclarations) == 0 {
			http.Error(w, "missing function declarations in tools", http.StatusBadRequest)
			return
		}
		decl := req.Request.Tools[0].FunctionDeclarations[0]
		if decl.Name != "get_current_weather" {
			http.Error(w, "unexpected function name: "+decl.Name, http.StatusBadRequest)
			return
		}

		resp := google.GeminiResponse{
			Candidates: []google.Candidate{
				{
					Content: google.GeminiContent{
						Role: "model",
						Parts: []google.GeminiPart{
							{
								FunctionCall: &google.FunctionCall{
									ID:   "call_weather_123",
									Name: "get_current_weather",
									Args: map[string]interface{}{
										"location": "Tokyo",
									},
								},
							},
						},
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	acc := makeDefaultAccount("acc1", "user1@example.com")
	_, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	reqPayload := `{
		"model": "gpt-4o",
		"messages": [{"role": "user", "content": "What is the weather in Tokyo?"}],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_current_weather",
					"description": "Get weather",
					"parameters": {
						"type": "object",
						"properties": {
							"location": {"type": "string"}
						}
					}
				}
			}
		]
	}`

	resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqPayload))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}

	var chatResp proxy.OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(chatResp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(chatResp.Choices))
	}
	choice := chatResp.Choices[0]
	if choice.Message == nil || len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %+v", choice.Message)
	}
	tc := choice.Message.ToolCalls[0]
	if tc.Function.Name != "get_current_weather" {
		t.Errorf("expected function name get_current_weather, got %q", tc.Function.Name)
	}
	if !strings.Contains(tc.Function.Arguments, `"Tokyo"`) {
		t.Errorf("expected arguments containing Tokyo, got %q", tc.Function.Arguments)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason tool_calls, got %v", choice.FinishReason)
	}
}

// 4. TestOpenAI_ChatCompletions_429Failover
func TestOpenAI_ChatCompletions_429Failover(t *testing.T) {
	var attempts int32

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		auth := r.Header.Get("Authorization")

		// Account 1 gets 429
		if strings.Contains(auth, "token-acc1") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Resource exhausted: quota exceeded"}}`))
			return
		}

		// Account 2 succeeds
		if strings.Contains(auth, "token-acc2") {
			resp := google.GeminiResponse{
				Candidates: []google.Candidate{
					{
						Content: google.GeminiContent{
							Role:  "model",
							Parts: []google.GeminiPart{{Text: "Recovered via acc2!"}},
						},
						FinishReason: "STOP",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.Error(w, "unknown auth: "+auth, http.StatusUnauthorized)
	}

	acc1 := makeDefaultAccount("acc1", "user1@example.com")
	acc2 := makeDefaultAccount("acc2", "user2@example.com")

	srv, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc1, acc2}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	reqBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"Failover test"}],"stream":false}`
	resp, err := http.Post(proxyServer.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK after 429 failover, got %d: %s", resp.StatusCode, string(b))
	}

	var chatResp proxy.OpenAIChatResponse
	_ = json.NewDecoder(resp.Body).Decode(&chatResp)
	if len(chatResp.Choices) == 0 || chatResp.Choices[0].Message.Content != "Recovered via acc2!" {
		t.Fatalf("unexpected response content: %+v", chatResp)
	}

	// Verify stats
	stats := srv.GetStats()
	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 total request, got %d", stats.TotalRequests)
	}
	if stats.RateLimitRequests != 1 {
		t.Errorf("expected 1 rate limit event, got %d", stats.RateLimitRequests)
	}
	if stats.SuccessRequests != 1 {
		t.Errorf("expected 1 success request, got %d", stats.SuccessRequests)
	}

	// Verify account 1 is in cooldown
	accounts := srv.Pool().GetAccounts()
	var acc1Status, acc2Status string
	for _, a := range accounts {
		if a.ID == "acc1" {
			acc1Status = a.Status
			if !a.IsCooldown() {
				t.Errorf("expected acc1 to be in cooldown")
			}
		}
		if a.ID == "acc2" {
			acc2Status = a.Status
			if a.IsCooldown() {
				t.Errorf("acc2 should not be in cooldown")
			}
		}
	}
	if acc1Status != "cooldown" {
		t.Errorf("expected acc1 status to be cooldown, got %q", acc1Status)
	}
	if acc2Status != "active" {
		t.Errorf("expected acc2 status to be active, got %q", acc2Status)
	}
}

// 5. TestAnthropic_Messages_Streaming
func TestAnthropic_Messages_Streaming(t *testing.T) {
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			http.Error(w, "invalid path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}

		chunk1 := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"index":0}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":1,"totalTokenCount":13}}` + "\n\n"
		chunk2 := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":" Claude user!"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5,"totalTokenCount":17}}` + "\n\n"

		_, _ = w.Write([]byte(chunk1))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte(chunk2))
		flusher.Flush()
	}

	acc := makeDefaultAccount("acc1", "user1@example.com")
	srv, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	reqBody := `{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"Stream to Claude"}],"stream":true,"max_tokens":1024}`
	resp, err := http.Post(proxyServer.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("streaming request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", resp.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(resp.Body)
	var eventTypes []string
	var textBuilder strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("stream read error: %v", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			eventTypes = append(eventTypes, strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			var raw map[string]interface{}
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &raw)
			if delta, ok := raw["delta"].(map[string]interface{}); ok {
				if txt, ok := delta["text"].(string); ok {
					textBuilder.WriteString(txt)
				}
			}
		}
	}

	expectedEvents := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	for _, exp := range expectedEvents {
		found := false
		for _, ev := range eventTypes {
			if ev == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected Anthropic event %q in %v", exp, eventTypes)
		}
	}

	if textBuilder.String() != "Hello Claude user!" {
		t.Errorf("expected 'Hello Claude user!', got %q", textBuilder.String())
	}

	stats := srv.GetStats()
	if stats.TotalRequests != 1 || stats.SuccessRequests != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

// 6. TestAnthropic_Messages_NonStreaming
func TestAnthropic_Messages_NonStreaming(t *testing.T) {
	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":generateContent") {
			var req google.GeminiInternalRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Model != "claude-sonnet-4-6" {
				http.Error(w, "expected resolved model claude-sonnet-4-6, got "+req.Model, http.StatusBadRequest)
				return
			}

			resp := google.GeminiResponse{
				Candidates: []google.Candidate{
					{
						Content: google.GeminiContent{
							Role: "model",
							Parts: []google.GeminiPart{
								{
									Thought: true,
									Text:    "Thinking about Claude...",
								},
								{
									Text: "Greetings from Claude model!",
								},
							},
						},
						FinishReason: "STOP",
					},
				},
				UsageMetadata: google.UsageMetadata{
					PromptTokenCount:     20,
					CandidatesTokenCount: 15,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "unsupported path", http.StatusBadRequest)
	}

	acc := makeDefaultAccount("acc1", "user1@example.com")
	srv, proxyServer, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	// 1. Test POST /v1/messages
	reqBody := `{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"Non-streaming Anthropic"}],"max_tokens":1024,"stream":false}`
	resp, err := http.Post(proxyServer.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}

	var anthropicResp proxy.AnthropicMessagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		t.Fatalf("failed to decode Anthropic response: %v", err)
	}

	if anthropicResp.Type != "message" || anthropicResp.Role != "assistant" {
		t.Errorf("unexpected type/role: %s/%s", anthropicResp.Type, anthropicResp.Role)
	}
	if len(anthropicResp.Content) != 2 {
		t.Fatalf("expected 2 content blocks (thinking + text), got %d", len(anthropicResp.Content))
	}
	if anthropicResp.Content[0].Type != "thinking" || anthropicResp.Content[0].Thinking != "Thinking about Claude..." {
		t.Errorf("unexpected thinking block: %+v", anthropicResp.Content[0])
	}
	if anthropicResp.Content[1].Type != "text" || anthropicResp.Content[1].Text != "Greetings from Claude model!" {
		t.Errorf("unexpected text block: %+v", anthropicResp.Content[1])
	}
	if anthropicResp.StopReason == nil || *anthropicResp.StopReason != "end_turn" {
		t.Errorf("expected stop_reason end_turn, got %v", anthropicResp.StopReason)
	}

	// 2. Test POST /v1/messages/count_tokens
	countBody := `{"model":"claude-3-7-sonnet","messages":[{"role":"user","content":"Count these tokens please."}],"system":"You are helpful"}`
	respCount, err := http.Post(proxyServer.URL+"/v1/messages/count_tokens", "application/json", strings.NewReader(countBody))
	if err != nil {
		t.Fatalf("count_tokens failed: %v", err)
	}
	defer respCount.Body.Close()

	if respCount.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respCount.Body)
		t.Fatalf("count_tokens expected 200 OK, got %d: %s", respCount.StatusCode, string(b))
	}

	var countResp struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(respCount.Body).Decode(&countResp); err != nil {
		t.Fatalf("failed to decode count_tokens response: %v", err)
	}
	if countResp.InputTokens <= 0 {
		t.Errorf("expected input_tokens > 0, got %d", countResp.InputTokens)
	}

	stats := srv.GetStats()
	if stats.TotalRequests != 2 || stats.SuccessRequests != 2 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

// 7. TestModels_And_Health_Endpoints
func TestModels_And_Health_Endpoints(t *testing.T) {
	acc := makeDefaultAccount("acc1", "user1@example.com")
	srv, proxyServer, upstream := setupTestServer(t, nil, []*account.CloudAccount{acc}, "")
	defer proxyServer.Close()
	defer upstream.Close()

	// 1. Test /v1/models and /models
	for _, path := range []string{"/v1/models", "/models"} {
		resp, err := http.Get(proxyServer.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK for %s, got %d", path, resp.StatusCode)
		}

		var listResp struct {
			Object string `json:"object"`
			Data   []struct {
				ID      string `json:"id"`
				Object  string `json:"object"`
				OwnedBy string `json:"owned_by"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
			t.Fatalf("failed to decode models list: %v", err)
		}

		if listResp.Object != "list" {
			t.Errorf("expected object 'list', got %q", listResp.Object)
		}
		if len(listResp.Data) == 0 {
			t.Errorf("expected models in list, got 0")
		}
		var foundGemini, foundClaude, foundGPT bool
		for _, m := range listResp.Data {
			if strings.Contains(m.ID, "gemini") {
				foundGemini = true
			}
			if strings.Contains(m.ID, "claude") {
				foundClaude = true
			}
			if strings.Contains(m.ID, "gpt") {
				foundGPT = true
			}
			if m.ID == "gpt-4o" || m.ID == "gpt-4" || m.ID == "gpt-3.5-turbo" {
				t.Errorf("unexpected proprietary OpenAI model %q in catalog", m.ID)
			}
			if m.ID == "gpt-oss-120b-medium" && m.OwnedBy != "google" {
				t.Errorf("expected gpt-oss-120b-medium owned_by to be 'google', got %q", m.OwnedBy)
			}
		}
		if !foundGemini || !foundClaude || !foundGPT {
			t.Errorf("expected gemini, claude, and gpt models in catalog: foundGemini=%v, foundClaude=%v, foundGPT=%v", foundGemini, foundClaude, foundGPT)
		}
	}

	// 2. Test /health
	respHealth, err := http.Get(proxyServer.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer respHealth.Body.Close()

	if respHealth.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for /health, got %d", respHealth.StatusCode)
	}

	var healthResp struct {
		Status         string `json:"status"`
		AccountsTotal  int    `json:"accounts_total"`
		AccountsActive int    `json:"accounts_active"`
	}
	if err := json.NewDecoder(respHealth.Body).Decode(&healthResp); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	if healthResp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", healthResp.Status)
	}
	if healthResp.AccountsTotal != 1 || healthResp.AccountsActive != 1 {
		t.Errorf("expected 1 total and 1 active account, got %d total, %d active", healthResp.AccountsTotal, healthResp.AccountsActive)
	}

	_ = srv
}

// 8. TestAuth_Validation
func TestAuth_Validation(t *testing.T) {
	acc := makeDefaultAccount("acc1", "user1@example.com")
	_, proxyServer, upstream := setupTestServer(t, nil, []*account.CloudAccount{acc}, "secret-key-xyz")
	defer proxyServer.Close()
	defer upstream.Close()

	client := proxyServer.Client()

	// 1. Missing Auth Header -> 401
	req1, _ := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/models", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("req1 failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for missing key, got %d", resp1.StatusCode)
	}

	// 2. Invalid Bearer Token -> 401
	req2, _ := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/models", nil)
	req2.Header.Set("Authorization", "Bearer invalid-token")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("req2 failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for wrong bearer key, got %d", resp2.StatusCode)
	}

	// 3. Valid Bearer Token -> 200
	req3, _ := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/models", nil)
	req3.Header.Set("Authorization", "Bearer secret-key-xyz")
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("req3 failed: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for valid bearer key, got %d", resp3.StatusCode)
	}

	// 4. Valid x-api-key -> 200
	req4, _ := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/models", nil)
	req4.Header.Set("x-api-key", "secret-key-xyz")
	resp4, err := client.Do(req4)
	if err != nil {
		t.Fatalf("req4 failed: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for valid x-api-key, got %d", resp4.StatusCode)
	}

	// 5. /health does not require Auth -> 200
	req5, _ := http.NewRequest(http.MethodGet, proxyServer.URL+"/health", nil)
	resp5, err := client.Do(req5)
	if err != nil {
		t.Fatalf("req5 failed: %v", err)
	}
	resp5.Body.Close()
	if resp5.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for /health without auth, got %d", resp5.StatusCode)
	}

	// 6. Test with default sk-antigravity placeholder API key (allows all local clients)
	_, defaultProxyServer, defaultUpstream := setupTestServer(t, nil, []*account.CloudAccount{acc}, "sk-antigravity")
	defer defaultProxyServer.Close()
	defer defaultUpstream.Close()
	defaultClient := defaultProxyServer.Client()

	// 6a. No auth header -> 200
	req6a, _ := http.NewRequest(http.MethodGet, defaultProxyServer.URL+"/v1/models", nil)
	resp6a, err := defaultClient.Do(req6a)
	if err != nil {
		t.Fatalf("req6a failed: %v", err)
	}
	resp6a.Body.Close()
	if resp6a.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for sk-antigravity with no header, got %d", resp6a.StatusCode)
	}

	// 6b. Bearer no-key-required -> 200
	req6b, _ := http.NewRequest(http.MethodGet, defaultProxyServer.URL+"/v1/models", nil)
	req6b.Header.Set("Authorization", "Bearer no-key-required")
	resp6b, err := defaultClient.Do(req6b)
	if err != nil {
		t.Fatalf("req6b failed: %v", err)
	}
	resp6b.Body.Close()
	if resp6b.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for Bearer no-key-required, got %d", resp6b.StatusCode)
	}

	// 6c. Single model endpoint /v1/models/gemini-3-flash -> 200
	req6c, _ := http.NewRequest(http.MethodGet, defaultProxyServer.URL+"/v1/models/gemini-3-flash", nil)
	resp6c, err := defaultClient.Do(req6c)
	if err != nil {
		t.Fatalf("req6c failed: %v", err)
	}
	defer resp6c.Body.Close()
	if resp6c.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for /v1/models/gemini-3-flash, got %d", resp6c.StatusCode)
	}
	var modelDetail struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	if err := json.NewDecoder(resp6c.Body).Decode(&modelDetail); err != nil {
		t.Fatalf("failed to decode single model response: %v", err)
	}
	if modelDetail.ID != "gemini-3-flash" {
		t.Errorf("expected model id 'gemini-3-flash', got %q", modelDetail.ID)
	}
}

// Wrapper tests to match -run TestServer
func TestServer_OpenAI_ChatCompletions_NonStreaming(t *testing.T) {
	TestOpenAI_ChatCompletions_NonStreaming(t)
}

func TestServer_OpenAI_ChatCompletions_Streaming(t *testing.T) {
	TestOpenAI_ChatCompletions_Streaming(t)
}

func TestServer_OpenAI_ChatCompletions_ToolCalling(t *testing.T) {
	TestOpenAI_ChatCompletions_ToolCalling(t)
}

func TestServer_OpenAI_ChatCompletions_429Failover(t *testing.T) {
	TestOpenAI_ChatCompletions_429Failover(t)
}

func TestServer_Anthropic_Messages_Streaming(t *testing.T) {
	TestAnthropic_Messages_Streaming(t)
}

func TestServer_Anthropic_Messages_NonStreaming(t *testing.T) {
	TestAnthropic_Messages_NonStreaming(t)
}

func TestServer_Models_And_Health_Endpoints(t *testing.T) {
	TestModels_And_Health_Endpoints(t)
}

func TestServer_Auth_Validation(t *testing.T) {
	TestAuth_Validation(t)
}
