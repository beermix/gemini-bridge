package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gemini-bridge/internal/google"
)

const (
	// SkipThoughtSignatureValidator is the official sentinel supported by Cloud Code Assist
	// and Hermes to bypass thought signature validation for cross-provider tool calls.
	SkipThoughtSignatureValidator = "skip_thought_signature_validator"

	// ForcedToolDirective is injected into the transcript when tool_choice is forced on Gemini routes,
	// because Cloud Code Assist drops functionCallingConfig mode "ANY" on Gemini models.
	ForcedToolDirective = "TOOL-ONLY TURN. This turn accepts a tool call and nothing else; a text reply here is discarded unread and you will be re-prompted. Emit the tool call now."

	// InterruptedResponsePlaceholder is interposed between a functionResponse user turn
	// and a human user text turn to preserve Gemini alternation while preventing text fold.
	InterruptedResponsePlaceholder = "[The previous response was interrupted before it completed.]"
)

var (
	thoughtSigMu      sync.RWMutex
	thoughtSigCache   = make(map[string]string)
	defaultThoughtSig = SkipThoughtSignatureValidator
	lastThoughtSig    = defaultThoughtSig
)

// ClearThoughtSignatures resets the thought signature cache and clears fallback.
func ClearThoughtSignatures() {
	thoughtSigMu.Lock()
	defer thoughtSigMu.Unlock()
	thoughtSigCache = make(map[string]string)
	lastThoughtSig = defaultThoughtSig
}

// StripThoughtSignatures replaces thought signatures with the skip validator sentinel on functionCall parts.
func StripThoughtSignatures(req *google.GeminiInternalRequest) {
	if req == nil {
		return
	}
	for i := range req.Request.Contents {
		for j := range req.Request.Contents[i].Parts {
			if req.Request.Contents[i].Parts[j].FunctionCall != nil {
				req.Request.Contents[i].Parts[j].ThoughtSignature = SkipThoughtSignatureValidator
			} else {
				req.Request.Contents[i].Parts[j].ThoughtSignature = ""
			}
		}
	}
}

// IsInvalidThoughtSignatureError checks whether an error is a Google HTTP 400 invalid thought signature error.
func IsInvalidThoughtSignatureError(err error) bool {
	if err == nil {
		return false
	}
	var upErr *google.UpstreamError
	if errors.As(err, &upErr) && upErr.StatusCode == 400 {
		lower := strings.ToLower(upErr.Body)
		return strings.Contains(lower, "thought signature") || strings.Contains(lower, "thought_signature")
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "400") && (strings.Contains(lower, "thought signature") || strings.Contains(lower, "thought_signature"))
}

// StoreThoughtSignature records a thought signature for a tool call ID and/or function name.
func StoreThoughtSignature(callID, funcName, sig string) {
	if sig == "" || sig == SkipThoughtSignatureValidator {
		return
	}
	thoughtSigMu.Lock()
	defer thoughtSigMu.Unlock()
	lastThoughtSig = sig
	if callID != "" {
		thoughtSigCache[callID] = sig
	}
	if funcName != "" {
		thoughtSigCache["fn:"+funcName] = sig
	}
	if len(thoughtSigCache) > 1000 {
		thoughtSigCache = make(map[string]string)
		if callID != "" {
			thoughtSigCache[callID] = sig
		}
		if funcName != "" {
			thoughtSigCache["fn:"+funcName] = sig
		}
	}
}

// GetThoughtSignature retrieves the recorded thought signature for a tool call ID or function name,
// falling back to the most recently recorded valid thought signature.
func GetThoughtSignature(callID, funcName string) string {
	thoughtSigMu.RLock()
	defer thoughtSigMu.RUnlock()
	if callID != "" {
		if sig, ok := thoughtSigCache[callID]; ok && sig != "" && sig != SkipThoughtSignatureValidator {
			return sig
		}
	}
	if funcName != "" {
		if sig, ok := thoughtSigCache["fn:"+funcName]; ok && sig != "" && sig != SkipThoughtSignatureValidator {
			return sig
		}
	}
	if lastThoughtSig != SkipThoughtSignatureValidator {
		return lastThoughtSig
	}
	return ""
}

