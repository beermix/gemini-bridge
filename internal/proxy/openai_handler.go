package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"gemini-bridge/internal/google"
)

type openAIModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type openAIModelListResponse struct {
	Object string            `json:"object"`
	Data   []openAIModelInfo `json:"data"`
}

var supportedOpenAIModels = []openAIModelInfo{
	{ID: "gemini-3-flash-medium", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3-flash", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3-flash-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3-flash-low", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.5-flash-lite", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.1-pro-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.5-flash-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.8-flash-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.8-flash-medium", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.8-flash-low", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.7-flash-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.7-flash-medium", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.7-flash-low", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.6-flash-high", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.6-flash-medium", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-3.6-flash-low", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "gemini-pro-agent", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "claude-opus-4-6-thinking", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "claude-sonnet-4-6-thinking", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "claude-sonnet-4-6", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "claude-3-7-sonnet", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "claude-3-5-sonnet", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "gpt-oss-120b-medium", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-4o", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-4o-mini", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-4", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-3.5-turbo", Object: "model", Created: 1700000000, OwnedBy: "openai"},
}

// handleOpenAIModels returns the list of supported models in OpenAI catalog format,
// or a single model if a model ID is specified in the path (e.g. /v1/models/gemini-3-flash).
func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models")
	modelID = strings.TrimPrefix(modelID, "/models")
	modelID = strings.Trim(modelID, "/")

	if modelID != "" {
		info := openAIModelInfo{
			ID:      modelID,
			Object:  "model",
			Created: 1700000000,
			OwnedBy: "google",
		}
		s.recordRequest(RequestLogEntry{
			Timestamp: startTime,
			Method:    r.Method,
			Path:      r.URL.Path,
			Model:     modelID,
			Status:    http.StatusOK,
			Duration:  time.Since(startTime),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
		return
	}

	resp := openAIModelListResponse{
		Object: "list",
		Data:   supportedOpenAIModels,
	}

	s.recordRequest(RequestLogEntry{
		Timestamp: startTime,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    http.StatusOK,
		Duration:  time.Since(startTime),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleOpenAIChatCompletions handles /v1/chat/completions (streaming & non-streaming).
func (s *Server) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		s.respondOpenAIError(w, r, startTime, "", "", http.StatusBadRequest, "failed to read request body: "+err.Error(), "")
		return
	}

	var req OpenAIChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.respondOpenAIError(w, r, startTime, "", "", http.StatusBadRequest, "invalid request json: "+err.Error(), "")
		return
	}

	if s.pool == nil {
		s.respondOpenAIError(w, r, startTime, req.Model, "", http.StatusServiceUnavailable, "account pool is not initialized", "")
		return
	}
	if s.client == nil {
		s.respondOpenAIError(w, r, startTime, req.Model, "", http.StatusServiceUnavailable, "google client is not initialized", "")
		return
	}

	targetModel := ResolveModel(req.Model)
	const maxAttempts = 3

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc, leaseErr := s.pool.LeaseAccount(targetModel)
		if leaseErr != nil {
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, http.StatusServiceUnavailable, "account lease failed: "+leaseErr.Error(), "")
			return
		}

		geminiReq := MapOpenAIToGemini(&req, acc.Token.ProjectID)

		if !req.Stream {
			// Non-streaming completion
			resp, genErr := s.client.GenerateContent(r.Context(), acc, geminiReq)
			if genErr != nil && IsInvalidThoughtSignatureError(genErr) {
				ClearThoughtSignatures()
				StripThoughtSignatures(geminiReq)
				resp, genErr = s.client.GenerateContent(r.Context(), acc, geminiReq)
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
				s.respondOpenAIError(w, r, startTime, req.Model, targetModel, status, genErr.Error(), acc.Email)
				return
			}

			// Success
			respID := "chatcmpl-" + generateID()
			openAIResp := FormatOpenAIResponse(respID, req.Model, resp)

			s.recordRequest(RequestLogEntry{
				Timestamp:    startTime,
				Method:       r.Method,
				Path:         r.URL.Path,
				Model:        req.Model,
				TargetModel:  targetModel,
				Status:       http.StatusOK,
				Duration:     time.Since(startTime),
				AccountEmail: acc.Email,
			})

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(openAIResp)
			return
		}

		// Streaming completion
		stream, streamErr := s.client.StreamGenerateContent(r.Context(), acc, geminiReq)
		if streamErr != nil && IsInvalidThoughtSignatureError(streamErr) {
			ClearThoughtSignatures()
			StripThoughtSignatures(geminiReq)
			stream, streamErr = s.client.StreamGenerateContent(r.Context(), acc, geminiReq)
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
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, status, streamErr.Error(), acc.Email)
			return
		}

		// First-event buffering before committing headers
		reader := bufio.NewReader(stream)
		firstPayload, firstErr := readFirstSSEPayload(r.Context(), reader)
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
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, http.StatusBadGateway, "failed to receive initial stream event: "+firstErr.Error(), acc.Email)
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

		flusher, ok := w.(http.Flusher)
		if !ok {
			stream.Close()
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, http.StatusInternalServerError, "streaming is not supported by client", acc.Email)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		streamID := "chatcmpl-" + generateID()

		processPayload := func(payload []byte) error {
			geminiResp, err := ParseGeminiChunk(payload)
			if err != nil || geminiResp == nil {
				return nil
			}

			if len(geminiResp.Candidates) > 0 {
				for i, cand := range geminiResp.Candidates {
					var usage *google.UsageMetadata
					if i == len(geminiResp.Candidates)-1 && geminiResp.UsageMetadata.TotalTokenCount > 0 {
						usage = &geminiResp.UsageMetadata
					}
					chunkBytes := FormatOpenAIChunk(streamID, req.Model, &cand, usage)
					if len(chunkBytes) > 0 {
						_, _ = w.Write(chunkBytes)
					}
				}
				flusher.Flush()
			} else if geminiResp.UsageMetadata.TotalTokenCount > 0 {
				chunkBytes := FormatOpenAIChunk(streamID, req.Model, nil, &geminiResp.UsageMetadata)
				if len(chunkBytes) > 0 {
					_, _ = w.Write(chunkBytes)
					flusher.Flush()
				}
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

		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()

		s.recordRequest(RequestLogEntry{
			Timestamp:    startTime,
			Method:       r.Method,
			Path:         r.URL.Path,
			Model:        req.Model,
			TargetModel:  targetModel,
			Status:       http.StatusOK,
			Duration:     time.Since(startTime),
			AccountEmail: acc.Email,
		})
		return
	}
}

func (s *Server) respondOpenAIError(w http.ResponseWriter, r *http.Request, startTime time.Time, model, targetModel string, status int, msg, email string) {
	s.recordRequest(RequestLogEntry{
		Timestamp:    startTime,
		Method:       r.Method,
		Path:         r.URL.Path,
		Model:        model,
		TargetModel:  targetModel,
		Status:       status,
		Duration:     time.Since(startTime),
		AccountEmail: email,
		Error:        msg,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(formatOpenAIError(status, msg))
}
