package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ParseFile reads and parses a JSONL session file.
func ParseFile(path string) (*ParseResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	return ParseReader(f, path)
}

// ParseReader parses JSONL from an io.Reader.
func ParseReader(r io.Reader, path string) (*ParseResult, error) {
	result := &ParseResult{
		FilePath:    path,
		ToolUses:    make(map[string]*ToolUseEntry),
		ToolResults: make(map[string]*ToolResultEntry),
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			result.Errors = append(result.Errors, ParseError{
				Line:    lineNum,
				Message: fmt.Sprintf("JSON parse error: %v", err),
			})
			continue
		}

		// Track timestamps
		if !msg.Timestamp.IsZero() {
			if result.StartTime.IsZero() || msg.Timestamp.Before(result.StartTime) {
				result.StartTime = msg.Timestamp
			}
			if msg.Timestamp.After(result.EndTime) {
				result.EndTime = msg.Timestamp
			}
		}

		switch msg.Type {
		case "file-history-snapshot", "queue-operation":
			continue
		case "progress":
			result.Progress++
			result.Messages++
			continue
		case "user":
			result.Users++
		case "assistant":
			result.Assistants++
		default:
			continue
		}

		result.Messages++

		if result.SessionID == "" && msg.SessionID != "" {
			result.SessionID = msg.SessionID
		}
		if result.Version == "" && msg.Version != "" {
			result.Version = msg.Version
		}

		if msg.Message == nil {
			continue
		}

		if result.Model == "" && msg.Message.Model != "" {
			result.Model = msg.Message.Model
		}

		if msg.Message.Usage != nil {
			result.Usages = append(result.Usages, *msg.Message.Usage)
		}

		blocks := ParseContentBlocks(msg.Message.Content)

		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				if block.ID != "" {
					result.ToolUses[block.ID] = &ToolUseEntry{
						ID:        block.ID,
						Name:      block.Name,
						Timestamp: msg.Timestamp,
						UUID:      msg.UUID,
					}
				}
			case "tool_result":
				if block.ToolUseID != "" {
					entry := &ToolResultEntry{
						ToolUseID: block.ToolUseID,
						Timestamp: msg.Timestamp,
						UUID:      msg.UUID,
						IsError:   block.IsError,
					}
					if msg.ToolUseResult != nil {
						entry.Interrupted = msg.ToolUseResult.Interrupted
					}
					result.ToolResults[block.ToolUseID] = entry
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("scan error at line %d: %w", lineNum, err)
	}

	return result, nil
}

// ParseContentBlocks handles the content field which can be a string or array.
func ParseContentBlocks(raw json.RawMessage) []ContentBlock {
	if len(raw) == 0 {
		return nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ContentBlock{{Type: "text", Text: s}}
	}

	return nil
}

// ExtractInputString extracts a string field from a tool_use input JSON.
func ExtractInputString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	val, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(val, &s); err != nil {
		return ""
	}
	return s
}
