package google

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gemini-bridge/internal/account"
)

func TestClient_HeadersAndAuth(t *testing.T) {
	var receivedAuth string
	var receivedContentType string
	var receivedUserAgent string
	var receivedProject string
	var receivedPath string
	var receivedRawQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedContentType = r.Header.Get("Content-Type")
		receivedUserAgent = r.Header.Get("User-Agent")
		receivedProject = r.Header.Get("x-goog-user-project")
		receivedPath = r.URL.Path
		receivedRawQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]}}]}`))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(server.URL, server.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{
			AccessToken: "test-access-token-123",
			ProjectID:   "my-test-project",
		},
	}

	body := &GeminiInternalRequest{
		Model: "gemini-2.5-flash",
		Request: GeminiRequest{
			Contents: []GeminiContent{
				{
					Role: "user",
					Parts: []GeminiPart{
						{Text: "ping"},
					},
				},
			},
		},
	}

	// 1. Test GenerateContent (non-streaming)
	resp, err := client.GenerateContent(context.Background(), acc, body)
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}
	if resp == nil || len(resp.Candidates) == 0 {
		t.Fatalf("expected candidates in response, got %v", resp)
	}

	if receivedAuth != "Bearer test-access-token-123" {
		t.Errorf("expected Authorization Bearer test-access-token-123, got %q", receivedAuth)
	}
	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedContentType)
	}
	if receivedUserAgent != DefaultUserAgent {
		t.Errorf("expected default User-Agent %q, got %q", DefaultUserAgent, receivedUserAgent)
	}
	if receivedProject != "my-test-project" {
		t.Errorf("expected x-goog-user-project my-test-project, got %q", receivedProject)
	}
	if !strings.HasSuffix(receivedPath, ":generateContent") {
		t.Errorf("expected path to end with :generateContent, got %q", receivedPath)
	}

	// 2. Test StreamGenerateContent with custom User-Agent and empty ProjectID
	accNoProject := &account.CloudAccount{
		Email: "test2@example.com",
		Token: account.CloudToken{
			AccessToken: "test-access-token-456",
		},
	}
	bodyCustomUA := &GeminiInternalRequest{
		UserAgent: "custom-agent/2.0",
		Model:     "gemini-2.5-flash",
		Request: GeminiRequest{
			Contents: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "stream ping"}}},
			},
		},
	}

	stream, err := client.StreamGenerateContent(context.Background(), accNoProject, bodyCustomUA)
	if err != nil {
		t.Fatalf("StreamGenerateContent failed: %v", err)
	}
	defer stream.Close()

	if receivedAuth != "Bearer test-access-token-456" {
		t.Errorf("expected Authorization Bearer test-access-token-456, got %q", receivedAuth)
	}
	if receivedUserAgent != "custom-agent/2.0" {
		t.Errorf("expected custom User-Agent custom-agent/2.0, got %q", receivedUserAgent)
	}
	if receivedProject != "" {
		t.Errorf("expected empty x-goog-user-project, got %q", receivedProject)
	}
	if !strings.HasSuffix(receivedPath, ":streamGenerateContent") {
		t.Errorf("expected path to end with :streamGenerateContent, got %q", receivedPath)
	}
	if receivedRawQuery != "alt=sse" {
		t.Errorf("expected raw query alt=sse, got %q", receivedRawQuery)
	}
}

func TestClient_StreamGenerateContent_Success(t *testing.T) {
	ssePayload := "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"chunk 1\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"chunk 2\"}]}}]}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(server.URL, server.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	body := &GeminiInternalRequest{
		Model: "gemini-2.5-flash",
		Request: GeminiRequest{
			Contents: []GeminiContent{
				{Role: "user", Parts: []GeminiPart{{Text: "stream me"}}},
			},
		},
	}

	stream, err := client.StreamGenerateContent(context.Background(), acc, body)
	if err != nil {
		t.Fatalf("StreamGenerateContent failed: %v", err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}
	if string(data) != ssePayload {
		t.Errorf("expected stream data %q, got %q", ssePayload, string(data))
	}
}

func TestClient_GenerateContent_Success_WrappedEnvelope(t *testing.T) {
	wrappedJSON := `{
		"response": {
			"candidates": [
				{
					"content": {
						"role": "model",
						"parts": [
							{"text": "thinking...", "thought": true},
							{"text": "Hello, world!"}
						]
					},
					"finishReason": "STOP"
				}
			],
			"usageMetadata": {
				"promptTokenCount": 10,
				"candidatesTokenCount": 25,
				"totalTokenCount": 35
			},
			"responseId": "resp-wrapped-999"
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wrappedJSON))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(server.URL, server.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	if resp.ResponseId != "resp-wrapped-999" {
		t.Errorf("expected responseId resp-wrapped-999, got %q", resp.ResponseId)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(resp.Candidates))
	}
	cand := resp.Candidates[0]
	if cand.FinishReason != "STOP" {
		t.Errorf("expected finishReason STOP, got %q", cand.FinishReason)
	}
	if len(cand.Content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(cand.Content.Parts))
	}
	if !cand.Content.Parts[0].Thought || cand.Content.Parts[0].Text != "thinking..." {
		t.Errorf("unexpected part[0]: %+v", cand.Content.Parts[0])
	}
	if cand.Content.Parts[1].Thought || cand.Content.Parts[1].Text != "Hello, world!" {
		t.Errorf("unexpected part[1]: %+v", cand.Content.Parts[1])
	}
	if resp.UsageMetadata.TotalTokenCount != 35 {
		t.Errorf("expected TotalTokenCount 35, got %d", resp.UsageMetadata.TotalTokenCount)
	}
}

