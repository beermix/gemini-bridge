package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"gemini-bridge/internal/google"
)

func TestFormatOpenAI_LeakedToolCallRecovery(t *testing.T) {
	// Non-streaming test
	t.Run("Non-streaming leaked tool recovery", func(t *testing.T) {
		resp := &google.GeminiResponse{
			Candidates: []google.Candidate{
				{
					Content: google.GeminiContent{
						Role: "model",
						Parts: []google.GeminiPart{
							{
								Text: `call:default_api:execute_command{"cmd":"ls -la"}`,
							},
						},
					},
					FinishReason: "STOP",
				},
			},
		}

		formatted := FormatOpenAIResponse("test-id", "gemini-3-flash", resp)
		if formatted == nil || len(formatted.Choices) == 0 {
			t.Fatal("expected formatted response with choices")
		}

		msg := formatted.Choices[0].Message
		if msg.Content != "" {
			t.Errorf("expected empty message content, got %q", msg.Content)
		}
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		if msg.ToolCalls[0].Function.Name != "execute_command" {
			t.Errorf("expected tool name execute_command, got %q", msg.ToolCalls[0].Function.Name)
		}
		if msg.ToolCalls[0].Function.Arguments != `{"cmd":"ls -la"}` {
			t.Errorf("expected arguments, got %q", msg.ToolCalls[0].Function.Arguments)
		}
	})

	// Streaming chunk test
	t.Run("Streaming chunk leaked tool recovery", func(t *testing.T) {
		cand := &google.Candidate{
			Content: google.GeminiContent{
				Parts: []google.GeminiPart{
					{
						Text: `call:default_api:fetch_url{url:"https://example.com"}`,
					},
				},
			},
		}

		chunkBytes := FormatOpenAIChunk("stream-id", "gemini-3-flash", cand, nil)
		if len(chunkBytes) == 0 {
			t.Fatal("expected non-empty chunk bytes")
		}

		chunkStr := string(chunkBytes)
		if !strings.Contains(chunkStr, "fetch_url") {
			t.Errorf("expected chunk to contain tool name fetch_url, got %s", chunkStr)
		}
		if strings.Contains(chunkStr, `\"content\":\"call:default_api`) {
			t.Errorf("expected tool not to be in content, got %s", chunkStr)
		}
	})
}

func TestFormatAnthropic_LeakedToolAndEmptyRecovery(t *testing.T) {
	// Leaked tool recovery in non-streaming
	t.Run("Anthropic non-streaming leaked tool recovery", func(t *testing.T) {
		resp := &google.GeminiResponse{
			Candidates: []google.Candidate{
				{
					Content: google.GeminiContent{
						Role: "model",
						Parts: []google.GeminiPart{
							{
								Text: `call:default_api:read_file{"path":"/root/test.txt"}`,
							},
						},
					},
					FinishReason: "STOP",
				},
			},
		}

		formatted := FormatAnthropicResponse("msg-1", "claude-3-7-sonnet", resp)
		if formatted == nil {
			t.Fatal("expected formatted anthropic response")
		}
		if len(formatted.Content) != 1 {
			t.Fatalf("expected 1 content block, got %d", len(formatted.Content))
		}
		if formatted.Content[0].Type != "tool_use" {
			t.Errorf("expected tool_use block, got %s", formatted.Content[0].Type)
		}
		if formatted.Content[0].Name != "read_file" {
			t.Errorf("expected tool name read_file, got %s", formatted.Content[0].Name)
		}
	})

	// Empty response recovery fallback to "."
	t.Run("Anthropic empty response fallback", func(t *testing.T) {
		resp := &google.GeminiResponse{
			Candidates: []google.Candidate{
				{
					Content: google.GeminiContent{
						Role:  "model",
						Parts: []google.GeminiPart{},
					},
					FinishReason: "STOP",
				},
			},
		}

		formatted := FormatAnthropicResponse("msg-2", "claude-3-7-sonnet", resp)
		if formatted == nil {
			t.Fatal("expected formatted response")
		}
		if len(formatted.Content) != 1 {
			t.Fatalf("expected 1 fallback content block, got %d", len(formatted.Content))
		}
		if formatted.Content[0].Type != "text" || formatted.Content[0].Text != "." {
			t.Errorf("expected text '.' block, got %+v", formatted.Content[0])
		}
	})
}

func TestReadFirstSSEPayload(t *testing.T) {
	rawSSE := ": keepalive ping\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}]}}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" world\"}]}}]}\n\n"
	reader := bufio.NewReader(strings.NewReader(rawSSE))

	payload, err := readFirstSSEPayload(context.Background(), reader)
	if err != nil {
		t.Fatalf("readFirstSSEPayload failed: %v", err)
	}
	if !bytes.Contains(payload, []byte("Hello")) {
		t.Errorf("expected first payload to contain Hello, got %s", string(payload))
	}
	if bytes.Contains(payload, []byte("world")) {
		t.Errorf("first payload should not contain subsequent chunks, got %s", string(payload))
	}

	// Test context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	emptyReader := bufio.NewReader(strings.NewReader(""))
	_, cancelErr := readFirstSSEPayload(ctx, emptyReader)
	if !errors.Is(cancelErr, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", cancelErr)
	}
}

