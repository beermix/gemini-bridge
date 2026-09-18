package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"gemini-bridge/internal/google"
)

// handleAnthropicCountTokens counts tokens for an incoming AnthropicMessagesRequest.
func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		s.respondAnthropicError(w, r, startTime, "", "", http.StatusBadRequest, "failed to read body: "+err.Error(), "")
		return
	}

	var req AnthropicMessagesRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.respondAnthropicError(w, r, startTime, "", "", http.StatusBadRequest, "invalid request json: "+err.Error(), "")
		return
	}

	tokens := countAnthropicTokens(&req)

	s.recordRequest(RequestLogEntry{
		Timestamp:   startTime,
		Method:      r.Method,
		Path:        r.URL.Path,
		Model:       req.Model,
		TargetModel: ResolveModel(req.Model),
		Status:      http.StatusOK,
		Duration:    time.Since(startTime),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]int{
		"input_tokens": tokens,
	})
}

// handleAnthropicMessages handles /v1/messages (streaming & non-streaming).
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		s.respondAnthropicError(w, r, startTime, "", "", http.StatusBadRequest, "failed to read request body: "+err.Error(), "")
		return
	}

	var req AnthropicMessagesRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.respondAnthropicError(w, r, startTime, "", "", http.StatusBadRequest, "invalid request json: "+err.Error(), "")
		return
	}

	if s.pool == nil {
		s.respondAnthropicError(w, r, startTime, req.Model, "", http.StatusServiceUnavailable, "account pool is not initialized", "")
		return
	}
	if s.client == nil {
		s.respondAnthropicError(w, r, startTime, req.Model, "", http.StatusServiceUnavailable, "google client is not initialized", "")
		return
	}

	targetModel := ResolveModel(req.Model)
	const maxAttempts = 3

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc, leaseErr := s.pool.LeaseAccount(targetModel)
		if leaseErr != nil {
			s.respondAnthropicError(w, r, startTime, req.Model, targetModel, http.StatusServiceUnavailable, "account lease failed: "+leaseErr.Error(), "")
			return
		}

		geminiReq := MapAnthropicToGemini(&req, acc.Token.ProjectID)

		if !req.Stream {
			// Non-streaming messages
			resp, ep, genErr := s.client.GenerateContentWithEndpoint(r.Context(), acc, geminiReq)
			if genErr != nil && IsInvalidThoughtSignatureError(genErr) {
				ClearThoughtSignatures()
				StripThoughtSignatures(geminiReq)
				resp, ep, genErr = s.client.GenerateContentWithEndpoint(r.Context(), acc, geminiReq)
			}
			if genErr != nil {
				var upErr *google.UpstreamError
				if errors.As(genErr, &upErr) && upErr.StatusCode == http.StatusTooManyRequests {
					s.recordRateLimit()
					cooldownID := acc.ID
					if cooldownID == "" {
						cooldownID = acc.Email
					}
					s.pool.MarkCooldown(cooldownID, 60*time.Second)
					if attempt < maxAttempts-1 {
						continue
					}
				}

				status := http.StatusInternalServerError
				if errors.As(genErr, &upErr) && upErr.StatusCode > 0 {
					status = upErr.StatusCode
				}
				s.respondAnthropicError(w, r, startTime, req.Model, targetModel, status, genErr.Error(), acc.Email, ep)
				return
			}

			// Check if non-streaming response has visible assistant content (retry if empty / thought-only)
			hasVisibleContent := false
			if resp != nil {
				for _, cand := range resp.Candidates {
					for _, p := range cand.Content.Parts {
						if p.FunctionCall != nil {
							hasVisibleContent = true
							break
						}
						if !p.Thought && strings.TrimSpace(p.Text) != "" {
							var pb PlanningBuffer
							clean, isLeak := pb.Consume(p.Text, true)
							if !isLeak && strings.TrimSpace(clean) != "" {
								hasVisibleContent = true
								break
							}
						}
					}
					if hasVisibleContent {
						break
					}
				}
			}
			if !hasVisibleContent && attempt < maxAttempts-1 {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			// Success
			msgID := "msg_" + generateID()
			anthropicResp := FormatAnthropicResponse(msgID, req.Model, resp)

			s.recordRequest(RequestLogEntry{
				Timestamp:    startTime,
				Method:       r.Method,
				Path:         r.URL.Path,
				Model:        req.Model,
				TargetModel:  targetModel,
				Status:       http.StatusOK,
				Duration:     time.Since(startTime),
				AccountEmail: acc.Email,
				Endpoint:     ep,
			})

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(anthropicResp)
			return
		}

		// Streaming messages
		stream, ep, streamErr := s.client.StreamGenerateContentWithEndpoint(r.Context(), acc, geminiReq)
		if streamErr != nil && IsInvalidThoughtSignatureError(streamErr) {
			ClearThoughtSignatures()
			StripThoughtSignatures(geminiReq)
			stream, ep, streamErr = s.client.StreamGenerateContentWithEndpoint(r.Context(), acc, geminiReq)
		}
		if streamErr != nil {
			var upErr *google.UpstreamError
			if errors.As(streamErr, &upErr) && upErr.StatusCode == http.StatusTooManyRequests {
				s.recordRateLimit()
				cooldownID := acc.ID
				if cooldownID == "" {
					cooldownID = acc.Email
				}
				s.pool.MarkCooldown(cooldownID, 60*time.Second)
				if attempt < maxAttempts-1 {
					continue
				}
			}

			status := http.StatusInternalServerError
			if errors.As(streamErr, &upErr) && upErr.StatusCode > 0 {
				status = upErr.StatusCode
			}
			s.respondAnthropicError(w, r, startTime, req.Model, targetModel, status, streamErr.Error(), acc.Email, ep)
			return
		}

		// First-event buffering before committing headers (with 45s TTFT watchdog)
		firstEventCtx, firstEventCancel := context.WithTimeout(r.Context(), 45*time.Second)
		reader := bufio.NewReader(stream)
		firstPayload, firstErr := readFirstSSEPayload(firstEventCtx, reader)
		firstEventCancel()
		if firstErr != nil {
			stream.Close()
			if attempt < maxAttempts-1 {
				cooldownID := acc.ID
				if cooldownID == "" {
					cooldownID = acc.Email
				}
				s.pool.MarkCooldown(cooldownID, 30*time.Second)
				continue
			}
			s.respondAnthropicError(w, r, startTime, req.Model, targetModel, http.StatusBadGateway, "failed to receive initial stream event: "+firstErr.Error(), acc.Email, ep)
			return
		}

		// Check if firstPayload is an invalid thought signature error
		if bytes.Contains(bytes.ToLower(firstPayload), []byte("thought signature")) || bytes.Contains(bytes.ToLower(firstPayload), []byte("thought_signature")) {
			stream.Close()
			ClearThoughtSignatures()
			StripThoughtSignatures(geminiReq)
			if attempt < maxAttempts-1 {
				continue
			}
		}

		// Check if stream finished prematurely on first event without visible content
		if initResp, _ := ParseGeminiChunk(firstPayload); initResp != nil && len(initResp.Candidates) > 0 {
			cand := initResp.Candidates[0]
			if cand.FinishReason != "" {
				hasVisible := false
				for _, p := range cand.Content.Parts {
					if p.FunctionCall != nil {
						hasVisible = true
						break
					}
					if !p.Thought && strings.TrimSpace(p.Text) != "" {
						var pb PlanningBuffer
						clean, isLeak := pb.Consume(p.Text, true)
						if !isLeak && strings.TrimSpace(clean) != "" {
							hasVisible = true
							break
						}
					}
				}
				if !hasVisible && attempt < maxAttempts-1 {
					stream.Close()
					time.Sleep(200 * time.Millisecond)
					continue
				}
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			stream.Close()
			s.respondAnthropicError(w, r, startTime, req.Model, targetModel, http.StatusInternalServerError, "streaming is not supported by client", acc.Email)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		msgID := "msg_" + generateID()
		var state AnthropicStreamState
		var planBuf PlanningBuffer

		processPayload := func(payload []byte) error {
			geminiResp, err := ParseGeminiChunk(payload)
			if err != nil || geminiResp == nil {
				return nil
			}

			if geminiResp.UsageMetadata.PromptTokenCount > 0 {
				promptTokens := geminiResp.UsageMetadata.PromptTokenCount
				cachedTokens := geminiResp.UsageMetadata.CachedContentTokenCount
				if cachedTokens > 0 && promptTokens >= cachedTokens {
					promptTokens -= cachedTokens
				}
				state.InputTokens = promptTokens
				state.CachedTokens = cachedTokens
			}
			if geminiResp.UsageMetadata.CandidatesTokenCount > 0 {
				state.OutputTokens = geminiResp.UsageMetadata.CandidatesTokenCount
			}
			if geminiResp.UsageMetadata.ThoughtsTokenCount > 0 {
				state.ThinkingTokens = geminiResp.UsageMetadata.ThoughtsTokenCount
			}

			if len(geminiResp.Candidates) > 0 {
				for _, cand := range geminiResp.Candidates {
					var filteredParts []google.GeminiPart
					for _, part := range cand.Content.Parts {
						if part.FunctionCall != nil || part.Thought {
							filteredParts = append(filteredParts, part)
						} else if part.Text != "" {
							clean, _ := planBuf.Consume(part.Text, cand.FinishReason != "")
							if clean != "" {
								part.Text = clean
								filteredParts = append(filteredParts, part)
							}
						}
					}
					cand.Content.Parts = filteredParts

					events := FormatAnthropicEvents(msgID, req.Model, &cand, &state)
					for _, ev := range events {
						_, _ = w.Write(ev)
					}
				}
				flusher.Flush()
			} else if !state.MessageStarted {
				events := FormatAnthropicEvents(msgID, req.Model, nil, &state)
				for _, ev := range events {
					_, _ = w.Write(ev)
				}
				flusher.Flush()
			}
			return nil
		}

		// Process initial buffered payload
		_ = processPayload(firstPayload)

		// Read subsequent payloads with 10s keepalive pings
		_ = readSSEDataWithKeepalive(r.Context(), reader, 10*time.Second, func() {
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}, processPayload)

		stream.Close()

		// Graceful finalization if closed without finish reason
		if !state.MessageStopped {
			if state.BlockOpen {
				state.BlockOpen = false
				_, _ = w.Write(formatAnthropicSSE("content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.CurrentBlockIdx,
				}))
			}
			stopReason := "end_turn"
			if state.ToolUseEmitted {
				stopReason = "tool_use"
			}
			usageMap := map[string]interface{}{
				"output_tokens": state.OutputTokens,
			}
			if state.ThinkingTokens > 0 {
				usageMap["output_tokens_details"] = map[string]interface{}{
					"thinking_tokens": state.ThinkingTokens,
				}
			}
			_, _ = w.Write(formatAnthropicSSE("message_delta", map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason":   stopReason,
					"stop_sequence": nil,
				},
				"usage": usageMap,
			}))
			_, _ = w.Write(formatAnthropicSSE("message_stop", map[string]interface{}{
				"type": "message_stop",
			}))
			flusher.Flush()
		}

		s.recordRequest(RequestLogEntry{
			Timestamp:    startTime,
			Method:       r.Method,
			Path:         r.URL.Path,
			Model:        req.Model,
			TargetModel:  targetModel,
			Status:       http.StatusOK,
			Duration:     time.Since(startTime),
			AccountEmail: acc.Email,
			Endpoint:     ep,
		})
		return
	}
}