// OpenAIChatRequest represents an incoming OpenAI-compatible chat completion request.
type OpenAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []OpenAIMessage `json:"messages"`
	Tools               []OpenAITool    `json:"tools,omitempty"`
	ToolChoice          interface{}     `json:"tool_choice,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                interface{}     `json:"stop,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ResponseFormat      interface{}     `json:"response_format,omitempty"`
}

// OpenAIMessage represents a single chat completion message.
type OpenAIMessage struct {
	Role             string           `json:"role"`
	Content          interface{}      `json:"content"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	Name             string           `json:"name,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
}

// OpenAIContentPart represents a multi-part content entry (text or image_url).
type OpenAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

// OpenAIImageURL represents an image payload in OpenAI message content.
type OpenAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// OpenAITool defines a callable tool in OpenAI format.
type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction defines function signature details.
type OpenAIFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// OpenAIChatResponse represents the response object for non-streaming completions.
type OpenAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

// OpenAIChoice represents a single choice in the response or chunk.
type OpenAIChoice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Delta        *OpenAIDelta   `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

// OpenAIDelta represents the incremental content in an SSE chunk.
type OpenAIDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
}

// OpenAIToolCall represents a tool call emitted by the model.
type OpenAIToolCall struct {
	Index    *int               `json:"index,omitempty"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall represents the function name and arguments in a tool call.
type OpenAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// OpenAICompletionTokensDetails holds extra breakdown of generated completion tokens.
type OpenAICompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// OpenAIPromptTokensDetails tracks breakdown of prompt tokens such as cached tokens.
type OpenAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// OpenAIUsage tracks token usage metrics.
type OpenAIUsage struct {
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *OpenAIPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *OpenAICompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// MapOpenAIToGemini translates an OpenAIChatRequest into a native GeminiInternalRequest.
func MapOpenAIToGemini(req *OpenAIChatRequest, projectID string) *google.GeminiInternalRequest {
	if req == nil {
		return nil
	}

	model := ResolveModel(req.Model)

	internalReq := &google.GeminiInternalRequest{
		Project:            projectID,
		Model:              model,
		UserAgent:          "antigravity",
		EnabledCreditTypes: []string{"CREDIT_TYPE_UNSPECIFIED"},
		Request:            google.GeminiRequest{},
	}

	// 1. Separate system messages vs conversation messages
	var systemParts []google.GeminiPart
	var convMessages []OpenAIMessage

	for _, msg := range req.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			for _, part := range parseOpenAIContentParts(msg.Content) {
				if part.Text != "" {
					systemParts = append(systemParts, part)
				}
			}
		} else {
			convMessages = append(convMessages, msg)
		}
	}

	firstUserPrompt := ""
	for _, msg := range convMessages {
		if msg.Role == "user" {
			for _, part := range parseOpenAIContentParts(msg.Content) {
				if part.Text != "" {
					firstUserPrompt = part.Text
					break
				}
			}
			if firstUserPrompt != "" {
				break
			}
		}
	}

	reqID, sessID, labels := GenerateAntigravityRequestEnvelope(model, firstUserPrompt)
	internalReq.RequestID = reqID
	internalReq.Request.SessionID = sessID
	internalReq.Request.Labels = labels

	if len(systemParts) > 0 {
		internalReq.Request.SystemInstruction = &google.GeminiContent{
			Parts: systemParts,
		}
	}

	// 2. Map conversation messages into GeminiContent turns
	var contents []google.GeminiContent
	toolCallIDToName := make(map[string]string)
	seenCallIDs := make(map[string]int)
	idRemap := make(map[string]string)

	for _, msg := range convMessages {
		var role string
		var parts []google.GeminiPart

		switch msg.Role {
		case "assistant":
			role = "model"
			parts = append(parts, parseOpenAIContentParts(msg.Content)...)
			for i, tc := range msg.ToolCalls {
				var argsMap map[string]interface{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &argsMap)
				}
				if argsMap == nil {
					argsMap = make(map[string]interface{})
				}
				effectiveID := tc.ID
				if tc.ID != "" {
					count := seenCallIDs[tc.ID]
					seenCallIDs[tc.ID]++
					if count > 0 {
						effectiveID = fmt.Sprintf("%s_d%d", tc.ID, count+1)
					}
					idRemap[tc.ID] = effectiveID
					if tc.Function.Name != "" {
						toolCallIDToName[tc.ID] = tc.Function.Name
						if parts := strings.Split(tc.ID, "|"); len(parts) > 1 && parts[0] != "" {
							toolCallIDToName[parts[0]] = tc.Function.Name
						}
					}
				}
				sig := GetThoughtSignature(tc.ID, tc.Function.Name)
				if sig == "" && i == 0 {
					sig = SkipThoughtSignatureValidator
				}
				parts = append(parts, google.GeminiPart{
					ThoughtSignature: sig,
					FunctionCall: &google.FunctionCall{
						ID:   effectiveID,
						Name: tc.Function.Name,
						Args: argsMap,
					},
				})
			}

		case "tool":
			role = "user"
			name := msg.Name
			if name == "" && msg.ToolCallID != "" {
				name = toolCallIDToName[msg.ToolCallID]
				if name == "" {
					if parts := strings.Split(msg.ToolCallID, "|"); len(parts) > 1 && parts[0] != "" {
						name = toolCallIDToName[parts[0]]
					}
				}
			}
			if name == "" {
				if len(req.Tools) > 0 && req.Tools[0].Function.Name != "" {
					name = req.Tools[0].Function.Name
				} else {
					name = "tool"
				}
			}
			effectiveID := msg.ToolCallID
			if remapped, ok := idRemap[msg.ToolCallID]; ok && remapped != "" {
				effectiveID = remapped
			}
			var respMap map[string]interface{}
			if str, ok := msg.Content.(string); ok {
				if strings.Contains(str, `"$ref"`) || strings.Contains(str, `"$defs"`) {
					respMap = map[string]interface{}{"output": str}
				} else if err := json.Unmarshal([]byte(str), &respMap); err != nil {
					respMap = map[string]interface{}{"result": str}
				}
			} else if m, ok := msg.Content.(map[string]interface{}); ok {
				b, _ := json.Marshal(m)
				if strings.Contains(string(b), `"$ref"`) || strings.Contains(string(b), `"$defs"`) {
					respMap = map[string]interface{}{"output": string(b)}
				} else {
					respMap = m
				}
			} else if msg.Content != nil {
				b, _ := json.Marshal(msg.Content)
				if strings.Contains(string(b), `"$ref"`) || strings.Contains(string(b), `"$defs"`) {
					respMap = map[string]interface{}{"output": string(b)}
				} else if err := json.Unmarshal(b, &respMap); err != nil {
					respMap = map[string]interface{}{"result": string(b)}
				}
			} else {
				respMap = map[string]interface{}{"result": "success"}
			}

			parts = append(parts, google.GeminiPart{
				FunctionResponse: &google.FunctionResponse{
					ID:       effectiveID,
					Name:     name,
					Response: respMap,
				},
			})

		default: // "user" or others
			role = "user"
			parts = append(parts, parseOpenAIContentParts(msg.Content)...)
		}

		if len(parts) == 0 {
			continue
		}

		// Merge adjacent messages with the same role to strictly satisfy Gemini alternating role requirements,
		// but interpose an interrupted response placeholder between tool results and user text so Gemini
		// doesn't fold human text into the function response or return empty completions.
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			prevHasFuncResp := hasFunctionResponse(contents[len(contents)-1])
			currIsTool := (msg.Role == "tool")

			if prevHasFuncResp && !currIsTool {
				contents = append(contents, google.GeminiContent{
					Role: "model",
					Parts: []google.GeminiPart{{
						Text: InterruptedResponsePlaceholder,
					}},
				})
				contents = append(contents, google.GeminiContent{
					Role:  role,
					Parts: parts,
				})
			} else if !prevHasFuncResp && currIsTool {
				contents = append(contents, google.GeminiContent{
					Role: "model",
					Parts: []google.GeminiPart{{
						Text: InterruptedResponsePlaceholder,
					}},
				})
				contents = append(contents, google.GeminiContent{
					Role:  role,
					Parts: parts,
				})
			} else {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
			}
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
		for _, tool := range req.Tools {
			if tool.Type == "function" || tool.Type == "" {
				decls = append(decls, google.FunctionDeclaration{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  NormalizeSchemaForGoogle(tool.Function.Parameters),
				})
			}
		}
		if len(decls) > 0 {
			internalReq.Request.Tools = []google.GeminiTool{{FunctionDeclarations: decls}}
		}
	}

	// 4. Map ToolChoice
	if len(req.Tools) > 0 {
		if req.ToolChoice != nil {
			switch tc := req.ToolChoice.(type) {
			case string:
				switch strings.ToLower(tc) {
				case "auto":
					internalReq.Request.ToolConfig = &google.GeminiToolConfig{
						FunctionCallingConfig: &google.FunctionCallingConfig{Mode: "VALIDATED"},
					}
				case "none":
					internalReq.Request.ToolConfig = &google.GeminiToolConfig{
						FunctionCallingConfig: &google.FunctionCallingConfig{Mode: "NONE"},
					}
				case "required":
					internalReq.Request.ToolConfig = &google.GeminiToolConfig{
						FunctionCallingConfig: &google.FunctionCallingConfig{Mode: "ANY"},
					}
				}
			case map[string]interface{}:
				if fn, ok := tc["function"].(map[string]interface{}); ok {
					if fnName, ok := fn["name"].(string); ok && fnName != "" {
						internalReq.Request.ToolConfig = &google.GeminiToolConfig{
							FunctionCallingConfig: &google.FunctionCallingConfig{
								Mode:                 "ANY",
								AllowedFunctionNames: []string{fnName},
							},
						}
					}
				}
			}
		}

		// Antigravity's default tool mode is VALIDATED (verified for Gemini and Claude)
		if internalReq.Request.ToolConfig == nil {
			internalReq.Request.ToolConfig = &google.GeminiToolConfig{
				FunctionCallingConfig: &google.FunctionCallingConfig{Mode: "VALIDATED"},
			}
		}
	}

	// Claude on Antigravity always forces VALIDATED, even with no tools declared
	if strings.HasPrefix(model, "claude-") && internalReq.Request.ToolConfig == nil {
		internalReq.Request.ToolConfig = &google.GeminiToolConfig{
			FunctionCallingConfig: &google.FunctionCallingConfig{Mode: "VALIDATED"},
		}
	}

	// Cloud Code Assist drops toolConfig on Gemini routes: under mode "ANY" it answers in text.
	// We restate forced tool choice by appending ForcedToolDirective to transcript.
	if internalReq.Request.ToolConfig != nil &&
		internalReq.Request.ToolConfig.FunctionCallingConfig != nil &&
		internalReq.Request.ToolConfig.FunctionCallingConfig.Mode == "ANY" &&
		!strings.HasPrefix(model, "claude-") {
		internalReq.Request.Contents = append(internalReq.Request.Contents, google.GeminiContent{
			Role:  "user",
			Parts: []google.GeminiPart{{Text: ForcedToolDirective}},
		})
	}

	// 5. Map GenerationConfig
	genCfg := &google.GenerationConfig{
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}

	if req.MaxCompletionTokens != nil {
		genCfg.MaxOutputTokens = req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		genCfg.MaxOutputTokens = req.MaxTokens
	}

	if req.Stop != nil {
		switch s := req.Stop.(type) {
		case string:
			if s != "" {
				genCfg.StopSequences = []string{s}
			}
		case []string:
			genCfg.StopSequences = s
		case []interface{}:
			var seqs []string
			for _, item := range s {
				if str, ok := item.(string); ok && str != "" {
					seqs = append(seqs, str)
				}
			}
			genCfg.StopSequences = seqs
		}
	}

	// Response format handling (JSON Object / JSON Schema)
	if req.ResponseFormat != nil {
		switch rf := req.ResponseFormat.(type) {
		case string:
			if strings.EqualFold(rf, "json_object") || strings.EqualFold(rf, "json") {
				genCfg.ResponseMimeType = "application/json"
			}
		case map[string]interface{}:
			rfType, _ := rf["type"].(string)
			if strings.EqualFold(rfType, "json_object") {
				genCfg.ResponseMimeType = "application/json"
			} else if strings.EqualFold(rfType, "json_schema") {
				genCfg.ResponseMimeType = "application/json"
				if schemaObj, ok := rf["json_schema"].(map[string]interface{}); ok {
					if schema, hasSchema := schemaObj["schema"].(map[string]interface{}); hasSchema {
						genCfg.ResponseSchema = schema
					}
				}
			}
		}
	}

	// Reasoning effort / thinking configuration
	if req.ReasoningEffort != "" {
		effort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort))
		switch effort {
		case "none":
			includeThoughts := false
			zeroBudget := 0
			genCfg.ThinkingConfig = &google.ThinkingConfig{
				IncludeThoughts: &includeThoughts,
				ThinkingBudget:  &zeroBudget,
				ThinkingLevel:   "LOW",
			}
		case "minimal", "low":
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
	} else if strings.Contains(model, "gemini-3") || strings.Contains(model, "gemini-2") {
		// Explicitly suppress default background reasoning to save token quotas
		includeThoughts := false
		zeroBudget := 0
		genCfg.ThinkingConfig = &google.ThinkingConfig{
			IncludeThoughts: &includeThoughts,
			ThinkingBudget:  &zeroBudget,
			ThinkingLevel:   "LOW",
		}
	}

	internalReq.Request.GenerationConfig = genCfg
	return internalReq
}

