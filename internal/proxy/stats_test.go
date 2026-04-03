package proxy

import (
	"testing"
	"time"
)

func TestComputeStatsEmpty(t *testing.T) {
	r := ComputeStats(nil, StatsOptions{})
	if r.TotalRequests != 0 {
		t.Errorf("expected 0 requests, got %d", r.TotalRequests)
	}
}

func TestComputeStatsBasic(t *testing.T) {
	now := time.Now()
	entries := []ProxyEntry{
		{
			Timestamp:     now.Add(-2 * time.Hour),
			Model:         "claude-opus-4-6-20260401",
			Status:        200,
			Streaming:     true,
			LatencyMs:     2000,
			InputTokens:   100,
			OutputTokens:  50,
			CacheCreate:   500,
			CacheRead:     400,
			CacheRatio:    0.4,
			RequestBytes:  10000,
			ResponseBytes: 5000,
		},
		{
			Timestamp:     now.Add(-1 * time.Hour),
			Model:         "claude-opus-4-6-20260401",
			Status:        200,
			Streaming:     true,
			LatencyMs:     3000,
			InputTokens:   200,
			OutputTokens:  100,
			CacheCreate:   1000,
			CacheRead:     800,
			CacheRatio:    0.4,
			RequestBytes:  20000,
			ResponseBytes: 10000,
		},
		{
			Timestamp:     now.Add(-30 * time.Minute),
			Model:         "claude-sonnet-4-20250514",
			Status:        401,
			LatencyMs:     200,
			ErrorType:     "authentication_error",
			ErrorMessage:  "invalid key",
			RequestBytes:  100,
			ResponseBytes: 130,
		},
	}

	r := ComputeStats(entries, StatsOptions{})

	if r.TotalRequests != 3 {
		t.Errorf("total requests: got %d, want 3", r.TotalRequests)
	}
	if r.SuccessCount != 2 {
		t.Errorf("success: got %d, want 2", r.SuccessCount)
	}
	if r.ErrorCount != 1 {
		t.Errorf("errors: got %d, want 1", r.ErrorCount)
	}
	if r.StreamingCount != 2 {
		t.Errorf("streaming: got %d, want 2", r.StreamingCount)
	}
	if r.InputTokens != 300 {
		t.Errorf("input tokens: got %d, want 300", r.InputTokens)
	}
	if r.OutputTokens != 150 {
		t.Errorf("output tokens: got %d, want 150", r.OutputTokens)
	}
	if r.CacheCreate != 1500 {
		t.Errorf("cache create: got %d, want 1500", r.CacheCreate)
	}
	if r.CacheRead != 1200 {
		t.Errorf("cache read: got %d, want 1200", r.CacheRead)
	}

	// Weighted cache ratio: 1200 / (300 + 1500 + 1200) = 1200/3000 = 0.4
	if r.CacheRatio < 0.399 || r.CacheRatio > 0.401 {
		t.Errorf("cache ratio: got %f, want ~0.4", r.CacheRatio)
	}

	if r.MinLatencyMs != 200 {
		t.Errorf("min latency: got %d, want 200", r.MinLatencyMs)
	}
	if r.MaxLatencyMs != 3000 {
		t.Errorf("max latency: got %d, want 3000", r.MaxLatencyMs)
	}

	// Should have 2 models
	if len(r.ByModel) != 2 {
		t.Fatalf("by model: got %d entries, want 2", len(r.ByModel))
	}

	// Cost should be > 0 for opus entries
	if r.Cost.TotalCost <= 0 {
		t.Errorf("total cost should be > 0, got %f", r.Cost.TotalCost)
	}

	// Error types
	if r.ErrorTypes["authentication_error"] != 1 {
		t.Errorf("error types: expected 1 authentication_error, got %v", r.ErrorTypes)
	}
}

