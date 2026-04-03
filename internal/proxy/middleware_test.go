package proxy

import "testing"

func TestShortModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-opus-4-6-20260401", "claude-opus-4-6"},
		{"claude-sonnet-4-20250514", "claude-sonnet-4"},
		{"claude-opus-4-6", "claude-opus-4-6"},
		{"gpt-4", "gpt-4"},
		{"", ""},
		{"short", "short"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shortModel(tt.input)
			if got != tt.want {
				t.Errorf("shortModel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0K"},
		{1200, "1.2K"},
		{45000, "45.0K"},
		{150000, "150.0K"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCacheRatio(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
		want  float64
	}{
		{
			name:  "zero total",
			usage: Usage{},
			want:  0,
		},
		{
			name: "full cache",
			usage: Usage{
				InputTokens:          0,
				CacheReadInputTokens: 1000,
			},
			want: 1.0,
		},
		{
			name: "typical",
			usage: Usage{
				InputTokens:              1000,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     43800,
			},
			want: float64(43800) / float64(45000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheRatio(tt.usage)
			diff := got - tt.want
			if diff < -0.001 || diff > 0.001 {
				t.Errorf("cacheRatio = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestIsMessagesEndpoint(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/v1/messages", true},
		{"/v1/messages?beta=true", true},
		{"/v1/completions", false},
		{"/health", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isMessagesEndpoint(tt.path)
			if got != tt.want {
				t.Errorf("isMessagesEndpoint(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