func TestClient_GenerateContent_Success_BareEnvelope(t *testing.T) {
	bareJSON := `{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [
						{
							"functionCall": {
								"name": "get_weather",
								"args": {"location": "London"}
							}
						}
					]
				},
				"finishReason": "TOOL_CALLS"
			}
		],
		"usageMetadata": {
			"promptTokenCount": 8,
			"candidatesTokenCount": 12,
			"totalTokenCount": 20
		},
		"responseId": "resp-bare-123"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bareJSON))
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(server.URL, server.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{Model: "gemini-2.5-pro"})
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}

	if resp.ResponseId != "resp-bare-123" {
		t.Errorf("expected responseId resp-bare-123, got %q", resp.ResponseId)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(resp.Candidates))
	}
	cand := resp.Candidates[0]
	if cand.FinishReason != "TOOL_CALLS" {
		t.Errorf("expected finishReason TOOL_CALLS, got %q", cand.FinishReason)
	}
	if len(cand.Content.Parts) != 1 || cand.Content.Parts[0].FunctionCall == nil {
		t.Fatalf("expected function call part, got %+v", cand.Content.Parts)
	}
	fnCall := cand.Content.Parts[0].FunctionCall
	if fnCall.Name != "get_weather" {
		t.Errorf("expected get_weather, got %q", fnCall.Name)
	}
	if fnCall.Args["location"] != "London" {
		t.Errorf("expected location London, got %v", fnCall.Args["location"])
	}
}

func TestClient_StreamGenerateContent_UpstreamError_429(t *testing.T) {
	primaryCalls := int32(0)
	fallbackCalls := int32(0)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded for project"}}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded for project"}}`))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	stream, err := client.StreamGenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err == nil {
		if stream != nil {
			stream.Close()
		}
		t.Fatalf("expected error on 429, got nil")
	}

	var upErr *UpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("expected UpstreamError, got %T: %v", err, err)
	}
	if upErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", upErr.StatusCode)
	}
	if !strings.Contains(upErr.Body, "Quota exceeded") {
		t.Errorf("expected error body to contain Quota exceeded, got %q", upErr.Body)
	}

	if atomic.LoadInt32(&primaryCalls) != 1 {
		t.Errorf("expected 1 primary call, got %d", primaryCalls)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("expected 1 fallback call on 429, got %d", fallbackCalls)
	}
}

