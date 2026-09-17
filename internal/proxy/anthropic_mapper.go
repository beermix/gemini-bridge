package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"gemini-bridge/internal/google"
)

// AnthropicMessagesRequest represents an incoming Anthropic Messages API request.
type AnthropicMessagesRequest struct {
	Model         string                  `json:"model"`
	Messages      []AnthropicMessage      `json:"messages"`
	System        interface{}             `json:"system,omitempty"` // string or []AnthropicContentBlock
	MaxTokens     int                     `json:"max_tokens"`
	Metadata      map[string]interface{}  `json:"metadata,omitempty"`
	StopSequences []string                `json:"stop_sequences,omitempty"`
	Stream        bool                    `json:"stream,omitempty"`
	Temperature   *float64                `json:"temperature,omitempty"`
	TopP          *float64                `json:"top_p,omitempty"`
	TopK          *int                    `json:"top_k,omitempty"`
	Tools         []AnthropicTool          `json:"tools,omitempty"`
	ToolChoice    interface{}              `json:"tool_choice,omitempty"`
	Thinking      *AnthropicThinkingConfig `json:"thinking,omitempty"`
	OutputConfig  *AnthropicOutputConfig   `json:"output_config,omitempty"`
}

// AnthropicOutputConfig specifies output configuration such as reasoning effort.
type AnthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

// AnthropicThinkingConfig specifies thinking/reasoning parameters for Claude models.
type AnthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// AnthropicMessage represents a message in Anthropic conversation history.
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []AnthropicContentBlock or []interface{}
}

// AnthropicContentBlock represents a content block (text, thinking, image, tool_use, or tool_result).
type AnthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Thinking  string                `json:"thinking,omitempty"`
	Signature string                `json:"signature,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     interface{}           `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   interface{}           `json:"content,omitempty"`
	IsError   bool                  `json:"is_error,omitempty"`
	Source    *AnthropicImageSource `json:"source,omitempty"`
}

// AnthropicImageSource holds base64 image data.
type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// AnthropicTool defines a tool available in Anthropic protocol.
type AnthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// AnthropicMessagesResponse represents the non-streaming response structure.
type AnthropicMessagesResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicOutputTokensDetails holds breakdown of generated completion tokens.
type AnthropicOutputTokensDetails struct {
	ThinkingTokens int `json:"thinking_tokens"`
}

