package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger writes ProxyEntry records to per-session JSONL files.
// Each session_id gets its own file: {session_id}.jsonl
// Requests without session_id go to _system.jsonl
// It is safe for concurrent use.
type Logger struct {
	mu    sync.Mutex
	dir   string
	files map[string]*sessionFile // session_id -> open file
	done  chan struct{}
}

type sessionFile struct {
	file    *os.File
	encoder *json.Encoder
}

const systemSessionFile = "_system"

// NewLogger creates a traffic logger that writes to per-session files in dir.
// The directory is created if it doesn't exist.
func NewLogger(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	l := &Logger{
		dir:   dir,
		files: make(map[string]*sessionFile),
		done:  make(chan struct{}),
	}

	go l.flushLoop()

	return l, nil
}

// Log writes a single ProxyEntry to the appropriate session file.
func (l *Logger) Log(entry ProxyEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := entry.SessionID
	if key == "" {
		key = systemSessionFile
	}

	sf, err := l.getOrCreate(key)
	if err != nil {
		return err
	}

	return sf.encoder.Encode(entry)
}

// getOrCreate returns the open file for a session, creating it if needed.
// Must be called with mu held.
func (l *Logger) getOrCreate(sessionID string) (*sessionFile, error) {
	if sf, ok := l.files[sessionID]; ok {
		return sf, nil
	}

	filename := sessionID + ".jsonl"
	path := filepath.Join(l.dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open session log %s: %w", path, err)
	}

	sf := &sessionFile{
		file:    f,
		encoder: json.NewEncoder(f),
	}
	l.files[sessionID] = sf

	return sf, nil
}

// Close flushes and closes all open session files.
func (l *Logger) Close() error {
	close(l.done)
	l.mu.Lock()
	defer l.mu.Unlock()

	var firstErr error
	for _, sf := range l.files {
		if err := sf.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.files = nil
	return firstErr
}

func (l *Logger) flushLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			for _, sf := range l.files {
				_ = sf.file.Sync()
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}