func TestClient_GenerateContent_UpstreamError_429(t *testing.T) {
	primaryCalls := int32(0)
	fallbackCalls := int32(0)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Rate limit exceeded"}}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Rate limit exceeded"}}`))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err == nil {
		t.Fatalf("expected error on 429, got response: %v", resp)
	}

	var upErr *UpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("expected UpstreamError, got %T: %v", err, err)
	}
	if upErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", upErr.StatusCode)
	}
	if !strings.Contains(upErr.Body, "Rate limit exceeded") {
		t.Errorf("expected error body to contain Rate limit exceeded, got %q", upErr.Body)
	}

	if atomic.LoadInt32(&primaryCalls) != 1 {
		t.Errorf("expected 1 primary call, got %d", primaryCalls)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("expected 1 fallback call on 429, got %d", fallbackCalls)
	}
}

func TestClient_Fallback_On500_GenerateContent(t *testing.T) {
	primaryCalls := int32(0)
	fallbackCalls := int32(0)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"fallback success"}]}}]}`))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err != nil {
		t.Fatalf("expected success via fallback, got error: %v", err)
	}

	if len(resp.Candidates) != 1 || resp.Candidates[0].Content.Parts[0].Text != "fallback success" {
		t.Errorf("unexpected fallback response: %+v", resp)
	}
	if atomic.LoadInt32(&primaryCalls) != 1 {
		t.Errorf("expected 1 primary call, got %d", primaryCalls)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("expected 1 fallback call, got %d", fallbackCalls)
	}
}

func TestClient_Fallback_On500_StreamGenerateContent(t *testing.T) {
	primaryCalls := int32(0)
	fallbackCalls := int32(0)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"stream fallback"}]}}]}` + "\n\n"))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	stream, err := client.StreamGenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err != nil {
		t.Fatalf("expected stream success via fallback, got error: %v", err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}
	if !strings.Contains(string(data), "stream fallback") {
		t.Errorf("expected stream fallback data, got %q", string(data))
	}
	if atomic.LoadInt32(&primaryCalls) != 1 {
		t.Errorf("expected 1 primary call, got %d", primaryCalls)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("expected 1 fallback call, got %d", fallbackCalls)
	}
}

func TestClient_Fallback_OnConnectionError(t *testing.T) {
	// Primary server listener is closed immediately to cause connection refused
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	primaryURL := primary.URL
	primary.Close() // closed server will produce connection error

	fallbackCalls := int32(0)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"conn failover ok"}]}}]}`))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primaryURL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err != nil {
		t.Fatalf("expected success via fallback after connection error, got %v", err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content.Parts[0].Text != "conn failover ok" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if atomic.LoadInt32(&fallbackCalls) != 1 {
		t.Errorf("expected 1 fallback call, got %d", fallbackCalls)
	}
}

func TestClient_Fallback_BothFail(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`primary error 500`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`fallback error 503`))
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token-abc"},
	}

	_, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err == nil {
		t.Fatalf("expected error when both fail, got nil")
	}

	var upErr *UpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("expected UpstreamError, got %T: %v", err, err)
	}
	if upErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected fallback status 503, got %d", upErr.StatusCode)
	}
	if !strings.Contains(upErr.Body, "fallback error 503") {
		t.Errorf("expected fallback body in error, got %q", upErr.Body)
	}
}

