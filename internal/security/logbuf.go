package security

import (
	"bytes"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a captured runtime log line from the LFH daemon.
type LogEntry struct {
	Seq       int64  `json:"seq"`
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	Message   string `json:"message"`
}

// LogBuffer implements io.Writer and retains a thread-safe in-memory ring of recent runtime logs.
type LogBuffer struct {
	mu         sync.Mutex
	maxEntries int
	seq        int64
	entries    []LogEntry
	partial    bytes.Buffer
}

// GlobalLogs captures standard Go logger output across all LFH packages.
var GlobalLogs = NewLogBuffer(1000)

func NewLogBuffer(maxEntries int) *LogBuffer {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &LogBuffer{
		maxEntries: maxEntries,
		entries:    make([]LogEntry, 0, maxEntries),
	}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, _ = b.partial.Write(p)
	data := b.partial.Bytes()
	for {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(data[:idx]), "\r")
		b.appendLineLocked(line)
		data = data[idx+1:]
	}
	rem := append([]byte(nil), data...)
	b.partial.Reset()
	_, _ = b.partial.Write(rem)
	return len(p), nil
}

func (b *LogBuffer) appendLineLocked(raw string) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	msg := stripGoLogPrefix(line, &ts)

	source := "system"
	if strings.HasPrefix(msg, "[") {
		if end := strings.IndexByte(msg, ']'); end > 1 && end <= 24 {
			source = strings.ToLower(strings.TrimSpace(msg[1:end]))
			msg = strings.TrimSpace(msg[end+1:])
		}
	}

	level := classifyLogLevel(msg)
	b.seq++
	entry := LogEntry{
		Seq:       b.seq,
		Timestamp: ts,
		Level:     level,
		Source:    source,
		Message:   msg,
	}
	if len(b.entries) >= b.maxEntries {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = entry
	} else {
		b.entries = append(b.entries, entry)
	}
}

func stripGoLogPrefix(line string, tsOut *string) string {
	// Standard Go logger prefixes lines with "2006/01/02 15:04:05 ".
	if len(line) >= 20 && line[4] == '/' && line[7] == '/' && line[10] == ' ' && line[13] == ':' && line[16] == ':' && line[19] == ' ' {
		if parsed, err := time.ParseInLocation("2006/01/02 15:04:05", line[:19], time.Local); err == nil {
			*tsOut = parsed.UTC().Format(time.RFC3339)
		}
		return strings.TrimSpace(line[20:])
	}
	return line
}

func classifyLogLevel(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "fatal") ||
		strings.Contains(low, "panic") || strings.Contains(msg, "失败") || strings.Contains(msg, "异常"):
		return "ERROR"
	case strings.Contains(low, "warn") || strings.Contains(low, "mismatch") || strings.Contains(low, "timeout") ||
		strings.Contains(low, "offline") || strings.Contains(msg, "警告") || strings.Contains(msg, "重试"):
		return "WARN"
	default:
		return "INFO"
	}
}

// List returns matching runtime log entries newest-first, plus total buffered count and per-level totals.
func (b *LogBuffer) List(limit int, level, query string) (entries []LogEntry, total int, counts map[string]int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if limit <= 0 {
		limit = 200
	}
	if limit > b.maxEntries {
		limit = b.maxEntries
	}

	wantLevel := strings.ToUpper(strings.TrimSpace(level))
	if wantLevel == "ALL" {
		wantLevel = ""
	}
	q := strings.ToLower(strings.TrimSpace(query))

	counts = map[string]int{
		"INFO":  0,
		"WARN":  0,
		"ERROR": 0,
	}
	total = len(b.entries)
	out := make([]LogEntry, 0, min(limit, total))

	for i := len(b.entries) - 1; i >= 0; i-- {
		e := b.entries[i]
		counts[e.Level]++
		if wantLevel != "" && e.Level != wantLevel {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Message), q) && !strings.Contains(strings.ToLower(e.Source), q) {
			continue
		}
		if len(out) < limit {
			out = append(out, e)
		}
	}
	return out, total, counts
}

// Clear empties all stored log entries.
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = b.entries[:0]
	b.partial.Reset()
}
