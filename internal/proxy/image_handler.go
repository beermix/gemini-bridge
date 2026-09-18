package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"gemini-bridge/internal/google"
)

// OpenAIImageGenerationRequest represents the request payload for POST /v1/images/generations.
type OpenAIImageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	N              int    `json:"n,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Size           string `json:"size,omitempty"`
	Style          string `json:"style,omitempty"`
	User           string `json:"user,omitempty"`
}

// OpenAIImageGenerationResponse represents the response payload for POST /v1/images/generations.
type OpenAIImageGenerationResponse struct {
	Created int64             `json:"created"`
	Data    []OpenAIImageData `json:"data"`
}

// OpenAIImageData holds a single generated image item.
type OpenAIImageData struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

const defaultImageSystemInstruction = "You are an AI image generator. Generate images based on user descriptions. Focus on creating high-quality, visually appealing images that match the user's request."

func MapSizeToAspectRatio(size string) string {
	switch strings.TrimSpace(size) {
	case "1024x1024", "512x512", "256x256", "1:1":
		return "1:1"
	case "1792x1024", "1536x1024", "16:9":
		return "16:9"
	case "1024x1792", "1024x1536", "9:16":
		return "9:16"
	case "1024x768", "1152x864", "4:3":
		return "4:3"
	case "768x1024", "864x1152", "3:4":
		return "3:4"
	default:
		if strings.Contains(size, ":") {
			return size
		}
		return "1:1"
	}
}

func ResolveImageModel(model string) string {
	m := strings.TrimSpace(strings.ToLower(model))
	switch m {
	case "", "dall-e-3", "dall-e-2", "image", "imagen":
		return "gemini-3-pro-image"
	default:
		return model
	}
}

func (s *Server) handleOpenAIImageGenerations(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		s.respondOpenAIError(w, r, startTime, "", "", http.StatusBadRequest, "failed to read body: "+err.Error(), "")
		return
	}

	var req OpenAIImageGenerationRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		s.respondOpenAIError(w, r, startTime, "", "", http.StatusBadRequest, "invalid json body: "+err.Error(), "")
		return
	}

	if strings.TrimSpace(req.Prompt) == "" {
		s.respondOpenAIError(w, r, startTime, req.Model, "", http.StatusBadRequest, "prompt is required", "")
		return
	}

	targetModel := ResolveImageModel(req.Model)
	aspectRatio := MapSizeToAspectRatio(req.Size)

	const maxAttempts = 3

	for attempt := 0; attempt < maxAttempts; attempt++ {
		acc, leaseErr := s.pool.LeaseAccount(targetModel)
		if leaseErr != nil {
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, http.StatusServiceUnavailable, "account lease failed: "+leaseErr.Error(), "")
			return
		}

		reqID, sessID, labels := GenerateAntigravityRequestEnvelope(targetModel, req.Prompt)
		internalReq := &google.GeminiInternalRequest{
			Project:     acc.Token.ProjectID,
			Model:       targetModel,
			RequestID:   reqID,
			UserAgent:   "antigravity",
			RequestType: "agent",
			Request: google.GeminiRequest{
				SessionID: sessID,
				Labels:    labels,
				Contents: []google.GeminiContent{
					{
						Role: "user",
						Parts: []google.GeminiPart{
							{Text: req.Prompt},
						},
					},
				},
				SystemInstruction: &google.GeminiContent{
					Parts: []google.GeminiPart{
						{Text: defaultImageSystemInstruction},
					},
				},
				GenerationConfig: &google.GenerationConfig{
					ResponseModalities: []string{"IMAGE"},
					CandidateCount:     1,
					ImageConfig: &google.GeminiImageConfig{
						AspectRatio: aspectRatio,
					},
				},
				SafetySettings: []google.SafetySetting{
					{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_ONLY_HIGH"},
					{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_ONLY_HIGH"},
					{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_ONLY_HIGH"},
					{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_ONLY_HIGH"},
					{Category: "HARM_CATEGORY_CIVIC_INTEGRITY", Threshold: "BLOCK_ONLY_HIGH"},
				},
			},
		}

		stream, ep, streamErr := s.client.StreamGenerateContentWithEndpoint(r.Context(), acc, internalReq)
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
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, status, streamErr.Error(), acc.Email, ep)
			return
		}

		reader := bufio.NewReader(stream)
		var images []OpenAIImageData
		var textParts []string

		processPayload := func(payload []byte) error {
			geminiResp, parseErr := ParseGeminiChunk(payload)
			if parseErr != nil || geminiResp == nil {
				return nil
			}
			for _, cand := range geminiResp.Candidates {
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						textParts = append(textParts, part.Text)
					}
					if part.InlineData != nil && part.InlineData.Data != "" {
						images = append(images, OpenAIImageData{
							B64JSON:       part.InlineData.Data,
							RevisedPrompt: req.Prompt,
						})
					}
				}
			}
			return nil
		}

		_ = readSSEDataWithKeepalive(r.Context(), reader, 10*time.Second, nil, processPayload)
		stream.Close()

		if len(images) == 0 {
			if attempt < maxAttempts-1 {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			errMsg := "no image data returned from upstream"
			if len(textParts) > 0 {
				errMsg += ": " + strings.Join(textParts, " ")
			}
			s.respondOpenAIError(w, r, startTime, req.Model, targetModel, http.StatusBadGateway, errMsg, acc.Email, ep)
			return
		}

		respObj := OpenAIImageGenerationResponse{
			Created: time.Now().Unix(),
			Data:    images,
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(respObj)
		return
	}
}
