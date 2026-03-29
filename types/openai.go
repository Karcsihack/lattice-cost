// Package types defines the shared OpenAI-compatible request/response types
// used across all Lattice-Cost components.
package types

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the OpenAI-compatible chat completion request body.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	User        string    `json:"user,omitempty"`
}

// Choice is one completion option returned by the LLM.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage contains token consumption data from an LLM response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is the OpenAI-compatible chat completion response.
type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// ComplexityLevel classifies a prompt for smart routing.
type ComplexityLevel int

const (
	ComplexitySimple   ComplexityLevel = iota // → cheap model
	ComplexityModerate                        // → cheap model
	ComplexityComplex                         // → powerful model
)

func (c ComplexityLevel) String() string {
	switch c {
	case ComplexitySimple:
		return "SIMPLE"
	case ComplexityModerate:
		return "MODERATE"
	case ComplexityComplex:
		return "COMPLEX"
	default:
		return "UNKNOWN"
	}
}

// ErrorResponse is the OpenAI-compatible error envelope.
type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}
