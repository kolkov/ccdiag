package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kolkov/ccdiag/internal/proxy"
)

func runProxyStats(args []string) {
	fs := flag.NewFlagSet("proxy stats", flag.ExitOnError)
	last := fs.String("last", "", "Time filter: 1h, 24h, 7d, etc.")
	showCost := fs.Bool("cost", false, "Show detailed cost breakdown")
	jsonOut := fs.Bool("json", false, "Output in JSON format")
	noColor := fs.Bool("no-color", false, "Disable colored output")
	logDir := fs.String("log-dir", "", "Directory for traffic.jsonl (default ~/.ccdiag/proxy)")
	fs.Parse(args)

	if os.Getenv("NO_COLOR") != "" {
		*noColor = true
	}

	since, err := proxy.ParseDurationFlag(*last)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	entries, path, err := proxy.LoadEntries(*logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "no traffic data found in %s\n", path)
		os.Exit(0)
	}

	opts := proxy.StatsOptions{
		Since:    since,
		ShowCost: *showCost,
	}
	result := proxy.ComputeStats(entries, opts)

	if result.TotalRequests == 0 {
		fmt.Fprintf(os.Stderr, "no entries match the time filter\n")
		os.Exit(0)
	}

	if *jsonOut {
		printStatsJSON(result)
	} else if *noColor {
		printStatsPlain(result, *showCost)
	} else {
		printStatsColored(result, *showCost)
	}
}

func printStatsJSON(r proxy.StatsResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(r)
}