func TestComputeStatsTimeFilter(t *testing.T) {
	now := time.Now()
	entries := []ProxyEntry{
		{Timestamp: now.Add(-3 * time.Hour), Model: "claude-opus-4-6", Status: 200, LatencyMs: 1000},
		{Timestamp: now.Add(-30 * time.Minute), Model: "claude-opus-4-6", Status: 200, LatencyMs: 2000},
	}

	r := ComputeStats(entries, StatsOptions{Since: now.Add(-1 * time.Hour)})
	if r.TotalRequests != 1 {
		t.Errorf("filtered requests: got %d, want 1", r.TotalRequests)
	}
}

func TestParseDurationFlag(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"1h", false},
		{"24h", false},
		{"7d", false},
		{"30m", false},
		{"", false},
		{"bad", true},
		{"xd", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseDurationFlag(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if tt.input == "" {
				if !result.IsZero() {
					t.Errorf("empty input should return zero time")
				}
				return
			}
			if result.After(time.Now()) {
				t.Errorf("cutoff should be in the past")
			}
		})
	}
}

func TestLookupPricing(t *testing.T) {
	tests := []struct {
		model     string
		wantInput float64
	}{
		{"claude-opus-4-6-20260401", 15.0},
		{"claude-opus-4-6", 15.0},
		{"claude-sonnet-4-20250514", 3.0},
		{"claude-haiku-3-5-20241022", 0.25},
		{"unknown-model", 3.0}, // defaults to sonnet
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := lookupPricing(tt.model)
			if p.InputPerMTok != tt.wantInput {
				t.Errorf("input pricing for %q: got %f, want %f", tt.model, p.InputPerMTok, tt.wantInput)
			}
		})
	}
}

func TestComputeModelCost(t *testing.T) {
	// Opus: input $15/MTok, output $75/MTok
	// 1000 input tokens = 1000 * 15 / 1_000_000 = $0.015
	c := computeModelCost("claude-opus-4-6", 1000, 1000, 0, 0)
	if c.InputCost < 0.014 || c.InputCost > 0.016 {
		t.Errorf("input cost: got %f, want ~0.015", c.InputCost)
	}
	if c.OutputCost < 0.074 || c.OutputCost > 0.076 {
		t.Errorf("output cost: got %f, want ~0.075", c.OutputCost)
	}

	// Cache create = input_price * 1.25 = 15 * 1.25 = 18.75 per MTok
	c2 := computeModelCost("claude-opus-4-6", 0, 0, 1_000_000, 0)
	if c2.CacheCreateCost < 18.74 || c2.CacheCreateCost > 18.76 {
		t.Errorf("cache create cost: got %f, want ~18.75", c2.CacheCreateCost)
	}

	// Cache read = input_price * 0.1 = 15 * 0.1 = 1.5 per MTok
	c3 := computeModelCost("claude-opus-4-6", 0, 0, 0, 1_000_000)
	if c3.CacheReadCost < 1.49 || c3.CacheReadCost > 1.51 {
		t.Errorf("cache read cost: got %f, want ~1.5", c3.CacheReadCost)
	}
}

func TestComputeHourly(t *testing.T) {
	now := time.Now().Truncate(time.Hour)
	entries := []ProxyEntry{
		{Timestamp: now.Add(10 * time.Minute), Model: "x", Status: 200, LatencyMs: 100, InputTokens: 50},
		{Timestamp: now.Add(20 * time.Minute), Model: "x", Status: 200, LatencyMs: 200, InputTokens: 30},
		{Timestamp: now.Add(70 * time.Minute), Model: "x", Status: 200, LatencyMs: 300, InputTokens: 40},
	}

	buckets := computeHourly(entries)
	if len(buckets) != 2 {
		t.Fatalf("expected 2 hourly buckets, got %d", len(buckets))
	}
	if buckets[0].Requests != 2 {
		t.Errorf("first bucket: got %d requests, want 2", buckets[0].Requests)
	}
	if buckets[1].Requests != 1 {
		t.Errorf("second bucket: got %d requests, want 1", buckets[1].Requests)
	}
}