// AnthropicUsage holds token counts.
type AnthropicUsage struct {
	InputTokens              int                           `json:"input_tokens"`
	OutputTokens             int                           `json:"output_tokens"`
	CacheCreationInputTokens int                           `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                           `json:"cache_read_input_tokens,omitempty"`
	OutputTokensDetails      *AnthropicOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// AnthropicStreamState maintains state across streaming chunks.
type AnthropicStreamState struct {
	MessageStarted   bool
	MessageStopped   bool
	CurrentBlockIdx  int
	CurrentBlockType string
	BlockOpen        bool
	InputTokens      int
	OutputTokens     int
	ThinkingTokens   int
	ToolUseEmitted   bool
}

// MapAnthropicToGemini translates an AnthropicMessagesRequest into a native GeminiInternalRequest.
func MapAnthropicToGemini(req *AnthropicMessagesRequest, projectID string) *google.GeminiInternalRequest {
	if req == nil {
		return nil
	}

	model := ResolveModel(req.Model)

	internalReq := &google.GeminiInternalRequest{
		Project:            projectID,
		Model:              model,
		UserAgent:          "antigravity",
		RequestType:        "agent",
		EnabledCreditTypes: []string{"CREDIT_TYPE_UNSPECIFIED"},
		Request:            google.GeminiRequest{},
	}

	// 1. Map System prompt
	var systemParts []google.GeminiPart
	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			if strings.TrimSpace(s) != "" {
				systemParts = append(systemParts, google.GeminiPart{Text: s})
			}
		case []AnthropicContentBlock:
			for _, b := range s {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					systemParts = append(systemParts, google.GeminiPart{Text: b.Text})
				}
			}
		case []interface{}:
			for _, item := range s {
				if m, ok := item.(map[string]interface{}); ok {
					if txt, ok := m["text"].(string); ok && strings.TrimSpace(txt) != "" {
						systemParts = append(systemParts, google.GeminiPart{Text: txt})
					}
				}
			}
		}
	}
	if len(systemParts) > 0 {
		internalReq.Request.SystemInstruction = &google.GeminiContent{
			Parts: systemParts,
		}
	}

	// 2. Map conversation messages
	var contents []google.GeminiContent
	toolUseIDToName := make(map[string]string)
	seenToolUseIDs := make(map[string]int)
	toolUseIDRemap := make(map[string]string)

	for _, msg := range req.Messages {
		var role string
		var parts []google.GeminiPart

		switch msg.Role {
		case "assistant":
			role = "model"
			for _, block := range parseAnthropicContentBlocks(msg.Content) {
				switch block.Type {
				case "text":
					if block.Text != "" {
						parts = append(parts, google.GeminiPart{Text: block.Text})
					}
				case "thinking":
					parts = append(parts, google.GeminiPart{
						Text:             block.Thinking,
						Thought:          true,
						ThoughtSignature: block.Signature,
					})
				case "tool_use":
					var argsMap map[string]interface{}
					if block.Input != nil {
						if m, ok := block.Input.(map[string]interface{}); ok {
							argsMap = m
						} else {
							b, _ := json.Marshal(block.Input)
							_ = json.Unmarshal(b, &argsMap)
						}
					}
					if argsMap == nil {
						argsMap = make(map[string]interface{})
					}
					effectiveID := block.ID
					if block.ID != "" {
						count := seenToolUseIDs[block.ID]
						seenToolUseIDs[block.ID]++
						if count > 0 {
							effectiveID = fmt.Sprintf("%s_d%d", block.ID, count+1)
						}
						toolUseIDRemap[block.ID] = effectiveID
						if block.Name != "" {
							toolUseIDToName[block.ID] = block.Name
							if p := strings.Split(block.ID, "|"); len(p) > 1 && p[0] != "" {
								toolUseIDToName[p[0]] = block.Name
							}
						}
					}
					parts = append(parts, google.GeminiPart{
						ThoughtSignature: SkipThoughtSignatureValidator,
						FunctionCall: &google.FunctionCall{
							ID:   effectiveID,
							Name: block.Name,
							Args: argsMap,
						},
					})
				}
			}

		default: // "user"
			var toolParts []google.GeminiPart
			var userParts []google.GeminiPart

			for _, block := range parseAnthropicContentBlocks(msg.Content) {
				switch block.Type {
				case "text":
					if block.Text != "" {
						userParts = append(userParts, google.GeminiPart{Text: block.Text})
					}
				case "image":
					if block.Source != nil && block.Source.Type == "base64" {
						userParts = append(userParts, google.GeminiPart{
							InlineData: &google.InlineData{
								MimeType: block.Source.MediaType,
								Data:     block.Source.Data,
							},
						})
					}
				case "tool_result":
					name := toolUseIDToName[block.ToolUseID]
					if name == "" {
						if p := strings.Split(block.ToolUseID, "|"); len(p) > 1 && p[0] != "" {
							name = toolUseIDToName[p[0]]
						}
					}
					if name == "" {
						if len(req.Tools) > 0 && req.Tools[0].Name != "" {
							name = req.Tools[0].Name
						} else {
							name = block.ToolUseID
							if name == "" {
								name = "tool"
							}
						}
					}
					effectiveID := block.ToolUseID
					if remapped, ok := toolUseIDRemap[block.ToolUseID]; ok && remapped != "" {
						effectiveID = remapped
					}
					var respMap map[string]interface{}
					if str, ok := block.Content.(string); ok {
						if strings.Contains(str, `"$ref"`) || strings.Contains(str, `"$defs"`) {
							respMap = map[string]interface{}{"output": str}
						} else if err := json.Unmarshal([]byte(str), &respMap); err != nil {
							respMap = map[string]interface{}{"result": str}
						}
					} else if block.Content != nil {
						b, _ := json.Marshal(block.Content)
						if strings.Contains(string(b), `"$ref"`) || strings.Contains(string(b), `"$defs"`) {
							respMap = map[string]interface{}{"output": string(b)}
						} else if err := json.Unmarshal(b, &respMap); err != nil {
							respMap = map[string]interface{}{"result": string(b)}
						}
					} else {
						respMap = map[string]interface{}{"result": "success"}
					}
					if block.IsError {
						respMap["is_error"] = true
					}
					toolParts = append(toolParts, google.GeminiPart{
						FunctionResponse: &google.FunctionResponse{
							ID:       effectiveID,
							Name:     name,
							Response: respMap,
						},
					})
				}
			}

			// First handle tool response parts (if any)
			if len(toolParts) > 0 {
				if len(contents) > 0 && contents[len(contents)-1].Role == "user" {
					if hasFunctionResponse(contents[len(contents)-1]) {
						contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, toolParts...)
					} else {
						// Previous user content was human text; interpose placeholder
						contents = append(contents, google.GeminiContent{
							Role:  "model",
							Parts: []google.GeminiPart{{Text: InterruptedResponsePlaceholder}},
						})
						contents = append(contents, google.GeminiContent{
							Role:  "user",
							Parts: toolParts,
						})
					}
				} else {
					contents = append(contents, google.GeminiContent{
						Role:  "user",
						Parts: toolParts,
					})
				}
			}

			// Next handle regular user text/image parts (if any)
			if len(userParts) > 0 {
				if len(contents) > 0 && contents[len(contents)-1].Role == "user" {
					if hasFunctionResponse(contents[len(contents)-1]) {
						// Previous user content was tool result; interpose placeholder so human text isn't folded into tool results
						contents = append(contents, google.GeminiContent{
							Role:  "model",
							Parts: []google.GeminiPart{{Text: InterruptedResponsePlaceholder}},
						})
						contents = append(contents, google.GeminiContent{
							Role:  "user",
							Parts: userParts,
						})
					} else {
						contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, userParts...)
					}
				} else {
					contents = append(contents, google.GeminiContent{
						Role:  "user",
						Parts: userParts,
					})
				}
			}
			continue
		}

		if len(parts) == 0 {
			continue
		}

		// Merge adjacent messages with the same role to strictly satisfy Gemini alternating role rule
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
		} else {
			contents = append(contents, google.GeminiContent{
				Role:  role,
				Parts: parts,
			})
		}
	}
	internalReq.Request.Contents = contents

	// 3. Map Tools
	if len(req.Tools) > 0 {
		var decls []google.FunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, google.FunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
		if len(decls) > 0 {
			internalReq.Request.Tools = []google.GeminiTool{{FunctionDeclarations: decls}}
		}
	}

	// 4. Map GenerationConfig
	genCfg := &google.GenerationConfig{
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: req.StopSequences,
	}

	if req.MaxTokens > 0 {
		maxT := req.MaxTokens
		genCfg.MaxOutputTokens = &maxT
	}

	// Thinking configuration
	if req.Thinking != nil {
		if req.Thinking.Type == "disabled" {
			includeThoughts := false
			zeroBudget := 0
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingBudget:  &zeroBudget,
			}
		} else if req.Thinking.Type == "adaptive" {
			includeThoughts := true
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
			}
		} else if req.Thinking.BudgetTokens > 0 {
			budget := req.Thinking.BudgetTokens
			includeThoughts := true
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				ThinkingBudget:  &budget,
				IncludeThoughts: &includeThoughts,
			}
		}
	} else if req.OutputConfig != nil && req.OutputConfig.Effort != "" {
		effort := strings.ToLower(strings.TrimSpace(req.OutputConfig.Effort))
		switch effort {
		case "low":
			includeThoughts := true
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingLevel:   "LOW",
			}
		case "medium":
			includeThoughts := true
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingLevel:   "MEDIUM",
			}
		case "high", "xhigh", "max":
			includeThoughts := true
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingLevel:   "HIGH",
			}
		}
	} else if rawModel := strings.ToLower(req.Model); strings.Contains(rawModel, "-high") || strings.Contains(rawModel, "pro-high") || strings.Contains(rawModel, "flash-high") {
		includeThoughts := true
		genCfg.ThinkingConfig = &google.ThinkingConfig{
			IncludeThoughts: &includeThoughts,
			ThinkingLevel:   "HIGH",
		}
	} else if rawModel := strings.ToLower(req.Model); strings.Contains(rawModel, "-medium") {
		includeThoughts := true
		genCfg.ThinkingConfig = &google.ThinkingConfig{
			IncludeThoughts: &includeThoughts,
			ThinkingLevel:   "MEDIUM",
		}
	} else if rawModel := strings.ToLower(req.Model); strings.Contains(rawModel, "-low") || strings.Contains(rawModel, "-minimal") {
		includeThoughts := true
		genCfg.ThinkingConfig = &google.ThinkingConfig{
			IncludeThoughts: &includeThoughts,
			ThinkingLevel:   "LOW",
		}
	} else if rawModel := strings.ToLower(req.Model); strings.Contains(rawModel, "thinking") || strings.Contains(model, "thinking") {
		includeThoughts := true
		genCfg.ThinkingConfig = &google.ThinkingConfig{
			IncludeThoughts: &includeThoughts,
		}
	}

	internalReq.Request.GenerationConfig = genCfg
	return internalReq
}

// FormatAnthropicEvents transforms a GeminiCandidate stream chunk into a sequence of Anthropic SSE events.
func FormatAnthropicEvents(msgID, model string, cand *google.Candidate, state *AnthropicStreamState) [][]byte {
	if state == nil {
		state = &AnthropicStreamState{}
	}

	var events [][]byte

	// 1. Emit message_start on the very first chunk
	if !state.MessageStarted {
		state.MessageStarted = true
		msgStart := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":            msgID,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"content":       []interface{}{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]interface{}{
					"input_tokens":  state.InputTokens,
					"output_tokens": 1,
				},
			},
		}
		events = append(events, formatAnthropicSSE("message_start", msgStart))
	}

	// 2. Process Candidate parts
	if cand != nil {
		for _, part := range cand.Content.Parts {
			if part.ThoughtSignature != "" {
				funcName := ""
				callID := ""
				if part.FunctionCall != nil {
					funcName = part.FunctionCall.Name
					callID = part.FunctionCall.ID
				}
				StoreThoughtSignature(callID, funcName, part.ThoughtSignature)
			}

			if part.Thought {
				clean := cleanThinkingTags(part.Text)
				if clean == "" && part.ThoughtSignature == "" {
					continue
				}
				if !state.BlockOpen || state.CurrentBlockType != "thinking" {
					if state.BlockOpen {
						events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
							"type":  "content_block_stop",
							"index": state.CurrentBlockIdx,
						}))
						state.CurrentBlockIdx++
					}
					cb := map[string]interface{}{
						"type":     "thinking",
						"thinking": "",
					}
					if part.ThoughtSignature != "" {
						cb["signature"] = part.ThoughtSignature
					}
					events = append(events, formatAnthropicSSE("content_block_start", map[string]interface{}{
						"type":          "content_block_start",
						"index":         state.CurrentBlockIdx,
						"content_block": cb,
					}))
					state.BlockOpen = true
					state.CurrentBlockType = "thinking"
				}

				if clean != "" {
					events = append(events, formatAnthropicSSE("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": state.CurrentBlockIdx,
						"delta": map[string]interface{}{
							"type":     "thinking_delta",
							"thinking": clean,
						},
					}))
				}
				if part.ThoughtSignature != "" {
					events = append(events, formatAnthropicSSE("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": state.CurrentBlockIdx,
						"delta": map[string]interface{}{
							"type":      "signature_delta",
							"signature": part.ThoughtSignature,
						},
					}))
				}
			} else if part.Text != "" {
				if leaked, ok := TryParseLeakedToolCall(part.Text); ok {
					if state.BlockOpen {
						events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
							"type":  "content_block_stop",
							"index": state.CurrentBlockIdx,
						}))
						state.CurrentBlockIdx++
					}
					state.ToolUseEmitted = true

					callID := fmt.Sprintf("toolu_leaked_%d", state.CurrentBlockIdx)

					events = append(events, formatAnthropicSSE("content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": state.CurrentBlockIdx,
						"content_block": map[string]interface{}{
							"type":  "tool_use",
							"id":    callID,
							"name":  leaked.Name,
							"input": map[string]interface{}{},
						},
					}))

					events = append(events, formatAnthropicSSE("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": state.CurrentBlockIdx,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": leaked.Arguments,
						},
					}))

					events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
						"type":  "content_block_stop",
						"index": state.CurrentBlockIdx,
					}))
					state.BlockOpen = false
					state.CurrentBlockType = ""
					state.CurrentBlockIdx++
					continue
				}

				clean := cleanThinkingTags(part.Text)
				if clean == "" {
					continue
				}
				if !state.BlockOpen || state.CurrentBlockType != "text" {
					if state.BlockOpen {
						events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
							"type":  "content_block_stop",
							"index": state.CurrentBlockIdx,
						}))
						state.CurrentBlockIdx++
					}
					events = append(events, formatAnthropicSSE("content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": state.CurrentBlockIdx,
						"content_block": map[string]interface{}{
							"type": "text",
							"text": "",
						},
					}))
					state.BlockOpen = true
					state.CurrentBlockType = "text"
				}

				events = append(events, formatAnthropicSSE("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": state.CurrentBlockIdx,
					"delta": map[string]interface{}{
						"type": "text_delta",
						"text": clean,
					},
				}))
			} else if part.FunctionCall != nil {
				if state.BlockOpen {
					events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
						"type":  "content_block_stop",
						"index": state.CurrentBlockIdx,
					}))
					state.CurrentBlockIdx++
				}
				state.ToolUseEmitted = true

				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("toolu_%d", state.CurrentBlockIdx)
				}
				if part.ThoughtSignature != "" {
					StoreThoughtSignature(callID, part.FunctionCall.Name, part.ThoughtSignature)
				}

				events = append(events, formatAnthropicSSE("content_block_start", map[string]interface{}{
					"type":  "content_block_start",
					"index": state.CurrentBlockIdx,
					"content_block": map[string]interface{}{
						"type":  "tool_use",
						"id":    callID,
						"name":  part.FunctionCall.Name,
						"input": map[string]interface{}{},
					},
				}))

				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				events = append(events, formatAnthropicSSE("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": state.CurrentBlockIdx,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"partial_json": string(argsBytes),
					},
				}))

				events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.CurrentBlockIdx,
				}))
				state.BlockOpen = false
				state.CurrentBlockType = ""
				state.CurrentBlockIdx++
			}
		}

		// 3. Handle finish reason
		if cand.FinishReason != "" {
			if state.BlockOpen {
				events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.CurrentBlockIdx,
				}))
				state.BlockOpen = false
				state.CurrentBlockType = ""
			}

			if state.CurrentBlockIdx == 0 && !state.ToolUseEmitted {
				events = append(events, formatAnthropicSSE("content_block_start", map[string]interface{}{
					"type":  "content_block_start",
					"index": 0,
					"content_block": map[string]interface{}{
						"type": "text",
						"text": ".",
					},
				}))
				events = append(events, formatAnthropicSSE("content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": 0,
				}))
				state.CurrentBlockIdx++
			}

			stopReason := mapAnthropicStopReason(cand.FinishReason, state.ToolUseEmitted)
			usageMap := map[string]interface{}{
				"output_tokens": state.OutputTokens,
			}
			if state.ThinkingTokens > 0 {
				usageMap["output_tokens_details"] = map[string]interface{}{
					"thinking_tokens": state.ThinkingTokens,
				}
			}
			events = append(events, formatAnthropicSSE("message_delta", map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason":   stopReason,
					"stop_sequence": nil,
				},
				"usage": usageMap,
			}))

			events = append(events, formatAnthropicSSE("message_stop", map[string]interface{}{
				"type": "message_stop",
			}))
			state.MessageStopped = true
		}
	}

	return events
}

// FormatAnthropicResponse translates a completed non-streaming GeminiResponse into AnthropicMessagesResponse.
func FormatAnthropicResponse(msgID, model string, resp *google.GeminiResponse) *AnthropicMessagesResponse {
	if resp == nil {
		return nil
	}

	var usageDetails *AnthropicOutputTokensDetails
	if resp.UsageMetadata.ThoughtsTokenCount > 0 {
		usageDetails = &AnthropicOutputTokensDetails{
			ThinkingTokens: resp.UsageMetadata.ThoughtsTokenCount,
		}
	}

	out := &AnthropicMessagesResponse{
		ID:      msgID,
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []AnthropicContentBlock{},
		Usage: AnthropicUsage{
			InputTokens:         resp.UsageMetadata.PromptTokenCount,
			OutputTokens:        resp.UsageMetadata.CandidatesTokenCount,
			OutputTokensDetails: usageDetails,
		},
	}

	hasToolUse := false
	var finishReason string

	for _, cand := range resp.Candidates {
		if cand.FinishReason != "" {
			finishReason = cand.FinishReason
		}

		for _, part := range cand.Content.Parts {
			if part.ThoughtSignature != "" {
				funcName := ""
				callID := ""
				if part.FunctionCall != nil {
					funcName = part.FunctionCall.Name
					callID = part.FunctionCall.ID
				}
				StoreThoughtSignature(callID, funcName, part.ThoughtSignature)
			}

			if part.Thought {
				out.Content = append(out.Content, AnthropicContentBlock{
					Type:      "thinking",
					Thinking:  cleanThinkingTags(part.Text),
					Signature: part.ThoughtSignature,
				})
			} else if part.Text != "" {
				if leaked, ok := TryParseLeakedToolCall(part.Text); ok {
					hasToolUse = true
					callID := fmt.Sprintf("toolu_leaked_%d", len(out.Content))
					out.Content = append(out.Content, AnthropicContentBlock{
						Type:  "tool_use",
						ID:    callID,
						Name:  leaked.Name,
						Input: leaked.ArgsMap,
					})
				} else {
					out.Content = append(out.Content, AnthropicContentBlock{
						Type: "text",
						Text: cleanThinkingTags(part.Text),
					})
				}
			} else if part.FunctionCall != nil {
				hasToolUse = true
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("toolu_%d", len(out.Content))
				}
				out.Content = append(out.Content, AnthropicContentBlock{
					Type:  "tool_use",
					ID:    callID,
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				})
			}
		}
	}

	if len(out.Content) == 0 {
		out.Content = append(out.Content, AnthropicContentBlock{
			Type: "text",
			Text: ".",
		})
	}

	stopReason := mapAnthropicStopReason(finishReason, hasToolUse)
	out.StopReason = &stopReason

	return out
}

func parseAnthropicContentBlocks(content interface{}) []AnthropicContentBlock {
	if content == nil {
		return nil
	}

	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []AnthropicContentBlock{{Type: "text", Text: c}}

	case []AnthropicContentBlock:
		return c

	case []interface{}:
		var blocks []AnthropicContentBlock
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				bType, _ := m["type"].(string)
				block := AnthropicContentBlock{Type: bType}
				switch bType {
				case "text":
					block.Text, _ = m["text"].(string)
				case "thinking":
					block.Thinking, _ = m["thinking"].(string)
					block.Signature, _ = m["signature"].(string)
				case "tool_use":
					block.ID, _ = m["id"].(string)
					block.Name, _ = m["name"].(string)
					block.Input = m["input"]
				case "tool_result":
					block.ToolUseID, _ = m["tool_use_id"].(string)
					block.Content = m["content"]
					block.IsError, _ = m["is_error"].(bool)
				case "image":
					if src, ok := m["source"].(map[string]interface{}); ok {
						block.Source = &AnthropicImageSource{
							Type:      src["type"].(string),
							MediaType: src["media_type"].(string),
							Data:      src["data"].(string),
						}
					}
				}
				blocks = append(blocks, block)
			}
		}
		return blocks

	default:
		return []AnthropicContentBlock{{Type: "text", Text: fmt.Sprintf("%v", content)}}
	}
}

func formatAnthropicSSE(eventType string, payload interface{}) []byte {
	bytes, _ := json.Marshal(payload)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(bytes)))
}

func mapAnthropicStopReason(geminiReason string, hasToolUse bool) string {
	if hasToolUse {
		return "tool_use"
	}
	switch strings.ToUpper(geminiReason) {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY", "RECITATION":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}
