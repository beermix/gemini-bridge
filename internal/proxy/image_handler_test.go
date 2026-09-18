package proxy_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gemini-bridge/internal/account"
	"gemini-bridge/internal/proxy"
)

func TestImageHandler_MappingHelpers(t *testing.T) {
	// 1. Aspect ratio mapping
	cases := []struct {
		size string
		want string
	}{
		{"1024x1024", "1:1"},
		{"512x512", "1:1"},
		{"1792x1024", "16:9"},
		{"1536x1024", "16:9"},
		{"1024x1792", "9:16"},
		{"1024x768", "4:3"},
		{"768x1024", "3:4"},
		{"16:9", "16:9"},
		{"", "1:1"},
	}

	for _, c := range cases {
		got := proxy.MapSizeToAspectRatio(c.size)
		if got != c.want {
			t.Errorf("MapSizeToAspectRatio(%q) = %q, want %q", c.size, got, c.want)
		}
	}

	// 2. Model mapping
	if proxy.ResolveImageModel("") != "gemini-3-pro-image" {
		t.Errorf("expected default model to be gemini-3-pro-image")
	}
	if proxy.ResolveImageModel("dall-e-3") != "gemini-3-pro-image" {
		t.Errorf("expected dall-e-3 to map to gemini-3-pro-image")
	}
	if proxy.ResolveImageModel("imagen-3.0-generate-002") != "imagen-3.0-generate-002" {
		t.Errorf("expected custom model to be preserved")
	}
}

func TestImageHandler_HTTP(t *testing.T) {
	// Mock upstream server that returns SSE stream with inlineData
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunk := `data: {"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]}}]}}` + "\n\n"
		_, _ = w.Write([]byte(chunk))
	})

	acc := makeDefaultAccount("acc-img", "img@test.com")
	srv, proxyHTTP, upstream := setupTestServer(t, upstreamHandler, []*account.CloudAccount{acc}, "")
	defer proxyHTTP.Close()
	defer upstream.Close()

	reqBody := proxy.OpenAIImageGenerationRequest{
		Prompt: "a red cute cat",
		Size:   "1024x1024",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(bodyBytes))

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp proxy.OpenAIImageGenerationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 image in response data, got %d", len(resp.Data))
	}
	if !strings.HasPrefix(resp.Data[0].B64JSON, "iVBORw0KGgo") {
		t.Errorf("expected b64_json to start with image data, got %q", resp.Data[0].B64JSON)
	}
	if resp.Data[0].RevisedPrompt != "a red cute cat" {
		t.Errorf("expected revised_prompt %q, got %q", "a red cute cat", resp.Data[0].RevisedPrompt)
	}
}
