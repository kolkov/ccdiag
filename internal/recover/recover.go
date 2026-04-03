package recover

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kolkov/ccdiag/internal/parser"
)

// Options controls session recovery output.
type Options struct {
	Output     string // "handoff", "messages", "actions", "full"
	OutFile    string // write to file instead of stdout
	MaxLines   int    // limit user/assistant messages (0 = all)
	LastN      int    // only last N user messages (0 = all)
	NoToolArgs bool   // omit tool input details
}

// Session holds all extracted context from a session.
type Session struct {
	SessionID string
	FilePath  string
	Version   string
	Model     string
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration

	UserMessages      []Message
	AssistantMessages []Message
	FilesModified     []FileAction
	GitCommands       []GitAction
	GitHubActions     []GitHubAction
	BashCommands      []BashAction
	WebSearches       []WebAction
	URLsReferenced    []string
	Issues            map[string]*IssueRef

	Stats Stats
}

// Stats holds recovery statistics.
type Stats struct {
	TotalLines     int
	UserCount      int
	AssistantCount int
	ToolUseCount   int
	FilesWritten   int
	FilesEdited    int
	FilesRead      int
	BashCount      int
	GitHubCount    int
	WebSearchCount int
}

// Message is a conversation message with metadata.
type Message struct {
	Line    int
	Role    string
	Text    string
	HasTool bool
}

// FileAction tracks a file operation.
type FileAction struct {
	Line   int
	Tool   string
	Path   string
	Action string
}

// GitAction tracks git commands.
type GitAction struct {
	Line    int
	Command string
}

// GitHubAction tracks gh CLI commands.
type GitHubAction struct {
	Line    int
	Command string
	Type    string
}

// BashAction tracks notable bash commands.
type BashAction struct {
	Line    int
	Command string
}

// WebAction tracks web searches/fetches.
type WebAction struct {
	Line  int
	Tool  string
	Query string
}

// IssueRef tracks references to GitHub issues.
type IssueRef struct {
	Number    string
	Commented bool
	Created   bool
	Lines     []int
}

// RecoverSession extracts full context from a session JSONL file.
func RecoverSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	return recoverFromReader(f, path)
}

func recoverFromReader(r io.Reader, path string) (*Session, error) {
	rs := &Session{
		FilePath: path,
		Issues:   make(map[string]*IssueRef),
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	urlSet := make(map[string]bool)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		rs.Stats.TotalLines = lineNum

		var msg parser.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if !msg.Timestamp.IsZero() {
			if rs.StartTime.IsZero() || msg.Timestamp.Before(rs.StartTime) {
				rs.StartTime = msg.Timestamp
			}
			if msg.Timestamp.After(rs.EndTime) {
				rs.EndTime = msg.Timestamp
			}
		}

		switch msg.Type {
		case "file-history-snapshot", "queue-operation", "progress", "system",
			"last-prompt", "permission-mode":
			continue
		}

		if msg.Message == nil {
			continue
		}

		if rs.SessionID == "" && msg.SessionID != "" {
			rs.SessionID = msg.SessionID
		}
		if rs.Version == "" && msg.Version != "" {
			rs.Version = msg.Version
		}
		if rs.Model == "" && msg.Message.Model != "" {
			rs.Model = msg.Message.Model
		}

		blocks := parser.ParseContentBlocks(msg.Message.Content)
		role := msg.Message.Role

		if role == "user" {
			rs.Stats.UserCount++
			text := extractText(blocks)
			if strings.Contains(text, "<command-name>") ||
				strings.Contains(text, "<local-command") {
				continue
			}
			if text != "" {
				rs.UserMessages = append(rs.UserMessages, Message{
					Line: lineNum, Role: "user", Text: text,
				})
			}
			extractURLs(text, urlSet)
			extractIssueRefs(text, lineNum, rs.Issues)
		}

		if role == "assistant" {
			rs.Stats.AssistantCount++
			text := extractText(blocks)
			hasTool := false

			for _, block := range blocks {
				if block.Type == "tool_use" {
					hasTool = true
					rs.Stats.ToolUseCount++
					processToolUse(block, lineNum, rs)
				}
			}

			if text != "" {
				rs.AssistantMessages = append(rs.AssistantMessages, Message{
					Line: lineNum, Role: "assistant", Text: text, HasTool: hasTool,
				})
			}
			extractURLs(text, urlSet)
			extractIssueRefs(text, lineNum, rs.Issues)
		}
	}

	if err := scanner.Err(); err != nil {
		return rs, fmt.Errorf("scan error: %w", err)
	}

	for u := range urlSet {
		rs.URLsReferenced = append(rs.URLsReferenced, u)
	}
	sort.Strings(rs.URLsReferenced)
	rs.Duration = rs.EndTime.Sub(rs.StartTime)

	return rs, nil
}

