package analyzer

import (
	"sort"
	"time"

	"github.com/kolkov/ccdiag/internal/parser"
)

// ToolCall is a parsed tool_use with its matching result.
type ToolCall struct {
	ID          string
	Name        string
	UseTime     time.Time
	UseUUID     string
	ResultTime  time.Time
	ResultUUID  string
	HasResult   bool
	IsError     bool
	Interrupted bool
	Duration    time.Duration
}

// OrphanInfo describes an orphaned tool_use or tool_result.
type OrphanInfo struct {
	Type      string // "tool_use" or "tool_result"
	ID        string
	ToolName  string
	Timestamp time.Time
	UUID      string
}

// SessionSummary holds aggregate session stats.
type SessionSummary struct {
	SessionID      string
	FilePath       string
	Version        string
	Model          string
	StartTime      time.Time
	EndTime        time.Time
	Duration       time.Duration
	TotalMessages  int
	UserMessages   int
	AssistMessages int
	ProgressMsgs   int
	TotalToolCalls int
	OrphanUses     int
	OrphanResults  int
	Errors         int
	Interrupted    int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalCacheCreate  int
	TotalCacheRead    int
	ToolStats      map[string]*ToolStat
}

// ToolStat holds per-tool statistics.
type ToolStat struct {
	Name      string
	Count     int
	Errors    int
	Orphans   int
	TotalDur  time.Duration
	MinDur    time.Duration
	MaxDur    time.Duration
	Durations []time.Duration
}

// Result holds the complete analysis output.
type Result struct {
	Summary    SessionSummary
	Orphans    []OrphanInfo
	ToolCalls  []ToolCall
	StuckCalls []ToolCall
	ErrorCalls []ToolCall
	TopSlowest []ToolCall
}

// Analyze performs full analysis on parsed session data.
func Analyze(pr *parser.ParseResult, stuckThreshold time.Duration) *Result {
	ar := &Result{}

	ar.ToolCalls, ar.Orphans = matchToolCalls(pr)

	toolStats := make(map[string]*ToolStat)
	var errors, interrupted int

	for i := range ar.ToolCalls {
		tc := &ar.ToolCalls[i]

		stat, ok := toolStats[tc.Name]
		if !ok {
			stat = &ToolStat{Name: tc.Name}
			toolStats[tc.Name] = stat
		}
		stat.Count++

		if tc.HasResult {
			stat.Durations = append(stat.Durations, tc.Duration)
			stat.TotalDur += tc.Duration
			if stat.MinDur == 0 || tc.Duration < stat.MinDur {
				stat.MinDur = tc.Duration
			}
			if tc.Duration > stat.MaxDur {
				stat.MaxDur = tc.Duration
			}
			if tc.Duration >= stuckThreshold {
				ar.StuckCalls = append(ar.StuckCalls, *tc)
			}
		}

		if tc.IsError {
			errors++
			stat.Errors++
			ar.ErrorCalls = append(ar.ErrorCalls, *tc)
		}
		if tc.Interrupted {
			interrupted++
		}
	}

	for _, o := range ar.Orphans {
		if o.Type == "tool_use" {
			if stat, ok := toolStats[o.ToolName]; ok {
				stat.Orphans++
			}
		}
	}

	ar.TopSlowest = topSlowest(ar.ToolCalls, 10)

	var totalIn, totalOut, totalCacheCreate, totalCacheRead int
	for _, u := range pr.Usages {
		totalIn += u.InputTokens
		totalOut += u.OutputTokens
		totalCacheCreate += u.CacheCreationInputTokens
		totalCacheRead += u.CacheReadInputTokens
	}

	ar.Summary = SessionSummary{
		SessionID:         pr.SessionID,
		FilePath:          pr.FilePath,
		Version:           pr.Version,
		Model:             pr.Model,
		StartTime:         pr.StartTime,
		EndTime:           pr.EndTime,
		Duration:          pr.EndTime.Sub(pr.StartTime),
		TotalMessages:     pr.Messages,
		UserMessages:      pr.Users,
		AssistMessages:    pr.Assistants,
		ProgressMsgs:      pr.Progress,
		TotalToolCalls:    len(ar.ToolCalls),
		OrphanUses:        countOrphanType(ar.Orphans, "tool_use"),
		OrphanResults:     countOrphanType(ar.Orphans, "tool_result"),
		Errors:            errors,
		Interrupted:       interrupted,
		TotalInputTokens:  totalIn,
		TotalOutputTokens: totalOut,
		TotalCacheCreate:  totalCacheCreate,
		TotalCacheRead:    totalCacheRead,
		ToolStats:         toolStats,
	}

	return ar
}

// MedianDuration computes the median of a duration slice.
func MedianDuration(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	return sorted[mid]
}

// AvgDuration computes the average duration.
func AvgDuration(total time.Duration, count int) time.Duration {
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func matchToolCalls(pr *parser.ParseResult) ([]ToolCall, []OrphanInfo) {
	var calls []ToolCall
	var orphans []OrphanInfo

	matched := make(map[string]bool)
	for id, use := range pr.ToolUses {
		tc := ToolCall{
			ID:      id,
			Name:    use.Name,
			UseTime: use.Timestamp,
			UseUUID: use.UUID,
		}

		if res, ok := pr.ToolResults[id]; ok {
			tc.HasResult = true
			tc.ResultTime = res.Timestamp
			tc.ResultUUID = res.UUID
			tc.IsError = res.IsError
			tc.Interrupted = res.Interrupted
			tc.Duration = res.Timestamp.Sub(use.Timestamp)
			if tc.Duration < 0 {
				tc.Duration = 0
			}
			matched[id] = true
		} else {
			orphans = append(orphans, OrphanInfo{
				Type:      "tool_use",
				ID:        id,
				ToolName:  use.Name,
				Timestamp: use.Timestamp,
				UUID:      use.UUID,
			})
		}
		calls = append(calls, tc)
	}

	for id, res := range pr.ToolResults {
		if _, ok := pr.ToolUses[id]; !ok {
			orphans = append(orphans, OrphanInfo{
				Type:      "tool_result",
				ID:        id,
				Timestamp: res.Timestamp,
				UUID:      res.UUID,
			})
		}
	}

	sort.Slice(orphans, func(i, j int) bool {
		return orphans[i].Timestamp.Before(orphans[j].Timestamp)
	})

	return calls, orphans
}

func topSlowest(calls []ToolCall, n int) []ToolCall {
	var matched []ToolCall
	for _, c := range calls {
		if c.HasResult {
			matched = append(matched, c)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Duration > matched[j].Duration
	})
	if len(matched) > n {
		matched = matched[:n]
	}
	return matched
}

func countOrphanType(orphans []OrphanInfo, typ string) int {
	count := 0
	for _, o := range orphans {
		if o.Type == typ {
			count++
		}
	}
	return count
}
