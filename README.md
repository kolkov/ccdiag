# ccdiag — Claude Code Session Analyzer & Recovery Tool

Go CLI tool for analyzing and recovering Claude Code sessions from JSONL files.

Born from necessity: after months of [reverse-engineering Claude Code bugs](https://dev.to/kolkov/we-reverse-engineered-12-versions-of-claude-code-then-it-leaked-its-own-source-code-pij) and losing sessions to broken `--resume` ([#40319](https://github.com/anthropics/claude-code/issues/40319)), we built this tool to take control of our own data.

## Features

**Analyze** — detect orphaned tool calls, stuck tools, errors, token usage:
```bash
ccdiag session.jsonl              # analyze single session
ccdiag --scan-all                 # scan all sessions in ~/.claude/projects/
ccdiag --json session.jsonl       # JSON output for CI
```

**Recover** — extract full context from a session when `--resume` is broken:
```bash
ccdiag recover session.jsonl                          # session handoff summary
ccdiag recover --latest D:\projects\myproject          # find & recover latest session
ccdiag recover --output full -o recovery.md file.jsonl # full dump to file
ccdiag recover --output messages file.jsonl            # all user messages
ccdiag recover --output actions file.jsonl             # all tool actions chronologically
```

## Install

```bash
go install github.com/kolkov/ccdiag@latest
```

Or build from source:
```bash
git clone https://github.com/kolkov/ccdiag
cd ccdiag
go build -o ccdiag .
```

Zero external dependencies — Go stdlib only.

## Why

Claude Code stores sessions as JSONL files in `~/.claude/projects/`. The built-in `--resume` is broken since v2.1.85 ([#40319](https://github.com/anthropics/claude-code/issues/40319)) — the linked list walker stops at version boundaries, loading 0% of conversation history. Each failed resume attempt corrupts the JSONL further by forking the `parentUuid` DAG.

This tool reads JSONL files directly, bypassing the broken resume logic.

### What `recover` extracts

| Category | Details |
|----------|---------|
| **Files** | All Write/Edit operations with paths and counts |
| **GitHub** | Issue creates, comments, API calls — classified |
| **Git** | All git commands |
| **Web** | Searches and fetches |
| **URLs** | All referenced URLs |
| **Issues** | GitHub issue references (#N) with commented/created tracking |
| **Messages** | User messages (command noise filtered) |

### What `analyze` detects

| Problem | How |
|---------|-----|
| **Orphaned tool calls** | tool_use without matching tool_result (data loss) |
| **Stuck tools** | Exceeded configurable threshold (default 60s) |
| **Errors** | Tool results marked as errors |
| **Interrupted** | Tool executions interrupted mid-run |
| **Token usage** | Input, output, cache creation, cache read totals |

## Recovery output formats

**handoff** (default) — structured summary for starting a new session:
- Session stats (lines, messages, tool uses)
- Files modified (deduplicated, write/edit counts)
- GitHub issues referenced (with commented/created flags)
- GitHub/git/bash commands executed
- Web searches performed
- URLs referenced
- Last 20 user messages

**messages** — all user messages with line numbers

**actions** — all tool actions in chronological order

**full** — handoff + all messages combined

## Analyze examples

```bash
# Find sessions with orphaned tool calls
ccdiag --scan-all --orphans-only

# Detailed analysis with all tool call info
ccdiag -v session.jsonl

# Stuck tool threshold at 30 seconds
ccdiag --stuck-threshold 30s session.jsonl
```

Exit codes: `0` = clean, `2` = orphans found (useful for CI).

## Session file locations

| OS | Path |
|----|------|
| Windows | `%USERPROFILE%\.claude\projects\` |
| macOS/Linux | `~/.claude/projects/` |

Session files are named `{uuid}.jsonl` inside project-specific directories.

## Related issues

- [#40319](https://github.com/anthropics/claude-code/issues/40319) — Session resume loads zero history (regression v2.1.85)
- [#42376](https://github.com/anthropics/claude-code/issues/42376) — `--continue` silently drops context (v2.1.90)
- [#42338](https://github.com/anthropics/claude-code/issues/42338) — Resume invalidates prompt cache
- [#31328](https://github.com/anthropics/claude-code/issues/31328) — JSONL writer drops entries during parallel tool calls
- [#33949](https://github.com/anthropics/claude-code/issues/33949) — Root cause: SSE hang + ESC queue + JSONL race
- [#39755](https://github.com/anthropics/claude-code/issues/39755) — Watchdog fallback is dead code

## Stats

Built after analyzing **1,571 sessions** (148,444 tool calls, 8,007 orphaned = 5.4%) and reverse-engineering **12 versions** of Claude Code's `cli.js` (v2.1.74 through v2.1.91).

## License

MIT