// ParseGeminiChunk parses a single Gemini SSE data chunk into a *google.GeminiResponse.
func ParseGeminiChunk(data []byte) (*google.GeminiResponse, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte(":")) {
		return nil, nil
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		trimmed = bytes.TrimSpace(trimmed[5:])
	}

	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return nil, nil
	}

	return google.ParseGeminiResponse(trimmed)
}

// OpenAIChunkOptions provides streaming control flags for chunk formatting.
type OpenAIChunkOptions struct {
	IsFirstChunk        bool
	HasEmittedToolCalls bool
}

// FormatOpenAIChunk converts a Gemini stream chunk into an SSE-formatted OpenAI delta chunk.
func FormatOpenAIChunk(streamID, model string, cand *google.Candidate, usage *google.UsageMetadata) []byte {
	bytes, _ := FormatOpenAIChunkWithOptions(streamID, model, cand, usage, OpenAIChunkOptions{IsFirstChunk: true})
	return bytes
}

// FormatOpenAIChunkWithOptions converts a Gemini stream chunk with fine-grained streaming options.
// It returns the formatted SSE chunk bytes and a boolean indicating whether any tool calls were emitted in this chunk.
func FormatOpenAIChunkWithOptions(streamID, model string, cand *google.Candidate, usage *google.UsageMetadata, opts OpenAIChunkOptions) ([]byte, bool) {
	chunk := OpenAIChatResponse{
		ID:      streamID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChoice{},
	}

	emittedToolsInChunk := false

	if cand != nil {
		delta := OpenAIDelta{}
		if opts.IsFirstChunk {
			delta.Role = "assistant"
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
				clean := cleanThinkingTags(part.Text)
				if clean != "" {
					delta.ReasoningContent += clean
				}
			} else if part.Text != "" {
				if leaked, ok := TryParseLeakedToolCall(part.Text); ok {
					idx := len(delta.ToolCalls)
					callID := fmt.Sprintf("call_leaked_%d", idx)
					delta.ToolCalls = append(delta.ToolCalls, OpenAIToolCall{
						Index: &idx,
						ID:    callID,
						Type:  "function",
						Function: OpenAIFunctionCall{
							Name:      leaked.Name,
							Arguments: leaked.Arguments,
						},
					})
					emittedToolsInChunk = true
				} else {
					clean := cleanThinkingTags(part.Text)
					if clean != "" {
						delta.Content += clean
					}
				}
			}

			if part.FunctionCall != nil {
				idx := len(delta.ToolCalls)
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d", idx)
				}
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				delta.ToolCalls = append(delta.ToolCalls, OpenAIToolCall{
					Index: &idx,
					ID:    callID,
					Type:  "function",
					Function: OpenAIFunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
				emittedToolsInChunk = true
			}
		}

		var finishReason *string
		if cand.FinishReason != "" {
			fr := mapOpenAIFinishReason(cand.FinishReason, len(delta.ToolCalls) > 0 || opts.HasEmittedToolCalls || emittedToolsInChunk)
			finishReason = &fr
		}

		choice := OpenAIChoice{
			Index:        cand.Index,
			Delta:        &delta,
			FinishReason: finishReason,
		}
		chunk.Choices = append(chunk.Choices, choice)
	}

	if usage != nil {
		var details *OpenAICompletionTokensDetails
		if usage.ThoughtsTokenCount > 0 {
			details = &OpenAICompletionTokensDetails{
				ReasoningTokens: usage.ThoughtsTokenCount,
			}
		}
		promptTokens := usage.PromptTokenCount
		cachedTokens := usage.CachedContentTokenCount
		if cachedTokens > 0 && promptTokens >= cachedTokens {
			promptTokens -= cachedTokens
		}
		var promptDetails *OpenAIPromptTokensDetails
		if cachedTokens > 0 {
			promptDetails = &OpenAIPromptTokensDetails{
				CachedTokens: cachedTokens,
			}
		}
		chunk.Usage = &OpenAIUsage{
			PromptTokens:            promptTokens,
			CompletionTokens:        usage.CandidatesTokenCount,
			TotalTokens:             usage.TotalTokenCount,
			PromptTokensDetails:     promptDetails,
			CompletionTokensDetails: details,
		}
	}

	jsonBytes, err := json.Marshal(chunk)
	if err != nil {
		return nil, emittedToolsInChunk
	}

	return append(append([]byte("data: "), jsonBytes...), []byte("\n\n")...), emittedToolsInChunk
}

