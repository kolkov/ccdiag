package proxy

import "time"

// ProxyEntry is one line in traffic.jsonl — a complete request/response pair.
type ProxyEntry struct {
	Timestamp     time.Time `json:"ts"`
	SessionID     string    `json:"session_id,omitempty"`
	RequestID     string    `json:"req_id,omitempty"`
	Model         string    `json:"model"`
	Status        int       `json:"status"`
	Streaming     bool      `json:"streaming"`
	LatencyMs     int64     `json:"latency_ms"`
	TTFBMs        int64     `json:"ttfb_ms,omitempty"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	CacheCreate   int       `json:"cache_create"`
	CacheRead     int       `json:"cache_read"`
	CacheRatio    float64   `json:"cache_ratio"`
	RequestBytes  int64     `json:"request_bytes"`
	ResponseBytes int64     `json:"response_bytes"`
	ToolCount     int       `json:"tool_count"`
	MsgCount      int       `json:"msg_count"`
	ErrorType     string    `json:"error_type,omitempty"`
	ErrorMessage  string    `json:"error_msg,omitempty"`
}

// requestMeta holds info extracted from the request body before forwarding.
type requestMeta struct {
	Model     string
	Stream    bool
	ToolCount int
	MsgCount  int
	SessionID string
}

// Usage mirrors the Anthropic usage block in responses.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}
