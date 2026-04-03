package proxy

import "testing"

func TestParseRequestMeta(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantMeta requestMeta
	}{
		{
			name: "basic non-streaming",
			body: `{"model":"claude-opus-4-6-20260401","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
			wantMeta: requestMeta{
				Model:    "claude-opus-4-6-20260401",
				Stream:   false,
				MsgCount: 1,
			},
		},
		{
			name: "streaming with tools",
			body: `{"model":"claude-sonnet-4-20250514","stream":true,"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}],"tools":[{"name":"bash"},{"name":"read"}]}`,
			wantMeta: requestMeta{
				Model:     "claude-sonnet-4-20250514",
				Stream:    true,
				MsgCount:  2,
				ToolCount: 2,
			},
		},
		{
			name: "empty body",
			body: ``,
			wantMeta: requestMeta{},
		},
		{
			name: "invalid json",
			body: `{broken`,
			wantMeta: requestMeta{},
		},
		{
			name: "no messages or tools",
			body: `{"model":"claude-opus-4-6","stream":true}`,
			wantMeta: requestMeta{
				Model:  "claude-opus-4-6",
				Stream: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRequestMeta([]byte(tt.body))
			if got.Model != tt.wantMeta.Model {
				t.Errorf("model = %q, want %q", got.Model, tt.wantMeta.Model)
			}
			if got.Stream != tt.wantMeta.Stream {
				t.Errorf("stream = %v, want %v", got.Stream, tt.wantMeta.Stream)
			}
			if got.MsgCount != tt.wantMeta.MsgCount {
				t.Errorf("msg_count = %d, want %d", got.MsgCount, tt.wantMeta.MsgCount)
			}
			if got.ToolCount != tt.wantMeta.ToolCount {
				t.Errorf("tool_count = %d, want %d", got.ToolCount, tt.wantMeta.ToolCount)
			}
		})
	}
}

func TestParseResponseUsage(t *testing.T) {
	body := `{"id":"msg_01X","type":"message","model":"claude-opus-4-6-20260401","usage":{"input_tokens":45000,"output_tokens":1200,"cache_creation_input_tokens":500,"cache_read_input_tokens":43800}}`
	usage, model, reqID := parseResponseUsage([]byte(body))

	if model != "claude-opus-4-6-20260401" {
		t.Errorf("model = %q, want claude-opus-4-6-20260401", model)
	}
	if reqID != "msg_01X" {
		t.Errorf("reqID = %q, want msg_01X", reqID)
	}
	if usage.InputTokens != 45000 {
		t.Errorf("input_tokens = %d, want 45000", usage.InputTokens)
	}
	if usage.OutputTokens != 1200 {
		t.Errorf("output_tokens = %d, want 1200", usage.OutputTokens)
	}
	if usage.CacheCreationInputTokens != 500 {
		t.Errorf("cache_create = %d, want 500", usage.CacheCreationInputTokens)
	}
	if usage.CacheReadInputTokens != 43800 {
		t.Errorf("cache_read = %d, want 43800", usage.CacheReadInputTokens)
	}
}

func TestParseErrorResponse(t *testing.T) {
	body := `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	errType, errMsg := parseErrorResponse([]byte(body))
	if errType != "overloaded_error" {
		t.Errorf("error type = %q, want overloaded_error", errType)
	}
	if errMsg != "Overloaded" {
		t.Errorf("error msg = %q, want Overloaded", errMsg)
	}
}

func TestCountJSONArray(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`[]`, 0},
		{`[1]`, 1},
		{`[1,2,3]`, 3},
		{`[{"a":1},{"b":2}]`, 2},
		{`[{"a":[1,2]},{"b":{"c":3}}]`, 2},
		{`["hello","world"]`, 2},
		{`["a,b","c"]`, 2},
		{`not-json`, 0},
		{`{}`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := countJSONArray([]byte(tt.input))
			if got != tt.want {
				t.Errorf("countJSONArray(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