// FormatOpenAIResponse translates a completed non-streaming GeminiResponse into OpenAIChatResponse.
func FormatOpenAIResponse(respID, model string, resp *google.GeminiResponse) *OpenAIChatResponse {
	if resp == nil {
		return nil
	}

	var usageDetails *OpenAICompletionTokensDetails
	if resp.UsageMetadata.ThoughtsTokenCount > 0 {
		usageDetails = &OpenAICompletionTokensDetails{
			ReasoningTokens: resp.UsageMetadata.ThoughtsTokenCount,
		}
	}

	promptTokens := resp.UsageMetadata.PromptTokenCount
	cachedTokens := resp.UsageMetadata.CachedContentTokenCount
	if cachedTokens > 0 && promptTokens >= cachedTokens {
		promptTokens -= cachedTokens
	}
	var promptDetails *OpenAIPromptTokensDetails
	if cachedTokens > 0 {
		promptDetails = &OpenAIPromptTokensDetails{
			CachedTokens: cachedTokens,
		}
	}

	openAIResp := &OpenAIChatResponse{
		ID:      respID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: make([]OpenAIChoice, 0, len(resp.Candidates)),
		Usage: &OpenAIUsage{
			PromptTokens:            promptTokens,
			CompletionTokens:        resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:             resp.UsageMetadata.TotalTokenCount,
			PromptTokensDetails:     promptDetails,
			CompletionTokensDetails: usageDetails,
		},
	}

	for _, cand := range resp.Candidates {
		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		var toolCalls []OpenAIToolCall

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
				reasoningBuilder.WriteString(cleanThinkingTags(part.Text))
			} else if part.Text != "" {
				if leaked, ok := TryParseLeakedToolCall(part.Text); ok {
					idx := len(toolCalls)
					callID := fmt.Sprintf("call_leaked_%d", idx)
					toolCalls = append(toolCalls, OpenAIToolCall{
						Index: &idx,
						ID:    callID,
						Type:  "function",
						Function: OpenAIFunctionCall{
							Name:      leaked.Name,
							Arguments: leaked.Arguments,
						},
					})
				} else {
					contentBuilder.WriteString(cleanThinkingTags(part.Text))
				}
			}
			if part.FunctionCall != nil {
				idx := len(toolCalls)
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = fmt.Sprintf("call_%d", idx)
				}
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, OpenAIToolCall{
					Index: &idx,
					ID:    callID,
					Type:  "function",
					Function: OpenAIFunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
			}
		}

		var finishReason *string
		if cand.FinishReason != "" {
			fr := mapOpenAIFinishReason(cand.FinishReason, len(toolCalls) > 0)
			finishReason = &fr
		}

		openAIResp.Choices = append(openAIResp.Choices, OpenAIChoice{
			Index: cand.Index,
			Message: &OpenAIMessage{
				Role:             "assistant",
				Content:          contentBuilder.String(),
				ReasoningContent: reasoningBuilder.String(),
				ToolCalls:        toolCalls,
			},
			FinishReason: finishReason,
		})
	}

	return openAIResp
}

