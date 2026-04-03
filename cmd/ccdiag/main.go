package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kolkov/ccdiag/internal/analyzer"
	"github.com/kolkov/ccdiag/internal/parser"
	"github.com/kolkov/ccdiag/internal/recover"
)

var version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "recover" {
		runRecover(os.Args[2:])
		return
	}

	scanAll := flag.Bool("scan-all", false, "Scan all sessions in ~/.claude/projects/")
	scanDir := flag.String("scan-dir", "", "Scan all .jsonl files in specified directory")
	stuckStr := flag.String("stuck-threshold", "60s", "Threshold for stuck tool detection")
	orphansOnly := flag.Bool("orphans-only", false, "Show only orphaned tool calls")
	jsonOut := flag.Bool("json", false, "Output in JSON format")
	verbose := flag.Bool("v", false, "Verbose output (show all tool call details)")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ccdiag v%s\n", version)
		return
	}

	stuckThreshold, err := time.ParseDuration(*stuckStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --stuck-threshold: %v\n", err)
		os.Exit(1)
	}

	opts := ReportOptions{
		JSONOutput:  *jsonOut,
		Verbose:     *verbose,
		OrphansOnly: *orphansOnly,
		NoColor:     *noColor,
	}
	if os.Getenv("NO_COLOR") != "" {
		opts.NoColor = true
	}

	if *scanAll || *scanDir != "" {
		dir := *scanDir
		if *scanAll {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot find home dir: %v\n", err)
				os.Exit(1)
			}
			dir = filepath.Join(home, ".claude", "projects")
		}

		results, err := scanSessions(dir, stuckThreshold)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
			os.Exit(1)
		}

		if len(results) == 0 {
			fmt.Println("No session files found.")
			return
		}

		if *jsonOut {
			for _, ar := range results {
				PrintReport(ar, opts)
			}
		} else {
			PrintScanSummary(results, opts.NoColor)
		}
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: ccdiag [command] [flags] <session.jsonl>\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  recover    Extract context from a session for handoff\n")
		fmt.Fprintf(os.Stderr, "  (default)  Analyze session for tool call issues\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  ccdiag session.jsonl\n")
		fmt.Fprintf(os.Stderr, "  ccdiag --scan-all\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover session.jsonl\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover --latest D:\\projects\\myproject\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover --output full -o handoff.md session.jsonl\n")
		os.Exit(1)
	}

	filePath := args[0]
	pr, err := parser.ParseFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
		os.Exit(1)
	}

	ar := analyzer.Analyze(pr, stuckThreshold)
	PrintReport(ar, opts)

	if ar.Summary.OrphanUses > 0 {
		os.Exit(2)
	}
}

func runRecover(args []string) {
	fs := flag.NewFlagSet("recover", flag.ExitOnError)
	output := fs.String("output", "handoff", "Output format: handoff, messages, actions, full")
	outFile := fs.String("o", "", "Write output to file instead of stdout")
	lastN := fs.Int("last", 0, "Show only last N user messages in handoff (0=20)")
	maxLines := fs.Int("max", 0, "Limit messages output (0=all)")
	latest := fs.Bool("latest", false, "Find latest session for the given project path")
	noToolArgs := fs.Bool("no-tool-args", false, "Omit tool input details")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: ccdiag recover [flags] <session.jsonl | project-path>\n\n")
		fmt.Fprintf(os.Stderr, "Formats:\n")
		fmt.Fprintf(os.Stderr, "  handoff   Session handoff summary (default)\n")
		fmt.Fprintf(os.Stderr, "  messages  All user messages\n")
		fmt.Fprintf(os.Stderr, "  actions   All tool actions chronologically\n")
		fmt.Fprintf(os.Stderr, "  full      Handoff + all messages\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover session.jsonl\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover --latest D:\\projects\\myproject\n")
		fmt.Fprintf(os.Stderr, "  ccdiag recover --output full -o recovery.md session.jsonl\n")
		os.Exit(1)
	}

	filePath := remaining[0]

	if *latest {
		var err error
		filePath, err = recover.FindLatestSession(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Found: %s\n", filePath)
	}

	rs, err := recover.RecoverSession(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recover error: %v\n", err)
		os.Exit(1)
	}

	opts := recover.Options{
		Output:     *output,
		OutFile:    *outFile,
		MaxLines:   *maxLines,
		LastN:      *lastN,
		NoToolArgs: *noToolArgs,
	}

	if err := recover.PrintRecovery(rs, opts); err != nil {
		fmt.Fprintf(os.Stderr, "output error: %v\n", err)
		os.Exit(1)
	}
}

func scanSessions(dir string, stuckThreshold time.Duration) ([]*analyzer.Result, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var results []*analyzer.Result
	for _, f := range files {
		pr, err := parser.ParseFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", f, err)
			continue
		}
		ar := analyzer.Analyze(pr, stuckThreshold)
		if ar.Summary.TotalToolCalls > 0 {
			results = append(results, ar)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Summary.StartTime.After(results[j].Summary.StartTime)
	})

	return results, nil
}
