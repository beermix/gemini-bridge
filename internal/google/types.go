package google

import (
	"encoding/json"
	"fmt"
)

// GeminiInternalRequest represents the outer request envelope sent to Google Cloud Code internal endpoints.
type GeminiInternalRequest struct {
	Project            string        `json:"project,omitempty"`
	RequestID          string        `json:"requestId,omitempty"`
	Model              string        `json:"model,omitempty"`
	UserAgent          string        `json:"userAgent,omitempty"`
	RequestType        string        `json:"requestType,omitempty"`
	EnabledCreditTypes []string      `json:"enabledCreditTypes,omitempty"`
	Request            GeminiRequest `json:"request"`
}

// GeminiRequest represents the inner Gemini model generation request payload.
type GeminiRequest struct {
	Contents          []GeminiContent    `json:"contents"`
	SessionID         string             `json:"sessionId,omitempty"`
	SystemInstruction *GeminiContent     `json:"systemInstruction,omitempty"`
	Tools             []GeminiTool       `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig  `json:"toolConfig,omitempty"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
	SafetySettings    []SafetySetting    `json:"safetySettings,omitempty"`
	Labels            map[string]string  `json:"labels,omitempty"`
}

// GeminiContent represents a single turn of content in Gemini conversation history.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a part of content (text, thought reasoning, tool call, tool response, or inline data).
type GeminiPart struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *InlineData       `json:"inlineData,omitempty"`
}

// FunctionCall represents a model-generated function invocation.
type FunctionCall struct {
	ID   string                 `json:"id,omitempty"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// FunctionResponse represents the result of executing a function call.
type FunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// InlineData represents raw binary data (e.g. images) encoded in Base64.
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// GenerationConfig contains sampling and generation parameters for the model.
type GenerationConfig struct {
	Temperature      *float64               `json:"temperature,omitempty"`
	TopP             *float64               `json:"topP,omitempty"`
	TopK             *int                   `json:"topK,omitempty"`
	MaxOutputTokens  *int                   `json:"maxOutputTokens,omitempty"`
	StopSequences    []string               `json:"stopSequences,omitempty"`
	ResponseMimeType string                 `json:"responseMimeType,omitempty"`
	ResponseSchema   map[string]interface{} `json:"responseSchema,omitempty"`
	ThinkingConfig     *ThinkingConfig        `json:"thinkingConfig,omitempty"`
	ResponseModalities []string               `json:"responseModalities,omitempty"`
	ImageConfig        *GeminiImageConfig     `json:"imageConfig,omitempty"`
	CandidateCount     int                    `json:"candidateCount,omitempty"`
}

// GeminiImageConfig specifies aspect ratio or size for image generation.
type GeminiImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

// ThinkingConfig controls reasoning/thinking parameters in supported models.
type ThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts *bool  `json:"includeThoughts,omitempty"`
}

// GeminiTool defines tools available to the model (such as function declarations).
type GeminiTool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration defines a callable function signature.
type FunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// GeminiToolConfig configures how tools should be invoked.
type GeminiToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// FunctionCallingConfig specifies function calling mode and allowed functions.
type FunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// SafetySetting defines thresholds for blocking harmful content.
type SafetySetting struct {
	Category  string `json:"category,omitempty"`
	Threshold string `json:"threshold,omitempty"`
}

// GeminiResponse represents the generation response from Google Cloud Code.
type GeminiResponse struct {
	Candidates    []Candidate   `json:"candidates,omitempty"`
	UsageMetadata UsageMetadata `json:"usageMetadata,omitempty"`
	ResponseId    string        `json:"responseId,omitempty"`
	ModelVersion  string        `json:"modelVersion,omitempty"`
}

// Candidate represents a candidate generation from the model.
type Candidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index,omitempty"`
}

// UsageMetadata records token counts for input and output.
type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// ParseGeminiResponse parses raw JSON bytes into *GeminiResponse, handling both
// the wrapped `{ "response": { ... } }` envelope and the bare `{ ... }` envelope.
func ParseGeminiResponse(data []byte) (*GeminiResponse, error) {
	var wrapped struct {
		Response *GeminiResponse `json:"response"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Response != nil {
		return wrapped.Response, nil
	}

	var bare GeminiResponse
	if err := json.Unmarshal(data, &bare); err != nil {
		return nil, fmt.Errorf("failed to parse gemini response: %w", err)
	}
	return &bare, nil
}
