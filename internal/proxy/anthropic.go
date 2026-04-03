package proxy

import (
	"bytes"
	"encoding/json"
)

// parseRequestMeta extracts model, stream flag, tool count, and message count
// from an Anthropic Messages API request body. The body is NOT consumed — a copy
// is used so the original request can be forwarded unchanged.
func parseRequestMeta(body []byte) requestMeta {
	var m requestMeta

	// Fast path: use a lightweight struct to avoid parsing the full body.
	var req struct {
		Model    string          `json:"model"`
		Stream   bool            `json:"stream"`
		Tools    json.RawMessage `json:"tools"`
		Messages json.RawMessage `json:"messages"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		return m
	}

	m.Model = req.Model
	m.Stream = req.Stream

	// Count tools — just count top-level array elements.
	if len(req.Tools) > 0 {
		m.ToolCount = countJSONArray(req.Tools)
	}

	// Count messages — just count top-level array elements.
	if len(req.Messages) > 0 {
		m.MsgCount = countJSONArray(req.Messages)
	}

	// Extract session_id from metadata.user_id (embedded JSON string).
	// Format: "session_id":"<uuid>" — fast scan, no extra JSON parse.
	m.SessionID = extractSessionID(body)

	return m
}

// extractSessionID finds session_id in the request body via byte scan.
// Claude Code sends metadata.user_id as a double-encoded JSON string, so
// the inner quotes are escaped: \"session_id\":\"<uuid>\"
func extractSessionID(body []byte) string {
	// Try escaped form first (double-encoded JSON string — the common case).
	marker := []byte(`\"session_id\":\"`)
	idx := bytes.Index(body, marker)
	if idx >= 0 {
		start := idx + len(marker)
		if start >= len(body) {
			return ""
		}
		end := bytes.Index(body[start:], []byte(`\"`))
		if end > 0 && end <= 64 {
			return string(body[start : start+end])
		}
	}

	// Fallback: unescaped form (in case metadata is sent as a nested object).
	marker = []byte(`"session_id":"`)
	idx = bytes.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	if start >= len(body) {
		return ""
	}
	end := bytes.IndexByte(body[start:], '"')
	if end < 0 || end > 64 {
		return ""
	}
	return string(body[start : start+end])
}

// parseResponseUsage extracts the usage block from a non-streaming Anthropic
// Messages API response.
func parseResponseUsage(body []byte) (Usage, string, string) {
	var resp struct {
		Model string `json:"model"`
		ID    string `json:"id"`
		Usage Usage  `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}, "", ""
	}
	return resp.Usage, resp.Model, resp.ID
}

// parseErrorResponse extracts error type and message from an Anthropic error response.
func parseErrorResponse(body []byte) (string, string) {
	var resp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", ""
	}
	return resp.Error.Type, resp.Error.Message
}

// countJSONArray counts top-level elements in a JSON array without fully parsing.
// Returns 0 for invalid input or empty arrays.
func countJSONArray(data json.RawMessage) int {
	data = bytes.TrimSpace(data)
	if len(data) < 2 || data[0] != '[' {
		return 0
	}

	// Check for empty array.
	inner := bytes.TrimSpace(data[1 : len(data)-1])
	if len(inner) == 0 {
		return 0
	}

	// Count commas at depth 0. Elements = commas + 1.
	commas := 0
	depth := 0
	inString := false
	escaped := false

	for _, b := range inner {
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' && inString {
			escaped = true
			continue
		}
		if b == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch b {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				commas++
			}
		}
	}

	return commas + 1
}