func parseOpenAIContentParts(content interface{}) []google.GeminiPart {
	if content == nil {
		return nil
	}

	switch c := content.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []google.GeminiPart{{Text: SanitizePromptText(c)}}

	case []OpenAIContentPart:
		var parts []google.GeminiPart
		for _, p := range c {
			if p.Type == "text" && p.Text != "" {
				parts = append(parts, google.GeminiPart{Text: SanitizePromptText(p.Text)})
			} else if p.Type == "image_url" && p.ImageURL != nil {
				if mime, data, ok := parseDataURL(p.ImageURL.URL); ok {
					parts = append(parts, google.GeminiPart{
						InlineData: &google.InlineData{MimeType: mime, Data: data},
					})
				}
			}
		}
		return parts

	case []interface{}:
		var parts []google.GeminiPart
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok {
				pType, _ := m["type"].(string)
				switch pType {
				case "text":
					if txt, ok := m["text"].(string); ok && txt != "" {
						parts = append(parts, google.GeminiPart{Text: SanitizePromptText(txt)})
					}
				case "image_url":
					if imgMap, ok := m["image_url"].(map[string]interface{}); ok {
						if url, ok := imgMap["url"].(string); ok {
							if mime, data, ok := parseDataURL(url); ok {
								parts = append(parts, google.GeminiPart{
									InlineData: &google.InlineData{MimeType: mime, Data: data},
								})
							}
						}
					}
				}
			}
		}
		return parts

	default:
		str := fmt.Sprintf("%v", content)
		if str != "" {
			return []google.GeminiPart{{Text: str}}
		}
		return nil
	}
}

func parseDataURL(url string) (mimeType string, data string, ok bool) {
	if !strings.HasPrefix(url, "data:") {
		return "", "", false
	}
	parts := strings.SplitN(url[5:], ";base64,", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func cleanThinkingTags(s string) string {
	s = strings.ReplaceAll(s, "<think>", "")
	s = strings.ReplaceAll(s, "</think>", "")
	s = StripThinkingFenceDelimiters(s)
	return s
}

func mapOpenAIFinishReason(geminiReason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	switch strings.ToUpper(geminiReason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION":
		return "content_filter"
	default:
		if geminiReason != "" {
			return strings.ToLower(geminiReason)
		}
		return "stop"
	}
}

func hasFunctionResponse(c google.GeminiContent) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}