func processToolUse(block parser.ContentBlock, lineNum int, rs *Session) {
	switch block.Name {
	case "Write":
		path := parser.ExtractInputString(block.Input, "file_path")
		if path != "" {
			rs.FilesModified = append(rs.FilesModified, FileAction{
				Line: lineNum, Tool: "Write", Path: path, Action: "created",
			})
			rs.Stats.FilesWritten++
		}
	case "Edit":
		path := parser.ExtractInputString(block.Input, "file_path")
		if path != "" {
			rs.FilesModified = append(rs.FilesModified, FileAction{
				Line: lineNum, Tool: "Edit", Path: path, Action: "modified",
			})
			rs.Stats.FilesEdited++
		}
	case "Read":
		path := parser.ExtractInputString(block.Input, "file_path")
		if path != "" {
			rs.FilesModified = append(rs.FilesModified, FileAction{
				Line: lineNum, Tool: "Read", Path: path, Action: "read",
			})
			rs.Stats.FilesRead++
		}
	case "Bash":
		cmd := parser.ExtractInputString(block.Input, "command")
		if cmd == "" {
			return
		}
		rs.Stats.BashCount++
		switch {
		case strings.Contains(cmd, "gh issue") || strings.Contains(cmd, "gh api") ||
			strings.Contains(cmd, "gh pr"):
			action := classifyGHCommand(cmd)
			rs.GitHubActions = append(rs.GitHubActions, GitHubAction{
				Line: lineNum, Command: cmd, Type: action,
			})
			rs.Stats.GitHubCount++
			if strings.Contains(cmd, "gh issue comment") {
				trackGHIssue(cmd, lineNum, rs.Issues, true, false)
			}
			if strings.Contains(cmd, "gh issue create") {
				trackGHIssue(cmd, lineNum, rs.Issues, false, true)
			}
		case strings.HasPrefix(cmd, "git "):
			rs.GitCommands = append(rs.GitCommands, GitAction{
				Line: lineNum, Command: cmd,
			})
		default:
			if isNotableBash(cmd) {
				rs.BashCommands = append(rs.BashCommands, BashAction{
					Line: lineNum, Command: cmd,
				})
			}
		}
	case "WebSearch":
		query := parser.ExtractInputString(block.Input, "query")
		if query != "" {
			rs.WebSearches = append(rs.WebSearches, WebAction{
				Line: lineNum, Tool: "WebSearch", Query: query,
			})
			rs.Stats.WebSearchCount++
		}
	case "WebFetch":
		url := parser.ExtractInputString(block.Input, "url")
		if url != "" {
			rs.WebSearches = append(rs.WebSearches, WebAction{
				Line: lineNum, Tool: "WebFetch", Query: url,
			})
			rs.Stats.WebSearchCount++
		}
	}
}