func TestReadSSEDataWithKeepalive(t *testing.T) {
	pipeR, pipeW := strings.Builder{}, &bytes.Buffer{}
	_ = pipeR
	_ = pipeW

	// Test idle keepalive invocation
	pr, pw := createMockPipe()
	defer pr.Close()
	defer pw.Close()

	var idlePings int
	var idleMu sync.Mutex
	onIdle := func() {
		idleMu.Lock()
		idlePings++
		idleMu.Unlock()
	}

	var receivedChunks []string
	var chunkMu sync.Mutex
	onData := func(payload []byte) error {
		chunkMu.Lock()
		receivedChunks = append(receivedChunks, string(payload))
		chunkMu.Unlock()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader := bufio.NewReader(pr)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- readSSEDataWithKeepalive(ctx, reader, 50*time.Millisecond, onIdle, onData)
	}()

	// Wait 120ms without sending data -> should trigger at least 1-2 idle pings
	time.Sleep(120 * time.Millisecond)

	idleMu.Lock()
	pings := idlePings
	idleMu.Unlock()

	if pings < 1 {
		t.Errorf("expected at least 1 idle ping, got %d", pings)
	}

	// Now send a real data chunk
	_, _ = pw.Write([]byte("data: {\"chunk\": 1}\n\n"))
	time.Sleep(30 * time.Millisecond)

	chunkMu.Lock()
	count := len(receivedChunks)
	chunkMu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 received chunk, got %d", count)
	}

	// Close write end to terminate
	_ = pw.Close()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Errorf("readSSEDataWithKeepalive returned unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for readSSEDataWithKeepalive to finish")
	}
}

func TestThoughtSignatureRecoveryHelpers(t *testing.T) {
	// Test IsInvalidThoughtSignatureError
	err1 := &google.UpstreamError{
		StatusCode: 400,
		Body:       `{"error":{"code":400,"message":"Invalid thought signature provided"}}`,
	}
	if !IsInvalidThoughtSignatureError(err1) {
		t.Errorf("expected IsInvalidThoughtSignatureError to return true for %v", err1)
	}

	err2 := errors.New("upstream error (status 400): invalid thought_signature")
	if !IsInvalidThoughtSignatureError(err2) {
		t.Errorf("expected IsInvalidThoughtSignatureError to return true for %v", err2)
	}

	err3 := errors.New("upstream error (status 500): internal error")
	if IsInvalidThoughtSignatureError(err3) {
		t.Errorf("expected IsInvalidThoughtSignatureError to return false for %v", err3)
	}

	// Test StripThoughtSignatures
	req := &google.GeminiInternalRequest{
		Request: google.GeminiRequest{
			Contents: []google.GeminiContent{
				{
					Role: "model",
					Parts: []google.GeminiPart{
						{
							Text:             "thinking...",
							Thought:          true,
							ThoughtSignature: "sig-12345",
						},
						{
							Text:             "content",
							ThoughtSignature: "sig-67890",
						},
					},
				},
			},
		},
	}

	StripThoughtSignatures(req)

	for _, c := range req.Request.Contents {
		for _, p := range c.Parts {
			if p.ThoughtSignature != "" {
				t.Errorf("expected empty ThoughtSignature, got %q", p.ThoughtSignature)
			}
		}
	}
}

type mockPipe struct {
	buf bytes.Buffer
	mu  sync.Mutex
	c   sync.Cond
	cls bool
}

func createMockPipe() (*mockPipeReader, *mockPipeWriter) {
	p := &mockPipe{}
	p.c.L = &p.mu
	return &mockPipeReader{p: p}, &mockPipeWriter{p: p}
}

type mockPipeReader struct{ p *mockPipe }

func (r *mockPipeReader) Read(b []byte) (n int, err error) {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	for r.p.buf.Len() == 0 && !r.p.cls {
		r.p.c.Wait()
	}
	if r.p.buf.Len() > 0 {
		n, err = r.p.buf.Read(b)
		return n, err
	}
	if r.p.cls {
		return 0, io.EOF
	}
	return 0, nil
}

func (r *mockPipeReader) Close() error {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	r.p.cls = true
	r.p.c.Broadcast()
	return nil
}

type mockPipeWriter struct{ p *mockPipe }

func (w *mockPipeWriter) Write(b []byte) (n int, err error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	n, err = w.p.buf.Write(b)
	w.p.c.Broadcast()
	return n, err
}

func (w *mockPipeWriter) Close() error {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	w.p.cls = true
	w.p.c.Broadcast()
	return nil
}
