package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// middleware wraps an http.Handler and captures request/response metrics
// for Anthropic Messages API traffic.
type middleware struct {
	upstream *url.URL
	logger   *Logger
	verbose  bool
}

// countingWriter wraps an http.ResponseWriter and counts bytes written.
type countingWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

func (cw *countingWriter) WriteHeader(code int) {
	cw.status = code
	cw.ResponseWriter.WriteHeader(code)
}

func (cw *countingWriter) Write(b []byte) (int, error) {
	n, err := cw.ResponseWriter.Write(b)
	cw.written += int64(n)
	return n, err
}

func (cw *countingWriter) Flush() {
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// wrap returns an http.Handler that captures metrics around the inner handler.
func (mw *middleware) wrap(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Only intercept Anthropic Messages API calls.
		if !isMessagesEndpoint(r.URL.Path) {
			inner.ServeHTTP(w, r)
			return
		}

		// Read request body for metadata extraction, then restore it.
		var reqBody []byte
		var reqSize int64
		if r.Body != nil {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body.Close()
			reqSize = int64(len(reqBody))
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		meta := parseRequestMeta(reqBody)

		if meta.Stream {
			mw.handleStreaming(w, r, meta, reqSize, start)
		} else {
			mw.handleNonStreaming(w, r, inner, meta, reqSize, start)
		}
	})
}

// handleStreaming intercepts SSE streaming responses.
func (mw *middleware) handleStreaming(w http.ResponseWriter, r *http.Request, meta requestMeta, reqSize int64, start time.Time) {
	// Make the upstream request ourselves so we can intercept the stream.
	// DisableCompression prevents Go from adding Accept-Encoding: gzip
	// and auto-decompressing — we need raw SSE text to parse events.
	client := &http.Client{
		Transport: &http.Transport{
			DisableCompression: true,
		},
	}
	upstreamURL := fmt.Sprintf("%s://%s%s", mw.upstream.Scheme, mw.upstream.Host, r.URL.RequestURI())
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	copyHeaders(upstreamReq.Header, r.Header)
	// Remove Accept-Encoding so upstream sends uncompressed SSE text.
	// We need raw text to parse events line-by-line.
	upstreamReq.Header.Del("Accept-Encoding")

	resp, err := client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// If not 200, just forward the body.
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		w.Write(body)
		errType, errMsg := parseErrorResponse(body)
		mw.logEntry(ProxyEntry{
			Timestamp:    start,
			Model:        meta.Model,
			Status:       resp.StatusCode,
			Streaming:    true,
			LatencyMs:    time.Since(start).Milliseconds(),
			RequestBytes: reqSize,
			ResponseBytes: int64(len(body)),
			SessionID:    meta.SessionID,
			ToolCount:    meta.ToolCount,
			MsgCount:     meta.MsgCount,
			ErrorType:    errType,
			ErrorMessage: errMsg,
		})
		return
	}

	// SSE interception: read upstream, extract metrics, forward to client.
	interceptor := &sseInterceptor{}
	written, _ := interceptor.intercept(w, resp.Body)
	latency := time.Since(start)
	usage := interceptor.usage
	model := interceptor.model
	if model == "" {
		model = meta.Model
	}

	entry := ProxyEntry{
		Timestamp:     start,
		SessionID:     meta.SessionID,
		RequestID:     interceptor.requestID,
		Model:         model,
		Status:        resp.StatusCode,
		Streaming:     true,
		LatencyMs:     latency.Milliseconds(),
		TTFBMs:        interceptor.ttfb.Milliseconds(),
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CacheCreate:   usage.CacheCreationInputTokens,
		CacheRead:     usage.CacheReadInputTokens,
		CacheRatio:    cacheRatio(usage),
		RequestBytes:  reqSize,
		ResponseBytes: written,
		ToolCount:     meta.ToolCount,
		MsgCount:      meta.MsgCount,
	}

	mw.logEntry(entry)
}