func TestClient_ProxySupport(t *testing.T) {
	proxyHit := int32(0)
	targetHit := int32(0)

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHit, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"through proxy"}]}}]}`))
	}))
	defer targetServer.Close()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyHit, 1)
		targetURL, _ := url.Parse(targetServer.URL)
		req, _ := http.NewRequest(r.Method, targetURL.String()+r.URL.Path, r.Body)
		for k, vv := range r.Header {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(targetServer.URL, targetServer.URL)

	acc := &account.CloudAccount{
		Email:    "test@example.com",
		ProxyURL: proxyServer.URL,
		Token: account.CloudToken{
			AccessToken: "proxy-token",
		},
	}

	resp, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err != nil {
		t.Fatalf("GenerateContent through proxy failed: %v", err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Content.Parts[0].Text != "through proxy" {
		t.Errorf("unexpected response through proxy: %+v", resp)
	}
	if atomic.LoadInt32(&proxyHit) != 1 {
		t.Errorf("expected 1 proxy hit, got %d", proxyHit)
	}
	if atomic.LoadInt32(&targetHit) != 1 {
		t.Errorf("expected 1 target hit, got %d", targetHit)
	}
}

func TestClient_InputValidation(t *testing.T) {
	client := NewClient(5 * time.Second)

	// nil account
	_, err := client.GenerateContent(context.Background(), nil, &GeminiInternalRequest{})
	if err == nil {
		t.Error("expected error with nil account")
	}

	// empty token
	_, err = client.GenerateContent(context.Background(), &account.CloudAccount{}, &GeminiInternalRequest{})
	if err == nil {
		t.Error("expected error with empty token")
	}

	// nil body
	acc := &account.CloudAccount{Token: account.CloudToken{AccessToken: "token"}}
	_, err = client.GenerateContent(context.Background(), acc, nil)
	if err == nil {
		t.Error("expected error with nil body")
	}

	// nil account for streaming
	_, err = client.StreamGenerateContent(context.Background(), nil, &GeminiInternalRequest{})
	if err == nil {
		t.Error("expected error with nil account for stream")
	}
}

func TestClient_GettersAndCloseIdleConnections(t *testing.T) {
	client := NewClient(10 * time.Second)
	if client.PrimaryBaseURL() != DefaultPrimaryBaseURL {
		t.Errorf("expected default primary URL, got %q", client.PrimaryBaseURL())
	}
	if client.FallbackBaseURL() != DefaultFallbackBaseURL {
		t.Errorf("expected default fallback URL, got %q", client.FallbackBaseURL())
	}

	client.SetBaseURLs("http://p.com", "http://f.com")
	if client.PrimaryBaseURL() != "http://p.com" {
		t.Errorf("expected updated primary URL, got %q", client.PrimaryBaseURL())
	}
	if client.FallbackBaseURL() != "http://f.com" {
		t.Errorf("expected updated fallback URL, got %q", client.FallbackBaseURL())
	}

	// Should not panic
	client.CloseIdleConnections()
}

func TestClient_InvalidProxyURL(t *testing.T) {
	client := NewClient(5 * time.Second)
	acc := &account.CloudAccount{
		Email:    "test@example.com",
		ProxyURL: "http://[::1]:namedport", // invalid URL
		Token:    account.CloudToken{AccessToken: "token"},
	}

	_, err := client.GenerateContent(context.Background(), acc, &GeminiInternalRequest{})
	if err == nil {
		t.Error("expected error with invalid proxy URL")
	}
}

func TestClient_ContextCancellation_NoFallback(t *testing.T) {
	primaryCalls := int32(0)
	fallbackCalls := int32(0)

	ctx, cancel := context.WithCancel(context.Background())
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryCalls, 1)
		cancel() // cancel context while request is in flight
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fallbackCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer fallback.Close()

	client := NewClient(5 * time.Second)
	client.SetBaseURLs(primary.URL, fallback.URL)

	acc := &account.CloudAccount{
		Email: "test@example.com",
		Token: account.CloudToken{AccessToken: "token"},
	}

	_, err := client.GenerateContent(ctx, acc, &GeminiInternalRequest{})
	if err == nil {
		t.Fatal("expected error on canceled context")
	}

	if atomic.LoadInt32(&fallbackCalls) != 0 {
		t.Errorf("fallback should NOT be called when context is canceled, got %d", fallbackCalls)
	}
}

func TestParseGeminiResponse_InvalidJSON(t *testing.T) {
	_, err := ParseGeminiResponse([]byte("not valid json"))
	if err == nil {
		t.Error("expected error parsing invalid JSON")
	}
}
