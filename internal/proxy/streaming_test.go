package proxy

import (
	"bytes"
	"strings"
	"testing"
)

func TestSSEInterceptor(t *testing.T) {
	// Simulate a minimal Anthropic SSE stream.
	sseStream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_01ABC","model":"claude-opus-4-6-20260401","usage":{"input_tokens":45000,"output_tokens":0,"cache_creation_input_tokens":500,"cache_read_input_tokens":43800}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	var dst bytes.Buffer
	src := strings.NewReader(sseStream)

	interceptor := &sseInterceptor{}
	written, err := interceptor.intercept(&dst, src)
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}

	// Verify metrics extracted.
	if interceptor.requestID != "msg_01ABC" {
		t.Errorf("requestID = %q, want msg_01ABC", interceptor.requestID)
	}
	if interceptor.model != "claude-opus-4-6-20260401" {
		t.Errorf("model = %q, want claude-opus-4-6-20260401", interceptor.model)
	}
	if interceptor.usage.InputTokens != 45000 {
		t.Errorf("input_tokens = %d, want 45000", interceptor.usage.InputTokens)
	}
	if interceptor.usage.OutputTokens != 42 {
		t.Errorf("output_tokens = %d, want 42", interceptor.usage.OutputTokens)
	}
	if interceptor.usage.CacheCreationInputTokens != 500 {
		t.Errorf("cache_create = %d, want 500", interceptor.usage.CacheCreationInputTokens)
	}
	if interceptor.usage.CacheReadInputTokens != 43800 {
		t.Errorf("cache_read = %d, want 43800", interceptor.usage.CacheReadInputTokens)
	}

	// Verify TTFB was recorded (may be 0 for in-memory reads).
	if !interceptor.gotFirst.Load() {
		t.Error("should have recorded first byte")
	}

	// Verify all bytes forwarded (every line + newline).
	if written == 0 {
		t.Error("written should be > 0")
	}

	// Verify output contains the original SSE events.
	output := dst.String()
	if !strings.Contains(output, "event: message_start") {
		t.Error("output should contain event: message_start")
	}
	if !strings.Contains(output, "Hello") {
		t.Error("output should contain forwarded text delta")
	}
}

func TestSSEInterceptorEmptyStream(t *testing.T) {
	var dst bytes.Buffer
	src := strings.NewReader("")

	interceptor := &sseInterceptor{}
	_, err := interceptor.intercept(&dst, src)
	if err != nil {
		t.Fatalf("intercept error: %v", err)
	}

	if interceptor.usage.InputTokens != 0 {
		t.Errorf("expected zero tokens for empty stream")
	}
}
