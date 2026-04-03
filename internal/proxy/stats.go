package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ModelPricing holds per-million-token prices in USD.
type ModelPricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// modelPricing maps short model name prefix to pricing.
// Cache read = InputPerMTok * 0.1, cache create = InputPerMTok * 1.25.
var modelPricing = map[string]ModelPricing{
	"claude-opus":   {InputPerMTok: 15.0, OutputPerMTok: 75.0},
	"claude-sonnet": {InputPerMTok: 3.0, OutputPerMTok: 15.0},
	"claude-haiku":  {InputPerMTok: 0.25, OutputPerMTok: 1.25},
}

// lookupPricing returns pricing for a model name by matching prefixes.
func lookupPricing(model string) ModelPricing {
	short := shortModel(model)
	for prefix, p := range modelPricing {
		if len(short) >= len(prefix) && short[:len(prefix)] == prefix {
			return p
		}
	}
	// Default to sonnet pricing for unknown models.
	return modelPricing["claude-sonnet"]
}

// CostBreakdown holds estimated costs.
type CostBreakdown struct {
	InputCost       float64 `json:"input_cost"`
	OutputCost      float64 `json:"output_cost"`
	CacheCreateCost float64 `json:"cache_create_cost"`
	CacheReadCost   float64 `json:"cache_read_cost"`
	TotalCost       float64 `json:"total_cost"`
}

// ModelStats holds per-model aggregation.
type ModelStats struct {
	Model        string        `json:"model"`
	Requests     int           `json:"requests"`
	Errors       int           `json:"errors"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CacheCreate  int           `json:"cache_create"`
	CacheRead    int           `json:"cache_read"`
	CacheRatio   float64       `json:"cache_ratio"`
	TotalLatency time.Duration `json:"-"`
	AvgLatencyMs float64       `json:"avg_latency_ms"`
	Cost         CostBreakdown `json:"cost"`
}

// HourBucket holds one hour of aggregation.
type HourBucket struct {
	Hour     time.Time `json:"hour"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
	Tokens   int       `json:"tokens"` // input + output
}