// handleNonStreaming intercepts regular JSON responses via ModifyResponse-style capture.
func (mw *middleware) handleNonStreaming(w http.ResponseWriter, r *http.Request, inner http.Handler, meta requestMeta, reqSize int64, start time.Time) {
	cw := &countingWriter{ResponseWriter: w, status: 200}
	rec := &responseRecorder{ResponseWriter: cw, body: &bytes.Buffer{}}

	inner.ServeHTTP(rec, r)

	latency := time.Since(start)
	body := rec.body.Bytes()

	entry := ProxyEntry{
		Timestamp:     start,
		SessionID:     meta.SessionID,
		Model:         meta.Model,
		Status:        cw.status,
		Streaming:     false,
		LatencyMs:     latency.Milliseconds(),
		RequestBytes:  reqSize,
		ResponseBytes: int64(len(body)),
		ToolCount:     meta.ToolCount,
		MsgCount:      meta.MsgCount,
	}

	if cw.status == http.StatusOK {
		usage, model, reqID := parseResponseUsage(body)
		if model != "" {
			entry.Model = model
		}
		entry.RequestID = reqID
		entry.InputTokens = usage.InputTokens
		entry.OutputTokens = usage.OutputTokens
		entry.CacheCreate = usage.CacheCreationInputTokens
		entry.CacheRead = usage.CacheReadInputTokens
		entry.CacheRatio = cacheRatio(usage)
	} else {
		entry.ErrorType, entry.ErrorMessage = parseErrorResponse(body)
	}

	mw.logEntry(entry)
}

func (mw *middleware) logEntry(entry ProxyEntry) {
	if mw.verbose {
		printStderr(entry)
	}

	if mw.logger != nil {
		if err := mw.logger.Log(entry); err != nil {
			fmt.Fprintf(stderr, "log error: %v\n", err)
		}
	}
}

// responseRecorder captures the response body while also writing it through.
type responseRecorder struct {
	http.ResponseWriter
	body *bytes.Buffer
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	rr.body.Write(b)
	return rr.ResponseWriter.Write(b)
}

// isMessagesEndpoint checks if the path looks like an Anthropic Messages API call.
func isMessagesEndpoint(path string) bool {
	return strings.HasSuffix(path, "/messages") || strings.Contains(path, "/messages?")
}

// hopByHop lists headers that should not be forwarded by proxies.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// copyHeaders copies headers from src to dst, skipping hop-by-hop headers.
func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// cacheRatio computes cache read ratio: cache_read / (input + cache_create + cache_read).
func cacheRatio(u Usage) float64 {
	total := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	if total == 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(total)
}

// printStderr outputs a one-line summary for a request.
func printStderr(e ProxyEntry) {
	ts := e.Timestamp.Format("15:04:05")
	model := shortModel(e.Model)
	cache := fmt.Sprintf("%.1f%%", e.CacheRatio*100)

	inTok := formatTokens(e.InputTokens)
	outTok := formatTokens(e.OutputTokens)

	dur := fmt.Sprintf("%.1fs", float64(e.LatencyMs)/1000)

	if e.Status != http.StatusOK {
		fmt.Fprintf(stderr, "[%s] %s | %d %s | %s\n", ts, model, e.Status, e.ErrorType, dur)
		return
	}

	fmt.Fprintf(stderr, "[%s] %s | %s in | %s out | cache %s | %s\n",
		ts, model, inTok, outTok, cache, dur)
}

// stderr is a writer for real-time output; package-level for testability.
var stderr io.Writer = os.Stderr

// shortModel trims model name for display: "claude-opus-4-6-20260401" -> "claude-opus-4-6".
func shortModel(model string) string {
	// Trim date suffix like -20260401.
	if len(model) > 9 {
		suffix := model[len(model)-9:]
		if len(suffix) == 9 && suffix[0] == '-' {
			allDigit := true
			for _, c := range suffix[1:] {
				if c < '0' || c > '9' {
					allDigit = false
					break
				}
			}
			if allDigit {
				model = model[:len(model)-9]
			}
		}
	}
	return model
}

// formatTokens formats token counts for display: 45000 -> "45.0K", 1200 -> "1.2K", 500 -> "500".
func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