func (s *Server) respondAnthropicError(w http.ResponseWriter, r *http.Request, startTime time.Time, model, targetModel string, status int, msg, email string, endpoint ...string) {
	ep := ""
	if len(endpoint) > 0 {
		ep = endpoint[0]
	}
	s.recordRequest(RequestLogEntry{
		Timestamp:    startTime,
		Method:       r.Method,
		Path:         r.URL.Path,
		Model:        model,
		TargetModel:  targetModel,
		Status:       status,
		Duration:     time.Since(startTime),
		AccountEmail: email,
		Endpoint:     ep,
		Error:        msg,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(formatAnthropicError(status, msg))
}

func countAnthropicTokens(req *AnthropicMessagesRequest) int {
	if req == nil {
		return 1
	}
	totalChars := 0

	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			totalChars += len(s)
		case []AnthropicContentBlock:
			for _, b := range s {
				totalChars += len(b.Text)
			}
		}
	}

	for _, m := range req.Messages {
		switch c := m.Content.(type) {
		case string:
			totalChars += len(c)
		case []AnthropicContentBlock:
			for _, b := range c {
				totalChars += len(b.Text)
				totalChars += len(b.Thinking)
			}
		case []interface{}:
			for _, item := range c {
				if mp, ok := item.(map[string]interface{}); ok {
					if txt, ok := mp["text"].(string); ok {
						totalChars += len(txt)
					}
					if th, ok := mp["thinking"].(string); ok {
						totalChars += len(th)
					}
				}
			}
		}
	}

	for _, t := range req.Tools {
		totalChars += len(t.Name) + len(t.Description)
	}

	tokens := (totalChars + 3) / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