// StatsResult is the full computed stats output.
type StatsResult struct {
	// Period
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	FilterDesc  string    `json:"filter,omitempty"`

	// Summary
	TotalRequests  int     `json:"total_requests"`
	SuccessCount   int     `json:"success_count"`
	ErrorCount     int     `json:"error_count"`
	StreamingCount int     `json:"streaming_count"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	MinLatencyMs   int64   `json:"min_latency_ms"`
	MaxLatencyMs   int64   `json:"max_latency_ms"`

	// Bytes
	TotalRequestBytes  int64 `json:"total_request_bytes"`
	TotalResponseBytes int64 `json:"total_response_bytes"`

	// Tokens
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheCreate  int     `json:"cache_create"`
	CacheRead    int     `json:"cache_read"`
	CacheRatio   float64 `json:"cache_ratio"` // weighted by input tokens

	// Cost
	Cost CostBreakdown `json:"cost"`

	// Per-model
	ByModel []ModelStats `json:"by_model"`

	// Hourly histogram
	Hourly []HourBucket `json:"hourly,omitempty"`

	// Anomalies
	LowCacheCount int `json:"low_cache_count"` // cache_ratio < 0.5 among non-error requests with tokens
	ErrorTypes    map[string]int `json:"error_types,omitempty"`
}

// StatsOptions controls stats computation.
type StatsOptions struct {
	Since    time.Time // filter entries after this time (zero = no filter)
	ShowCost bool
}

// LoadEntries reads traffic.jsonl from the default or specified directory.
// Returns entries and the file path used, or an error.
// LoadEntries reads all per-session JSONL files from the proxy log directory.
// Also reads legacy traffic.jsonl for backwards compatibility.
func LoadEntries(logDir string) ([]ProxyEntry, string, error) {
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", fmt.Errorf("find home dir: %w", err)
		}
		logDir = filepath.Join(home, ".ccdiag", "proxy")
	}

	dirEntries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, logDir, nil
		}
		return nil, logDir, fmt.Errorf("read log dir: %w", err)
	}

	var entries []ProxyEntry
	for _, de := range dirEntries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".jsonl" {
			continue
		}
		fileEntries, err := loadJSONLFile(filepath.Join(logDir, de.Name()))
		if err != nil {
			continue // skip unreadable files
		}
		entries = append(entries, fileEntries...)
	}

	// Sort by timestamp
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	return entries, logDir, nil
}

// loadJSONLFile reads ProxyEntry records from a single JSONL file.
func loadJSONLFile(path string) ([]ProxyEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []ProxyEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e ProxyEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	return entries, scanner.Err()
}

// ComputeStats aggregates entries into a StatsResult.
func ComputeStats(entries []ProxyEntry, opts StatsOptions) StatsResult {
	var filtered []ProxyEntry
	for i := range entries {
		if !opts.Since.IsZero() && entries[i].Timestamp.Before(opts.Since) {
			continue
		}
		filtered = append(filtered, entries[i])
	}

	var r StatsResult
	if len(filtered) == 0 {
		return r
	}

	r.TotalRequests = len(filtered)
	r.MinLatencyMs = math.MaxInt64
	r.ErrorTypes = make(map[string]int)

	// Per-model accumulators
	modelMap := make(map[string]*ModelStats)

	var totalLatency int64

	for i := range filtered {
		e := &filtered[i]

		// Period
		if r.PeriodStart.IsZero() || e.Timestamp.Before(r.PeriodStart) {
			r.PeriodStart = e.Timestamp
		}
		if e.Timestamp.After(r.PeriodEnd) {
			r.PeriodEnd = e.Timestamp
		}

		// Success / Error
		if e.ErrorType != "" {
			r.ErrorCount++
		} else {
			r.SuccessCount++
		}
		if e.Streaming {
			r.StreamingCount++
		}

		// Latency
		totalLatency += e.LatencyMs
		if e.LatencyMs < r.MinLatencyMs {
			r.MinLatencyMs = e.LatencyMs
		}
		if e.LatencyMs > r.MaxLatencyMs {
			r.MaxLatencyMs = e.LatencyMs
		}

		// Bytes
		r.TotalRequestBytes += e.RequestBytes
		r.TotalResponseBytes += e.ResponseBytes

		// Tokens
		r.InputTokens += e.InputTokens
		r.OutputTokens += e.OutputTokens
		r.CacheCreate += e.CacheCreate
		r.CacheRead += e.CacheRead

		// Error types
		if e.ErrorType != "" {
			r.ErrorTypes[e.ErrorType]++
		}

		// Anomaly: low cache
		totalInput := e.InputTokens + e.CacheCreate + e.CacheRead
		if e.ErrorType == "" && totalInput > 0 && e.CacheRatio < 0.5 {
			r.LowCacheCount++
		}

		// Per-model
		model := shortModel(e.Model)
		ms, ok := modelMap[model]
		if !ok {
			ms = &ModelStats{Model: model}
			modelMap[model] = ms
		}
		ms.Requests++
		if e.ErrorType != "" {
			ms.Errors++
		}
		ms.InputTokens += e.InputTokens
		ms.OutputTokens += e.OutputTokens
		ms.CacheCreate += e.CacheCreate
		ms.CacheRead += e.CacheRead
		ms.TotalLatency += time.Duration(e.LatencyMs) * time.Millisecond
	}

	// Averages
	if r.TotalRequests > 0 {
		r.AvgLatencyMs = float64(totalLatency) / float64(r.TotalRequests)
	}
	if r.MinLatencyMs == math.MaxInt64 {
		r.MinLatencyMs = 0
	}

	// Weighted cache ratio: cache_read / (input + cache_create + cache_read)
	totalInputAll := r.InputTokens + r.CacheCreate + r.CacheRead
	if totalInputAll > 0 {
		r.CacheRatio = float64(r.CacheRead) / float64(totalInputAll)
	}

	// Cost
	r.Cost = computeCostFromEntries(filtered)

	// Per-model finalize
	for _, ms := range modelMap {
		if ms.Requests > 0 {
			ms.AvgLatencyMs = float64(ms.TotalLatency.Milliseconds()) / float64(ms.Requests)
		}
		totalIn := ms.InputTokens + ms.CacheCreate + ms.CacheRead
		if totalIn > 0 {
			ms.CacheRatio = float64(ms.CacheRead) / float64(totalIn)
		}
		ms.Cost = computeModelCost(ms.Model, ms.InputTokens, ms.OutputTokens, ms.CacheCreate, ms.CacheRead)
		r.ByModel = append(r.ByModel, *ms)
	}
	// Sort by request count descending, then by model name for stability.
	sort.Slice(r.ByModel, func(i, j int) bool {
		if r.ByModel[i].Requests != r.ByModel[j].Requests {
			return r.ByModel[i].Requests > r.ByModel[j].Requests
		}
		return r.ByModel[i].Model < r.ByModel[j].Model
	})

	// Hourly histogram
	r.Hourly = computeHourly(filtered)

	// Filter desc
	if !opts.Since.IsZero() {
		r.FilterDesc = fmt.Sprintf("since %s", opts.Since.Local().Format("2006-01-02 15:04"))
	}

	return r
}

func computeCostFromEntries(entries []ProxyEntry) CostBreakdown {
	var c CostBreakdown
	for i := range entries {
		e := &entries[i]
		mc := computeModelCost(e.Model, e.InputTokens, e.OutputTokens, e.CacheCreate, e.CacheRead)
		c.InputCost += mc.InputCost
		c.OutputCost += mc.OutputCost
		c.CacheCreateCost += mc.CacheCreateCost
		c.CacheReadCost += mc.CacheReadCost
	}
	c.TotalCost = c.InputCost + c.OutputCost + c.CacheCreateCost + c.CacheReadCost
	return c
}

func computeModelCost(model string, input, output, cacheCreate, cacheRead int) CostBreakdown {
	p := lookupPricing(model)
	mtok := 1_000_000.0
	c := CostBreakdown{
		InputCost:       float64(input) * p.InputPerMTok / mtok,
		OutputCost:      float64(output) * p.OutputPerMTok / mtok,
		CacheCreateCost: float64(cacheCreate) * p.InputPerMTok * 1.25 / mtok,
		CacheReadCost:   float64(cacheRead) * p.InputPerMTok * 0.1 / mtok,
	}
	c.TotalCost = c.InputCost + c.OutputCost + c.CacheCreateCost + c.CacheReadCost
	return c
}

func computeHourly(entries []ProxyEntry) []HourBucket {
	if len(entries) == 0 {
		return nil
	}

	buckets := make(map[time.Time]*HourBucket)
	for i := range entries {
		e := &entries[i]
		hour := e.Timestamp.Local().Truncate(time.Hour)
		b, ok := buckets[hour]
		if !ok {
			b = &HourBucket{Hour: hour}
			buckets[hour] = b
		}
		b.Requests++
		if e.ErrorType != "" {
			b.Errors++
		}
		b.Tokens += e.InputTokens + e.OutputTokens
	}

	result := make([]HourBucket, 0, len(buckets))
	for _, b := range buckets {
		result = append(result, *b)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Hour.Before(result[j].Hour)
	})
	return result
}

// ParseDurationFlag parses time filter strings like "1h", "24h", "7d".
// Returns the cutoff time (now - duration).
func ParseDurationFlag(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}

	// Handle "Nd" shorthand for days.
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days := s[:len(s)-1]
		var n int
		for _, c := range days {
			if c < '0' || c > '9' {
				return time.Time{}, fmt.Errorf("invalid duration: %q", s)
			}
			n = n*10 + int(c-'0')
		}
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour), nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return time.Now().Add(-d), nil
}
