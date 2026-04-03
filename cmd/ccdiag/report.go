package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kolkov/ccdiag/internal/analyzer"
)

// ANSI color codes
const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
)

// ReportOptions controls report output.
type ReportOptions struct {
	JSONOutput   bool
	Verbose      bool
	OrphansOnly  bool
	NoColor      bool
}

// PrintReport outputs the analysis result.
func PrintReport(ar *analyzer.Result, opts ReportOptions) {
	if opts.JSONOutput {
		printJSON(ar)
		return
	}

	if opts.NoColor {
		printPlain(ar, opts)
	} else {
		printColored(ar, opts)
	}
}

func printJSON(ar *analyzer.Result) {
	type jsonReport struct {
		Summary struct {
			SessionID  string `json:"session_id"`
			Version    string `json:"version"`
			Model      string `json:"model"`
			StartTime  string `json:"start_time"`
			EndTime    string `json:"end_time"`
			Duration   string `json:"duration"`
			Messages   int    `json:"total_messages"`
			ToolCalls  int    `json:"total_tool_calls"`
			OrphanUses int    `json:"orphan_tool_uses"`
			OrphanResults int `json:"orphan_tool_results"`
			Errors     int    `json:"errors"`
			Interrupted int   `json:"interrupted"`
			Tokens     struct {
				Input       int `json:"input"`
				Output      int `json:"output"`
				CacheCreate int `json:"cache_create"`
				CacheRead   int `json:"cache_read"`
			} `json:"tokens"`
		} `json:"summary"`
		Orphans []struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			ToolName string `json:"tool_name,omitempty"`
			Time     string `json:"timestamp"`
			UUID     string `json:"uuid"`
		} `json:"orphans"`
		StuckCalls []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Duration string `json:"duration"`
			Time     string `json:"timestamp"`
		} `json:"stuck_calls"`
		ErrorCalls []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Time string `json:"timestamp"`
		} `json:"error_calls"`
		ToolStats map[string]struct {
			Count   int    `json:"count"`
			Errors  int    `json:"errors"`
			Orphans int    `json:"orphans"`
			AvgDur  string `json:"avg_duration"`
			MaxDur  string `json:"max_duration"`
		} `json:"tool_stats"`
	}

	var report jsonReport
	s := ar.Summary
	report.Summary.SessionID = s.SessionID
	report.Summary.Version = s.Version
	report.Summary.Model = s.Model
	report.Summary.StartTime = s.StartTime.Format(time.RFC3339)
	report.Summary.EndTime = s.EndTime.Format(time.RFC3339)
	report.Summary.Duration = s.Duration.Round(time.Second).String()
	report.Summary.Messages = s.TotalMessages
	report.Summary.ToolCalls = s.TotalToolCalls
	report.Summary.OrphanUses = s.OrphanUses
	report.Summary.OrphanResults = s.OrphanResults
	report.Summary.Errors = s.Errors
	report.Summary.Interrupted = s.Interrupted
	report.Summary.Tokens.Input = s.TotalInputTokens
	report.Summary.Tokens.Output = s.TotalOutputTokens
	report.Summary.Tokens.CacheCreate = s.TotalCacheCreate
	report.Summary.Tokens.CacheRead = s.TotalCacheRead

	for _, o := range ar.Orphans {
		report.Orphans = append(report.Orphans, struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			ToolName string `json:"tool_name,omitempty"`
			Time     string `json:"timestamp"`
			UUID     string `json:"uuid"`
		}{o.Type, o.ID, o.ToolName, o.Timestamp.Format(time.RFC3339), o.UUID})
	}

	for _, c := range ar.StuckCalls {
		report.StuckCalls = append(report.StuckCalls, struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Duration string `json:"duration"`
			Time     string `json:"timestamp"`
		}{c.ID, c.Name, c.Duration.Round(time.Millisecond).String(), c.UseTime.Format(time.RFC3339)})
	}

	for _, c := range ar.ErrorCalls {
		report.ErrorCalls = append(report.ErrorCalls, struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Time string `json:"timestamp"`
		}{c.ID, c.Name, c.UseTime.Format(time.RFC3339)})
	}

	report.ToolStats = make(map[string]struct {
		Count   int    `json:"count"`
		Errors  int    `json:"errors"`
		Orphans int    `json:"orphans"`
		AvgDur  string `json:"avg_duration"`
		MaxDur  string `json:"max_duration"`
	})
	for name, stat := range s.ToolStats {
		report.ToolStats[name] = struct {
			Count   int    `json:"count"`
			Errors  int    `json:"errors"`
			Orphans int    `json:"orphans"`
			AvgDur  string `json:"avg_duration"`
			MaxDur  string `json:"max_duration"`
		}{
			Count:   stat.Count,
			Errors:  stat.Errors,
			Orphans: stat.Orphans,
			AvgDur:  analyzer.AvgDuration(stat.TotalDur, len(stat.Durations)).Round(time.Millisecond).String(),
			MaxDur:  stat.MaxDur.Round(time.Millisecond).String(),
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(report)
}

func printColored(ar *analyzer.Result, opts ReportOptions) {
	s := ar.Summary

	if !opts.OrphansOnly {
		// Header
		fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", bold, cyan, reset)
		fmt.Printf("%s%s║      CCDIAG - Session Analysis Report    ║%s\n", bold, cyan, reset)
		fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", bold, cyan, reset)
		fmt.Println()

		// Session info
		printSection("SESSION INFO")
		printKV("File", s.FilePath)
		printKV("Session ID", s.SessionID)
		printKV("Version", s.Version)
		printKV("Model", s.Model)
		printKV("Start", s.StartTime.Local().Format("2006-01-02 15:04:05"))
		printKV("End", s.EndTime.Local().Format("2006-01-02 15:04:05"))
		printKV("Duration", formatDuration(s.Duration))
		fmt.Println()

		// Message counts
		printSection("MESSAGES")
		printKV("Total", fmt.Sprintf("%d", s.TotalMessages))
		printKV("User", fmt.Sprintf("%d", s.UserMessages))
		printKV("Assistant", fmt.Sprintf("%d", s.AssistMessages))
		printKV("Progress", fmt.Sprintf("%d", s.ProgressMsgs))
		fmt.Println()

		// Token usage
		printSection("TOKEN USAGE")
		printKV("Input", formatNumber(s.TotalInputTokens))
		printKV("Output", formatNumber(s.TotalOutputTokens))
		printKV("Cache Create", formatNumber(s.TotalCacheCreate))
		printKV("Cache Read", formatNumber(s.TotalCacheRead))
		total := s.TotalInputTokens + s.TotalOutputTokens + s.TotalCacheCreate + s.TotalCacheRead
		printKV("Total", formatNumber(total))
		fmt.Println()

		// Tool call summary
		printSection("TOOL CALLS")
		printKV("Total", fmt.Sprintf("%d", s.TotalToolCalls))

		orphanColor := green
		if s.OrphanUses > 0 {
			orphanColor = red
		}
		fmt.Printf("  %-18s %s%s%d%s\n", "Orphan Uses:", bold, orphanColor, s.OrphanUses, reset)

		if s.OrphanResults > 0 {
			fmt.Printf("  %-18s %s%s%d%s\n", "Orphan Results:", bold, yellow, s.OrphanResults, reset)
		} else {
			printKV("Orphan Results", "0")
		}

		errColor := green
		if s.Errors > 0 {
			errColor = yellow
		}
		fmt.Printf("  %-18s %s%s%d%s\n", "Errors:", bold, errColor, s.Errors, reset)
		printKV("Interrupted", fmt.Sprintf("%d", s.Interrupted))
		fmt.Println()

		// Per-tool stats table
		printToolStatsTable(s.ToolStats)
	}

	// Orphans detail
	if len(ar.Orphans) > 0 {
		printSection("ORPHANED TOOL CALLS")
		for _, o := range ar.Orphans {
			icon := "?"
			color := yellow
			label := ""
			if o.Type == "tool_use" {
				icon = "!"
				color = red
				label = fmt.Sprintf("tool_use  [%s] %s", o.ToolName, o.ID)
			} else {
				icon = "~"
				color = yellow
				label = fmt.Sprintf("tool_result  %s", o.ID)
			}
			fmt.Printf("  %s%s%s%s %s %s%s%s\n", bold, color, icon, reset, label,
				dim, o.Timestamp.Local().Format("15:04:05"), reset)
			if opts.Verbose {
				fmt.Printf("    UUID: %s\n", o.UUID)
			}
		}
		fmt.Println()
	} else if opts.OrphansOnly {
		fmt.Printf("  %s%sNo orphaned tool calls found.%s\n\n", bold, green, reset)
	}

	if opts.OrphansOnly {
		return
	}

	// Stuck calls
	if len(ar.StuckCalls) > 0 {
		printSection("STUCK/SLOW TOOL CALLS")
		for _, c := range ar.StuckCalls {
			durStr := c.Duration.Round(time.Millisecond).String()
			fmt.Printf("  %s%s%-12s%s %-40s %s%s%s\n",
				bold, yellow, durStr, reset,
				truncate(c.Name+" ["+c.ID+"]", 40),
				dim, c.UseTime.Local().Format("15:04:05"), reset)
		}
		fmt.Println()
	}

	// Error calls
	if len(ar.ErrorCalls) > 0 {
		printSection("ERROR TOOL CALLS")
		for _, c := range ar.ErrorCalls {
			fmt.Printf("  %s%sX%s %-12s %-30s %s%s%s\n",
				bold, red, reset,
				c.Name, c.ID,
				dim, c.UseTime.Local().Format("15:04:05"), reset)
		}
		fmt.Println()
	}

	// Top 10 slowest
	if len(ar.TopSlowest) > 0 && opts.Verbose {
		printSection("TOP 10 SLOWEST TOOL CALLS")
		for i, c := range ar.TopSlowest {
			durStr := c.Duration.Round(time.Millisecond).String()
			fmt.Printf("  %s%2d.%s %-12s %-12s %s\n",
				dim, i+1, reset,
				durStr, c.Name, c.ID)
		}
		fmt.Println()
	}
}

func printPlain(ar *analyzer.Result, opts ReportOptions) {
	s := ar.Summary

	if !opts.OrphansOnly {
		fmt.Println("=== CCDIAG — Session Analysis Report ===")
		fmt.Println()
		fmt.Println("--- SESSION INFO ---")
		fmt.Printf("  File:        %s\n", s.FilePath)
		fmt.Printf("  Session ID:  %s\n", s.SessionID)
		fmt.Printf("  Version:     %s\n", s.Version)
		fmt.Printf("  Model:       %s\n", s.Model)
		fmt.Printf("  Start:       %s\n", s.StartTime.Local().Format("2006-01-02 15:04:05"))
		fmt.Printf("  End:         %s\n", s.EndTime.Local().Format("2006-01-02 15:04:05"))
		fmt.Printf("  Duration:    %s\n", formatDuration(s.Duration))
		fmt.Println()
		fmt.Println("--- MESSAGES ---")
		fmt.Printf("  Total:       %d\n", s.TotalMessages)
		fmt.Printf("  User:        %d\n", s.UserMessages)
		fmt.Printf("  Assistant:   %d\n", s.AssistMessages)
		fmt.Printf("  Progress:    %d\n", s.ProgressMsgs)
		fmt.Println()
		fmt.Println("--- TOKEN USAGE ---")
		fmt.Printf("  Input:       %s\n", formatNumber(s.TotalInputTokens))
		fmt.Printf("  Output:      %s\n", formatNumber(s.TotalOutputTokens))
		fmt.Printf("  Cache Create:%s\n", formatNumber(s.TotalCacheCreate))
		fmt.Printf("  Cache Read:  %s\n", formatNumber(s.TotalCacheRead))
		fmt.Println()
		fmt.Println("--- TOOL CALLS ---")
		fmt.Printf("  Total:          %d\n", s.TotalToolCalls)
		fmt.Printf("  Orphan Uses:    %d\n", s.OrphanUses)
		fmt.Printf("  Orphan Results: %d\n", s.OrphanResults)
		fmt.Printf("  Errors:         %d\n", s.Errors)
		fmt.Printf("  Interrupted:    %d\n", s.Interrupted)
		fmt.Println()

		printToolStatsTablePlain(s.ToolStats)
	}

	if len(ar.Orphans) > 0 {
		fmt.Println("--- ORPHANED TOOL CALLS ---")
		for _, o := range ar.Orphans {
			fmt.Printf("  [%s] %s %s @ %s\n", o.Type, o.ToolName, o.ID,
				o.Timestamp.Local().Format("15:04:05"))
		}
		fmt.Println()
	}

	if opts.OrphansOnly {
		return
	}

	if len(ar.StuckCalls) > 0 {
		fmt.Println("--- STUCK/SLOW TOOL CALLS ---")
		for _, c := range ar.StuckCalls {
			fmt.Printf("  %s  %s  %s  @ %s\n",
				c.Duration.Round(time.Millisecond),
				c.Name, c.ID,
				c.UseTime.Local().Format("15:04:05"))
		}
		fmt.Println()
	}

	if len(ar.ErrorCalls) > 0 {
		fmt.Println("--- ERROR TOOL CALLS ---")
		for _, c := range ar.ErrorCalls {
			fmt.Printf("  %s  %s  @ %s\n", c.Name, c.ID,
				c.UseTime.Local().Format("15:04:05"))
		}
		fmt.Println()
	}
}

func printSection(title string) {
	fmt.Printf("%s%s--- %s ---%s\n", bold, white, title, reset)
}

func printKV(key, value string) {
	fmt.Printf("  %-18s %s\n", key+":", value)
}

func printToolStatsTable(stats map[string]*analyzer.ToolStat) {
	if len(stats) == 0 {
		return
	}

	printSection("TOOL STATISTICS")

	// Sort by count descending
	names := sortedToolNames(stats)

	// Header
	fmt.Printf("  %s%-14s %6s %6s %6s %10s %10s %10s%s\n",
		dim, "TOOL", "COUNT", "ERR", "ORPH", "AVG", "MEDIAN", "MAX", reset)
	fmt.Printf("  %s%s%s\n", dim, strings.Repeat("-", 70), reset)

	for _, name := range names {
		s := stats[name]
		avg := analyzer.AvgDuration(s.TotalDur, len(s.Durations))
		med := analyzer.MedianDuration(s.Durations)

		errStr := fmt.Sprintf("%d", s.Errors)
		orphStr := fmt.Sprintf("%d", s.Orphans)

		if s.Errors > 0 {
			errStr = fmt.Sprintf("%s%s%d%s", bold, yellow, s.Errors, reset)
		}
		if s.Orphans > 0 {
			orphStr = fmt.Sprintf("%s%s%d%s", bold, red, s.Orphans, reset)
		}

		fmt.Printf("  %-14s %6d %6s %6s %10s %10s %10s\n",
			name, s.Count,
			errStr, orphStr,
			fmtDurShort(avg),
			fmtDurShort(med),
			fmtDurShort(s.MaxDur))
	}
	fmt.Println()
}

func printToolStatsTablePlain(stats map[string]*analyzer.ToolStat) {
	if len(stats) == 0 {
		return
	}

	fmt.Println("--- TOOL STATISTICS ---")
	names := sortedToolNames(stats)

	fmt.Printf("  %-14s %6s %6s %6s %10s %10s %10s\n",
		"TOOL", "COUNT", "ERR", "ORPH", "AVG", "MEDIAN", "MAX")
	fmt.Printf("  %s\n", strings.Repeat("-", 70))

	for _, name := range names {
		s := stats[name]
		avg := analyzer.AvgDuration(s.TotalDur, len(s.Durations))
		med := analyzer.MedianDuration(s.Durations)

		fmt.Printf("  %-14s %6d %6d %6d %10s %10s %10s\n",
			name, s.Count, s.Errors, s.Orphans,
			fmtDurShort(avg), fmtDurShort(med), fmtDurShort(s.MaxDur))
	}
	fmt.Println()
}

func sortedToolNames(stats map[string]*analyzer.ToolStat) []string {
	names := make([]string, 0, len(stats))
	for n := range stats {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return stats[names[i]].Count > stats[names[j]].Count
	})
	return names
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func fmtDurShort(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fm", d.Minutes())
}

func formatNumber(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	// Add thousand separators
	parts := make([]string, 0)
	for i := len(s); i > 0; i -= 3 {
		start := i - 3
		if start < 0 {
			start = 0
		}
		parts = append([]string{s[start:i]}, parts...)
	}
	return strings.Join(parts, ",")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// PrintScanSummary prints a brief summary for --scan-all mode.
func PrintScanSummary(results []*analyzer.Result, noColor bool) {
	if noColor {
		printScanPlain(results)
		return
	}

	fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", bold, cyan, reset)
	fmt.Printf("%s%s║      CCDIAG - Multi-Session Scan         ║%s\n", bold, cyan, reset)
	fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", bold, cyan, reset)
	fmt.Println()

	fmt.Printf("  Sessions scanned: %s%d%s\n\n", bold, len(results), reset)

	// Header
	fmt.Printf("  %s%-8s %-20s %5s %5s %4s %4s %-20s%s\n",
		dim, "VERSION", "DATE", "TOOLS", "ORPH", "ERR", "INT", "MODEL", reset)
	fmt.Printf("  %s%s%s\n", dim, strings.Repeat("-", 75), reset)

	totalOrphans := 0
	totalErrors := 0
	totalTools := 0

	for _, ar := range results {
		s := ar.Summary
		totalOrphans += s.OrphanUses
		totalErrors += s.Errors
		totalTools += s.TotalToolCalls

		orphColor := ""
		if s.OrphanUses > 0 {
			orphColor = red
		}

		model := s.Model
		if len(model) > 20 {
			model = model[:20]
		}

		fmt.Printf("  %-8s %-20s %5d %s%5d%s %4d %4d %-20s\n",
			s.Version,
			s.StartTime.Local().Format("2006-01-02 15:04"),
			s.TotalToolCalls,
			orphColor, s.OrphanUses, reset,
			s.Errors, s.Interrupted,
			model)
	}

	fmt.Printf("  %s%s%s\n", dim, strings.Repeat("-", 75), reset)
	fmt.Printf("  %s%-8s %-20s %5d %5d %4d%s\n",
		bold, "TOTAL", "", totalTools, totalOrphans, totalErrors, reset)
	fmt.Println()

	if totalOrphans > 0 {
		fmt.Printf("  %s%sOrphaned tool calls found in sessions above. Use ccdiag <file> for details.%s\n\n",
			bold, yellow, reset)
	}
}

func printScanPlain(results []*analyzer.Result) {
	fmt.Println("=== CCDIAG — Multi-Session Scan ===")
	fmt.Printf("  Sessions scanned: %d\n\n", len(results))

	fmt.Printf("  %-8s %-20s %5s %5s %4s %4s %-20s\n",
		"VERSION", "DATE", "TOOLS", "ORPH", "ERR", "INT", "MODEL")
	fmt.Printf("  %s\n", strings.Repeat("-", 75))

	for _, ar := range results {
		s := ar.Summary
		model := s.Model
		if len(model) > 20 {
			model = model[:20]
		}
		fmt.Printf("  %-8s %-20s %5d %5d %4d %4d %-20s\n",
			s.Version,
			s.StartTime.Local().Format("2006-01-02 15:04"),
			s.TotalToolCalls,
			s.OrphanUses,
			s.Errors, s.Interrupted,
			model)
	}
	fmt.Println()
}
