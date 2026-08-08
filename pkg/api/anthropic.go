package api

type AnthropicMessageRequest struct {
	Model         string             `json:"model"`
	Source        string             `json:"source,omitempty"`
	Messages      []AnthropicMessage `json:"messages"`
	System        interface{}        `json:"system,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    interface{}        `json:"tool_choice,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type AnthropicTool struct {
	Name                string      `json:"name"`
	Description         string      `json:"description,omitempty"`
	InputSchema         interface{} `json:"input_schema,omitempty"`
	CacheControl        interface{} `json:"cache_control,omitempty"`
	EagerInputStreaming bool        `json:"eager_input_streaming,omitempty"`
}

type AnthropicContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
	Thinking  string      `json:"thinking,omitempty"`
	Signature string      `json:"signature,omitempty"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type AnthropicMessageResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        AnthropicUsage          `json:"usage"`
}

type AnthropicCountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

type AnthropicCapabilitySupport struct {
	Supported bool `json:"supported"`
}

type AnthropicThinkingTypes struct {
	Adaptive AnthropicCapabilitySupport `json:"adaptive"`
	Enabled  AnthropicCapabilitySupport `json:"enabled"`
}

type AnthropicThinkingCapability struct {
	Supported bool                   `json:"supported"`
	Types     AnthropicThinkingTypes `json:"types"`
}

type AnthropicContextManagementCapability struct {
	Supported bool `json:"supported"`
}

type AnthropicModelCapabilities struct {
	Batch             AnthropicCapabilitySupport           `json:"batch"`
	Citations         AnthropicCapabilitySupport           `json:"citations"`
	CodeExecution     AnthropicCapabilitySupport           `json:"code_execution"`
	ContextManagement AnthropicContextManagementCapability `json:"context_management"`
	ImageInput        AnthropicCapabilitySupport           `json:"image_input"`
	PDFInput          AnthropicCapabilitySupport           `json:"pdf_input"`
	StructuredOutputs AnthropicCapabilitySupport           `json:"structured_outputs"`
	Thinking          AnthropicThinkingCapability          `json:"thinking"`
}

type AnthropicModelInfo struct {
	ID             string                     `json:"id"`
	Type           string                     `json:"type"`
	DisplayName    string                     `json:"display_name"`
	CreatedAt      string                     `json:"created_at"`
	MaxInputTokens int                        `json:"max_input_tokens"`
	MaxTokens      int                        `json:"max_tokens"`
	Capabilities   AnthropicModelCapabilities `json:"capabilities"`
}

type AnthropicModelListResponse struct {
	Data    []AnthropicModelInfo `json:"data"`
	FirstID string               `json:"first_id,omitempty"`
	HasMore bool                 `json:"has_more"`
	LastID  string               `json:"last_id,omitempty"`
}