func printStatsColored(r proxy.StatsResult, showCost bool) {
	// Header
	fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", bold, cyan, reset)
	fmt.Printf("%s%s║      CCDIAG PROXY - Traffic Stats        ║%s\n", bold, cyan, reset)
	fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", bold, cyan, reset)
	fmt.Println()

	// Summary
	printSection("SUMMARY")
	period := formatPeriod(r.PeriodStart, r.PeriodEnd)
	printKV("Period", period)
	if r.FilterDesc != "" {
		printKV("Filter", r.FilterDesc)
	}
	printKV("Total requests", fmt.Sprintf("%d", r.TotalRequests))

	successPct := float64(r.SuccessCount) / float64(r.TotalRequests) * 100
	successStr := fmt.Sprintf("%d (%.1f%%)", r.SuccessCount, successPct)
	fmt.Printf("  %-18s %s%s%s\n", "Success:", green+bold, successStr, reset)

	if r.ErrorCount > 0 {
		errParts := make([]string, 0)
		for typ, cnt := range r.ErrorTypes {
			errParts = append(errParts, fmt.Sprintf("%d %s", cnt, typ))
		}
		errStr := fmt.Sprintf("%d (%s)", r.ErrorCount, strings.Join(errParts, ", "))
		fmt.Printf("  %-18s %s%s%s\n", "Errors:", red+bold, errStr, reset)
	} else {
		printKV("Errors", "0")
	}

	printKV("Avg latency", formatLatency(r.AvgLatencyMs))
	printKV("Min / Max", fmt.Sprintf("%s / %s", formatLatency(float64(r.MinLatencyMs)), formatLatency(float64(r.MaxLatencyMs))))
	printKV("Total bytes", fmt.Sprintf("%s in / %s out", formatBytes(r.TotalRequestBytes), formatBytes(r.TotalResponseBytes)))
	fmt.Println()

	// Tokens
	printSection("TOKENS")
	printKV("Input", formatNumber(r.InputTokens))
	printKV("Output", formatNumber(r.OutputTokens))
	printKV("Cache create", formatNumber(r.CacheCreate))
	printKV("Cache read", formatNumber(r.CacheRead))

	cacheColor := cacheRatioColor(r.CacheRatio)
	fmt.Printf("  %-18s %s%s%.1f%%%s\n", "Cache ratio:", bold, cacheColor, r.CacheRatio*100, reset)
	fmt.Println()

	// Cost
	printSection("COST (estimated)")
	printCostKV("Input", r.Cost.InputCost)
	printCostKV("Output", r.Cost.OutputCost)
	printCostKV("Cache create", r.Cost.CacheCreateCost)
	printCostKV("Cache read", r.Cost.CacheReadCost)
	fmt.Printf("  %-18s %s%s$%.4f%s\n", "Total:", bold, white, r.Cost.TotalCost, reset)
	fmt.Println()

	// By model
	if len(r.ByModel) > 0 {
		printSection("BY MODEL")
		for _, ms := range r.ByModel {
			line := fmt.Sprintf("%-22s %3d req", ms.Model, ms.Requests)
			if ms.Cost.TotalCost > 0 {
				line += fmt.Sprintf("   $%.4f", ms.Cost.TotalCost)
			}
			if ms.Errors > 0 && ms.Requests == ms.Errors {
				line += fmt.Sprintf("   %s(errors)%s", red, reset)
			} else if ms.CacheRatio > 0 {
				cc := cacheRatioColor(ms.CacheRatio)
				line += fmt.Sprintf("   cache %s%.1f%%%s", cc, ms.CacheRatio*100, reset)
			}
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}

	// Hourly histogram
	if len(r.Hourly) > 0 && len(r.Hourly) > 1 {
		printSection("HOURLY")
		maxReq := 0
		for _, b := range r.Hourly {
			if b.Requests > maxReq {
				maxReq = b.Requests
			}
		}
		barWidth := 30
		for _, b := range r.Hourly {
			hourStr := b.Hour.Format("15:04")
			bar := ""
			if maxReq > 0 {
				w := b.Requests * barWidth / maxReq
				if w == 0 && b.Requests > 0 {
					w = 1
				}
				bar = strings.Repeat("█", w)
			}
			fmt.Printf("  %s %s%-*s%s %d\n", hourStr, cyan, barWidth, bar, reset, b.Requests)
		}
		fmt.Println()
	}

	// Anomalies
	if r.LowCacheCount > 0 || r.ErrorCount > 0 {
		printSection("ANOMALIES")
		if r.LowCacheCount > 0 {
			fmt.Printf("  %s%s!%s %d requests with cache ratio < 50%%\n", bold, yellow, reset, r.LowCacheCount)
		}
		if r.ErrorCount > 0 {
			fmt.Printf("  %s%s!%s %d error responses\n", bold, red, reset, r.ErrorCount)
		}
		fmt.Println()
	}
}

func printStatsPlain(r proxy.StatsResult, showCost bool) {
	fmt.Println("=== CCDIAG PROXY — Traffic Stats ===")
	fmt.Println()

	fmt.Println("--- SUMMARY ---")
	fmt.Printf("  Period:          %s\n", formatPeriod(r.PeriodStart, r.PeriodEnd))
	if r.FilterDesc != "" {
		fmt.Printf("  Filter:          %s\n", r.FilterDesc)
	}
	fmt.Printf("  Total requests:  %d\n", r.TotalRequests)
	successPct := float64(r.SuccessCount) / float64(r.TotalRequests) * 100
	fmt.Printf("  Success:         %d (%.1f%%)\n", r.SuccessCount, successPct)
	fmt.Printf("  Errors:          %d\n", r.ErrorCount)
	fmt.Printf("  Avg latency:     %s\n", formatLatency(r.AvgLatencyMs))
	fmt.Printf("  Total bytes:     %s in / %s out\n", formatBytes(r.TotalRequestBytes), formatBytes(r.TotalResponseBytes))
	fmt.Println()

	fmt.Println("--- TOKENS ---")
	fmt.Printf("  Input:           %s\n", formatNumber(r.InputTokens))
	fmt.Printf("  Output:          %s\n", formatNumber(r.OutputTokens))
	fmt.Printf("  Cache create:    %s\n", formatNumber(r.CacheCreate))
	fmt.Printf("  Cache read:      %s\n", formatNumber(r.CacheRead))
	fmt.Printf("  Cache ratio:     %.1f%%\n", r.CacheRatio*100)
	fmt.Println()

	fmt.Println("--- COST (estimated) ---")
	fmt.Printf("  Input:           $%.4f\n", r.Cost.InputCost)
	fmt.Printf("  Output:          $%.4f\n", r.Cost.OutputCost)
	fmt.Printf("  Cache create:    $%.4f\n", r.Cost.CacheCreateCost)
	fmt.Printf("  Cache read:      $%.4f\n", r.Cost.CacheReadCost)
	fmt.Printf("  Total:           $%.4f\n", r.Cost.TotalCost)
	fmt.Println()

	if len(r.ByModel) > 0 {
		fmt.Println("--- BY MODEL ---")
		for _, ms := range r.ByModel {
			if ms.Errors > 0 && ms.Requests == ms.Errors {
				fmt.Printf("  %-22s %3d req   $%.4f   (errors)\n", ms.Model, ms.Requests, ms.Cost.TotalCost)
			} else {
				fmt.Printf("  %-22s %3d req   $%.4f   cache %.1f%%\n", ms.Model, ms.Requests, ms.Cost.TotalCost, ms.CacheRatio*100)
			}
		}
		fmt.Println()
	}
}

// --- Helpers ---

func formatPeriod(start, end time.Time) string {
	dur := end.Sub(start)
	return fmt.Sprintf("%s -> %s (%s)",
		start.Local().Format("2006-01-02 15:04"),
		end.Local().Format("15:04"),
		formatDuration(dur))
}

func formatLatency(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.1fs", ms/1000)
}

func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/1024/1024)
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func printCostKV(key string, cost float64) {
	fmt.Printf("  %-18s $%.4f\n", key+":", cost)
}

func cacheRatioColor(ratio float64) string {
	switch {
	case ratio >= 0.9:
		return green
	case ratio >= 0.5:
		return yellow
	default:
		return red
	}
}
