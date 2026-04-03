# ccdiag — Claude Code Session Analyzer, Recovery & Proxy Tool

Go CLI for analyzing, recovering, and monitoring Claude Code sessions. Three tools in one binary:

- **Analyze** — detect orphaned tool calls, stuck tools, errors, token usage
- **Recover** — extract full context from broken sessions when `--resume` fails
- **Proxy** — transparent reverse proxy for real-time LLM API traffic logging and cost tracking

Born from necessity: after months of [reverse-engineering Claude Code bugs](https://dev.to/kolkov/we-reverse-engineered-12-versions-of-claude-code-then-it-leaked-its-own-source-code-pij) and losing sessions to broken `--resume` ([#40319](https://github.com/anthropics/claude-code/issues/40319), [#43044](https://github.com/anthropics/claude-code/issues/43044)), we built this tool to take control of our own data.

## Install

```bash
go install github.com/kolkov/ccdiag@latest
```

Or build from source:
```bash
git clone https://github.com/kolkov/ccdiag
cd ccdiag
go build ./cmd/ccdiag/
```

Zero external dependencies — Go stdlib only.

## Proxy — Real-time API Traffic Monitoring

Transparent reverse proxy that sits between Claude Code (or any LLM client) and Anthropic API, logging every request with token usage, cache metrics, and cost estimation.

```bash
# Start proxy (default port 9119)
ccdiag proxy
ccdiag proxy --port 8080 --verbose

# Configure Claude Code to use proxy (per-project):
# .claude/settings.local.json:
# { "env": { "ANTHROPIC_BASE_URL": "http://localhost:9119" } }

# View traffic stats
ccdiag proxy stats                    # all traffic
ccdiag proxy stats --last 1h          # last hour
ccdiag proxy stats --last 24h         # last day
ccdiag proxy stats --cost             # cost breakdown
ccdiag proxy stats --json             # machine-readable
```

### What proxy captures

| Metric | Details |
|--------|---------|
| **Tokens** | input, output, cache_creation, cache_read per request |
| **Cache ratio** | Weighted cache hit ratio — detect cache invalidation bugs |
| **Cost** | Estimated USD by model (Opus/Sonnet/Haiku pricing) |
| **Latency** | Total request time + TTFB for streaming |
| **Session ID** | Extracted from request metadata — per-session log files |
| **Model** | Actual model used (detect silent Opus→Sonnet fallback) |
| **Errors** | Auth errors, rate limits, overloaded (529) |

### Per-session logging

Each Claude Code session gets its own log file in `~/.ccdiag/proxy/`:
```
~/.ccdiag/proxy/
├── 665fee43-fc87-4142-a4b0-ad175a14aff4.jsonl   # session traffic
├── 89a8c84d-2bfe-4585-be65-ae05f0236ad2.jsonl   # another session
└── _system.jsonl                                  # requests without session_id
```

Session file names match Claude Code session UUIDs — direct correlation with `~/.claude/projects/` session JSONL files.

### Real-time stderr output (--verbose)

```
[12:00:05] claude-opus-4-6 | 3 in | 6 out | cache 99.9% | 3.2s
[12:00:08] claude-opus-4-6 | 3 in | 26 out | cache 97.3% | 4.5s
[12:01:15] claude-haiku-4.5 | 401 authentication_error | 0.2s
```

## Recover — Session Context Extraction

When `--resume` is broken, extract full context directly from JSONL files:

```bash
ccdiag recover session.jsonl                          # session handoff summary
ccdiag recover --latest D:\projects\myproject          # find & recover latest session
ccdiag recover --output full -o recovery.md file.jsonl # full dump to file
ccdiag recover --output messages file.jsonl            # all user messages
ccdiag recover --output actions file.jsonl             # all tool actions chronologically
```

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

### Output formats

- **handoff** (default) — structured summary for starting a new session
- **messages** — all user messages with line numbers
- **actions** — all tool actions in chronological order
- **full** — handoff + all messages combined

## Analyze — Session Health Diagnostics

```bash
ccdiag session.jsonl              # analyze single session
ccdiag --scan-all                 # scan all sessions in ~/.claude/projects/
ccdiag --json session.jsonl       # JSON output for CI
ccdiag --scan-all --orphans-only  # find sessions with data loss
ccdiag -v session.jsonl           # verbose with all tool call details
```

### What `analyze` detects

| Problem | How |
|---------|-----|
| **Orphaned tool calls** | tool_use without matching tool_result (data loss) |
| **Stuck tools** | Exceeded configurable threshold (default 60s) |
| **Errors** | Tool results marked as errors |
| **Interrupted** | Tool executions interrupted mid-run |
| **Token usage** | Input, output, cache creation, cache read totals |

Exit codes: `0` = clean, `2` = orphans found (useful for CI).

## Why

Claude Code stores sessions as JSONL files in `~/.claude/projects/`. The built-in `--resume` is broken since v2.1.85 — the linked list walker stops at version boundaries, loading 0% of conversation history. Each failed resume attempt corrupts the JSONL further by forking the `parentUuid` DAG. Source code analysis (v2.1.88 leaked TypeScript vs v2.1.91 minified cli.js) revealed three specific regressions:

1. `walkChainBeforeParse` removed — fork pruning for >5 MB files no longer happens
2. New `ExY` timestamp fallback bridges across unrelated fork branches
3. Missing `leafUuids` check in `getLastSessionLog`

Full analysis: [#43044](https://github.com/anthropics/claude-code/issues/43044)

## Session file locations

| OS | Path |
|----|------|
| Windows | `%USERPROFILE%\.claude\projects\` |
| macOS/Linux | `~/.claude/projects/` |

## Related issues

- [#43044](https://github.com/anthropics/claude-code/issues/43044) — Resume loads 0% context on v2.1.91 (our analysis)
- [#40319](https://github.com/anthropics/claude-code/issues/40319) — Session resume loads zero history (v2.1.85+)
- [#42376](https://github.com/anthropics/claude-code/issues/42376) — `--continue` silently drops context (v2.1.90)
- [#42542](https://github.com/anthropics/claude-code/issues/42542) — Silent microcompact clears tool results
- [#42338](https://github.com/anthropics/claude-code/issues/42338) — Resume invalidates prompt cache
- [#33949](https://github.com/anthropics/claude-code/issues/33949) — Root cause: SSE hang + ESC queue + JSONL race
- [#39755](https://github.com/anthropics/claude-code/issues/39755) — Watchdog fallback is dead code

## Stats

Built after analyzing **1,571 sessions** (148,444 tool calls, 8,007 orphaned = 5.4%) and reverse-engineering **12 versions** of Claude Code's `cli.js` (v2.1.74 through v2.1.91).

## License

MIT
