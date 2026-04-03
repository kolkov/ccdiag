package recover

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PrintRecovery outputs recovery data in the requested format.
func PrintRecovery(rs *Session, opts Options) error {
	var w io.Writer = os.Stdout
	if opts.OutFile != "" {
		f, err := os.Create(opts.OutFile)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		defer f.Close()
		bw := bufio.NewWriter(f)
		defer bw.Flush()
		w = bw
	}

	switch opts.Output {
	case "messages":
		printMessages(w, rs, opts)
	case "actions":
		printActions(w, rs)
	case "full":
		printHandoff(w, rs, opts)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "---")
		fmt.Fprintln(w)
		printMessages(w, rs, opts)
	default:
		printHandoff(w, rs, opts)
	}

	return nil
}

func printHandoff(w io.Writer, rs *Session, opts Options) {
	fmt.Fprintf(w, "# Session Recovery — %s\n\n", rs.SessionID)
	fmt.Fprintf(w, "> **File**: `%s`\n", rs.FilePath)
	fmt.Fprintf(w, "> **Period**: %s → %s (%s)\n",
		rs.StartTime.Format("2006-01-02 15:04"),
		rs.EndTime.Format("2006-01-02 15:04"),
		formatDur(rs.Duration))
	fmt.Fprintf(w, "> **Model**: %s | **Version**: %s\n\n", rs.Model, rs.Version)

	fmt.Fprintln(w, "## Stats")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "| Metric | Count |\n|--------|-------|\n")
	fmt.Fprintf(w, "| JSONL lines | %d |\n", rs.Stats.TotalLines)
	fmt.Fprintf(w, "| User messages | %d |\n", rs.Stats.UserCount)
	fmt.Fprintf(w, "| Assistant messages | %d |\n", rs.Stats.AssistantCount)
	fmt.Fprintf(w, "| Tool uses | %d |\n", rs.Stats.ToolUseCount)
	fmt.Fprintf(w, "| Files written | %d |\n", rs.Stats.FilesWritten)
	fmt.Fprintf(w, "| Files edited | %d |\n", rs.Stats.FilesEdited)
	fmt.Fprintf(w, "| Bash commands | %d |\n", rs.Stats.BashCount)
	fmt.Fprintf(w, "| GitHub actions | %d |\n", rs.Stats.GitHubCount)
	fmt.Fprintf(w, "| Web searches | %d |\n", rs.Stats.WebSearchCount)
	fmt.Fprintln(w)

	if len(rs.FilesModified) > 0 {
		fmt.Fprintln(w, "## Files Modified")
		fmt.Fprintln(w)
		printFilesSummary(w, rs.FilesModified)
		fmt.Fprintln(w)
	}

	if len(rs.Issues) > 0 {
		fmt.Fprintln(w, "## GitHub Issues")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "| Issue | Commented | Created | Lines |\n")
		fmt.Fprintf(w, "|-------|-----------|---------|-------|\n")
		var nums []string
		for n := range rs.Issues {
			nums = append(nums, n)
		}
		sort.Strings(nums)
		for _, n := range nums {
			ref := rs.Issues[n]
			c, cr := "—", "—"
			if ref.Commented {
				c = "yes"
			}
			if ref.Created {
				cr = "yes"
			}
			fmt.Fprintf(w, "| #%s | %s | %s | %v |\n", n, c, cr, ref.Lines)
		}
		fmt.Fprintln(w)
	}

	if len(rs.GitHubActions) > 0 {
		fmt.Fprintln(w, "## GitHub Commands")
		fmt.Fprintln(w)
		for _, ga := range rs.GitHubActions {
			cmd := ga.Command
			if len(cmd) > 200 {
				cmd = cmd[:200] + "..."
			}
			fmt.Fprintf(w, "- L%d [%s]: `%s`\n", ga.Line, ga.Type, cmd)
		}
		fmt.Fprintln(w)
	}

	if len(rs.GitCommands) > 0 {
		fmt.Fprintln(w, "## Git Commands")
		fmt.Fprintln(w)
		for _, gc := range rs.GitCommands {
			cmd := gc.Command
			if len(cmd) > 200 {
				cmd = cmd[:200] + "..."
			}
			fmt.Fprintf(w, "- L%d: `%s`\n", gc.Line, cmd)
		}
		fmt.Fprintln(w)
	}

	if len(rs.WebSearches) > 0 {
		fmt.Fprintln(w, "## Web Searches")
		fmt.Fprintln(w)
		for _, ws := range rs.WebSearches {
			q := ws.Query
			if len(q) > 150 {
				q = q[:150] + "..."
			}
			fmt.Fprintf(w, "- L%d [%s]: %s\n", ws.Line, ws.Tool, q)
		}
		fmt.Fprintln(w)
	}

	if len(rs.URLsReferenced) > 0 {
		fmt.Fprintln(w, "## URLs Referenced")
		fmt.Fprintln(w)
		for _, u := range rs.URLsReferenced {
			fmt.Fprintf(w, "- %s\n", u)
		}
		fmt.Fprintln(w)
	}

	lastN := opts.LastN
	if lastN <= 0 {
		lastN = 20
	}
	msgs := rs.UserMessages
	if len(msgs) > lastN {
		msgs = msgs[len(msgs)-lastN:]
	}
	fmt.Fprintf(w, "## Last %d User Messages\n\n", len(msgs))
	for _, m := range msgs {
		text := m.Text
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		text = strings.ReplaceAll(text, "\n", " ")
		fmt.Fprintf(w, "- **L%d**: %s\n", m.Line, text)
	}
	fmt.Fprintln(w)
}

