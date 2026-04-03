package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sync/atomic"
	"time"
)

// sseInterceptor reads an SSE stream, extracts metrics from Anthropic events,
// and writes all bytes through to the downstream writer unchanged.
// This is zero-copy in the sense that we never buffer the full response — each
// line is scanned, inspected, and forwarded immediately.
type sseInterceptor struct {
	usage      Usage
	model      string
	requestID  string
	ttfb       time.Duration
	startTime  time.Time
	gotFirst   atomic.Bool
	firstByte  time.Time
	eventCount int
}

// intercept reads from src (the upstream SSE body), writes every byte to dst
// (the client), and extracts metrics from event lines along the way.
// Returns total bytes written and any error.
func (s *sseInterceptor) intercept(dst io.Writer, src io.Reader) (int64, error) {
	s.startTime = time.Now()

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB line buffer for large events
	var totalWritten int64
	var eventType string

	for scanner.Scan() {
		line := scanner.Bytes()

		// Record TTFB on the very first line.
		if s.gotFirst.CompareAndSwap(false, true) {
			s.firstByte = time.Now()
			s.ttfb = s.firstByte.Sub(s.startTime)
		}

		// Parse SSE event type and data lines.
		if bytes.HasPrefix(line, []byte("event: ")) {
			eventType = string(line[7:])
		} else if bytes.HasPrefix(line, []byte("data: ")) {
			data := line[6:]
			s.processEvent(eventType, data)
			s.eventCount++
		}

		// Write line + newline to client immediately.
		n, err := dst.Write(line)
		totalWritten += int64(n)
		if err != nil {
			return totalWritten, err
		}
		n, err = dst.Write([]byte("\n"))
		totalWritten += int64(n)
		if err != nil {
			return totalWritten, err
		}

		// Flush if the writer supports it.
		if f, ok := dst.(flusher); ok {
			f.Flush()
		}
	}

	return totalWritten, scanner.Err()
}

// flusher is the interface for http.Flusher without importing net/http.
type flusher interface {
	Flush()
}

// processEvent inspects a single SSE data payload and extracts metrics.
func (s *sseInterceptor) processEvent(eventType string, data []byte) {
	switch eventType {
	case "message_start":
		// Contains: {"type":"message_start","message":{"id":"...","model":"...","usage":{...}}}
		var ev struct {
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage Usage  `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &ev) == nil {
			s.requestID = ev.Message.ID
			s.model = ev.Message.Model
			// message_start carries input token counts.
			s.usage.InputTokens = ev.Message.Usage.InputTokens
			s.usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			s.usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
		}

	case "message_delta":
		// Contains: {"type":"message_delta","usage":{"output_tokens":N}}
		var ev struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &ev) == nil {
			s.usage.OutputTokens = ev.Usage.OutputTokens
		}
	}
}
