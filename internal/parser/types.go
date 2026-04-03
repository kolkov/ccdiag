package parser

import (
	"encoding/json"
	"time"
)

// Message represents a single line in the JSONL session file.
type Message struct {
	Type       string          `json:"type"` // user, assistant, progress, file-history-snapshot, queue-operation
	UUID       string          `json:"uuid"`
	ParentUUID *string         `json:"parentUuid"`
	Timestamp  time.Time       `json:"timestamp"`
	SessionID  string          `json:"sessionId"`
	Version    string          `json:"version"`
	Message    *MessageContent `json:"message"`
	Data       *ProgressData   `json:"data"`

	// Tool result metadata (on user messages containing tool_result)
	ToolUseResult        *ToolUseResultMeta `json:"toolUseResult"`
	SourceToolAssistUUID string             `json:"sourceToolAssistantUUID"`

	// Progress-specific
	ToolUseID       string `json:"toolUseID"`
	ParentToolUseID string `json:"parentToolUseID"`

	// Sidechain / branch info
	IsSidechain bool `json:"isSidechain"`
}

// MessageContent is the inner message payload.
type MessageContent struct {
	Role    string          `json:"role"` // user, assistant
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string or []ContentBlock
	Usage   *Usage          `json:"usage"`
	ID      string          `json:"id"` // message id from API
}

// ContentBlock represents one block inside message.content array.
type ContentBlock struct {
	Type string `json:"type"` // tool_use, tool_result, text, thinking

	// tool_use fields
	ID    string          `json:"id,omitempty"`   // tool_use id (toolu_...)
	Name  string          `json:"name,omitempty"` // tool name (Bash, Read, etc.)
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result fields
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or array

	// text fields
	Text string `json:"text,omitempty"`

	// thinking fields
	Thinking string `json:"thinking,omitempty"`
}

// ToolUseResultMeta is extra metadata attached to user messages with tool results.
type ToolUseResultMeta struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`
	IsImage     bool   `json:"isImage"`
}

// ProgressData is data from progress-type messages.
type ProgressData struct {
	Type               string  `json:"type"`
	Output             string  `json:"output"`
	FullOutput         string  `json:"fullOutput"`
	ElapsedTimeSeconds float64 `json:"elapsedTimeSeconds"`
	TotalLines         int     `json:"totalLines"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens              int            `json:"input_tokens"`
	OutputTokens             int            `json:"output_tokens"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int            `json:"cache_read_input_tokens"`
	CacheCreation            *CacheCreation `json:"cache_creation,omitempty"`
}

// CacheCreation holds cache creation details.
type CacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

// ToolUseEntry tracks a tool_use occurrence.
type ToolUseEntry struct {
	ID        string
	Name      string
	Timestamp time.Time
	UUID      string
}

// ToolResultEntry tracks a tool_result occurrence.
type ToolResultEntry struct {
	ToolUseID   string
	Timestamp   time.Time
	UUID        string
	IsError     bool
	Interrupted bool
}

// ParseError records a non-fatal parse error.
type ParseError struct {
	Line    int
	Message string
}

// ParseResult holds all extracted data from a JSONL session file.
type ParseResult struct {
	FilePath    string
	SessionID   string
	Version     string
	Model       string
	Messages    int
	Users       int
	Assistants  int
	Progress    int
	ToolUses    map[string]*ToolUseEntry    // id -> entry
	ToolResults map[string]*ToolResultEntry // tool_use_id -> entry
	StartTime   time.Time
	EndTime     time.Time
	Usages      []Usage
	Errors      []ParseError
}