// FindLatestSession finds the most recently modified .jsonl in a project dir.
func FindLatestSession(projectPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dirName := projectPath
	dirName = strings.ReplaceAll(dirName, ":\\", "--")
	dirName = strings.ReplaceAll(dirName, ":", "--")
	dirName = strings.ReplaceAll(dirName, "\\", "-")
	dirName = strings.ReplaceAll(dirName, "/", "-")

	sessDir := filepath.Join(home, ".claude", "projects", dirName)

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return "", fmt.Errorf("read session dir %s: %w", sessDir, err)
	}

	var latest string
	var latestTime time.Time

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = filepath.Join(sessDir, e.Name())
		}
	}

	if latest == "" {
		return "", fmt.Errorf("no session files found in %s", sessDir)
	}
	return latest, nil
}

// --- Helpers ---

func extractText(blocks []parser.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractURLs(text string, urlSet map[string]bool) {
	for _, prefix := range []string{"https://", "http://"} {
		idx := 0
		for {
			pos := strings.Index(text[idx:], prefix)
			if pos < 0 {
				break
			}
			start := idx + pos
			end := start
			for end < len(text) {
				c := text[end]
				if c == ' ' || c == '\n' || c == '\r' || c == '\t' ||
					c == '"' || c == '\'' || c == ')' || c == ']' ||
					c == '>' || c == '`' || c == '|' {
					break
				}
				end++
			}
			url := text[start:end]
			url = strings.TrimRight(url, ".,;:!?")
			if len(url) > 15 {
				urlSet[url] = true
			}
			idx = end
		}
	}
}

func extractIssueRefs(text string, lineNum int, issues map[string]*IssueRef) {
	for i := 0; i < len(text)-1; i++ {
		if text[i] == '#' && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
			j := i + 1
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			num := text[i+1 : j]
			if len(num) >= 3 && len(num) <= 6 {
				ref, ok := issues[num]
				if !ok {
					ref = &IssueRef{Number: num}
					issues[num] = ref
				}
				if len(ref.Lines) == 0 || ref.Lines[len(ref.Lines)-1] != lineNum {
					ref.Lines = append(ref.Lines, lineNum)
				}
			}
		}
	}
}

func trackGHIssue(cmd string, lineNum int, issues map[string]*IssueRef, commented, created bool) {
	parts := strings.Fields(cmd)
	for i, p := range parts {
		if (p == "comment" || p == "view") && i+1 < len(parts) {
			num := parts[i+1]
			num = strings.TrimFunc(num, func(r rune) bool { return r < '0' || r > '9' })
			if num != "" {
				ref, ok := issues[num]
				if !ok {
					ref = &IssueRef{Number: num}
					issues[num] = ref
				}
				if commented {
					ref.Commented = true
				}
				if created {
					ref.Created = true
				}
			}
		}
	}
}

func classifyGHCommand(cmd string) string {
	switch {
	case strings.Contains(cmd, "gh issue create"):
		return "issue-create"
	case strings.Contains(cmd, "gh issue comment"):
		return "issue-comment"
	case strings.Contains(cmd, "gh pr create"):
		return "pr-create"
	case strings.Contains(cmd, "gh pr comment"):
		return "pr-comment"
	case strings.Contains(cmd, "gh api"):
		return "api"
	default:
		return "other"
	}
}

func isNotableBash(cmd string) bool {
	trivial := []string{"ls", "cat ", "head ", "tail ", "echo ", "pwd", "cd ",
		"wc ", "stat ", "file ", "which ", "mkdir ", "test "}
	lower := strings.ToLower(cmd)
	for _, t := range trivial {
		if strings.HasPrefix(lower, t) {
			return false
		}
	}
	notable := []string{"go ", "npm ", "node ", "cargo ", "make", "python",
		"docker ", "kubectl ", "curl ", "wget ", "tar ", "grep ", "find ",
		"sed ", "awk ", "patch ", "diff "}
	for _, n := range notable {
		if strings.HasPrefix(lower, n) {
			return true
		}
	}
	return len(cmd) > 20
}