func printMessages(w io.Writer, rs *Session, opts Options) {
	fmt.Fprintln(w, "## All User Messages")
	fmt.Fprintln(w)
	for i, m := range rs.UserMessages {
		if opts.MaxLines > 0 && i >= opts.MaxLines {
			fmt.Fprintf(w, "\n... truncated (%d more)\n", len(rs.UserMessages)-i)
			break
		}
		text := m.Text
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		text = strings.ReplaceAll(text, "\n", " | ")
		fmt.Fprintf(w, "[%d] L%d: %s\n\n", i+1, m.Line, text)
	}
}

func printActions(w io.Writer, rs *Session) {
	fmt.Fprintln(w, "## All Actions (chronological)")
	fmt.Fprintln(w)

	type action struct {
		line int
		text string
	}
	var actions []action

	for _, fa := range rs.FilesModified {
		actions = append(actions, action{fa.Line, fmt.Sprintf("[%s] %s → %s", fa.Tool, fa.Action, fa.Path)})
	}
	for _, ga := range rs.GitHubActions {
		cmd := ga.Command
		if len(cmd) > 150 {
			cmd = cmd[:150] + "..."
		}
		actions = append(actions, action{ga.Line, fmt.Sprintf("[GitHub/%s] %s", ga.Type, cmd)})
	}
	for _, gc := range rs.GitCommands {
		cmd := gc.Command
		if len(cmd) > 150 {
			cmd = cmd[:150] + "..."
		}
		actions = append(actions, action{gc.Line, fmt.Sprintf("[Git] %s", cmd)})
	}
	for _, ws := range rs.WebSearches {
		actions = append(actions, action{ws.Line, fmt.Sprintf("[%s] %s", ws.Tool, ws.Query)})
	}

	sort.Slice(actions, func(i, j int) bool { return actions[i].line < actions[j].line })

	for _, a := range actions {
		fmt.Fprintf(w, "- L%d: %s\n", a.line, a.text)
	}
}

func printFilesSummary(w io.Writer, files []FileAction) {
	type fileInfo struct {
		path      string
		writes    int
		edits     int
		reads     int
		firstLine int
	}
	index := make(map[string]*fileInfo)
	var order []string

	for _, fa := range files {
		fi, ok := index[fa.Path]
		if !ok {
			fi = &fileInfo{path: fa.Path, firstLine: fa.Line}
			index[fa.Path] = fi
			order = append(order, fa.Path)
		}
		switch fa.Tool {
		case "Write":
			fi.writes++
		case "Edit":
			fi.edits++
		case "Read":
			fi.reads++
		}
	}

	fmt.Fprintf(w, "| File | Writes | Edits | First Line |\n")
	fmt.Fprintf(w, "|------|--------|-------|------------|\n")
	for _, p := range order {
		fi := index[p]
		if fi.writes == 0 && fi.edits == 0 {
			continue
		}
		short := shortenPath(p)
		fmt.Fprintf(w, "| `%s` | %d | %d | L%d |\n", short, fi.writes, fi.edits, fi.firstLine)
	}
}

func shortenPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	for _, prefix := range []string{"D:\\projects\\", "G:\\projects\\", "C:\\Users\\Andy\\"} {
		if strings.HasPrefix(p, prefix) {
			return p[len(prefix):]
		}
	}
	return p
}

func formatDur(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
