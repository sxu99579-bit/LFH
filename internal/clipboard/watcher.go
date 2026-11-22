package clipboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

const MaxCaptureBytes = 20 << 20 // 20 MB per clipboard capture

// Item represents a captured clipboard payload ready for upload.
type Item struct {
	Kind      string    `json:"kind"` // "text" or "image"
	Ext       string    `json:"ext"`  // ".txt", ".png", ".bmp"
	Data      []byte    `json:"-"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
}

// AutoFileName formats a deterministic timestamp-based filename for a clipboard item.
func AutoFileName(item Item) string {
	ts := item.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	ext := strings.TrimSpace(item.Ext)
	if ext == "" {
		if item.Kind == "image" {
			ext = ".png"
		} else {
			ext = ".txt"
		}
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return fmt.Sprintf("clip_%s%s", ts.Format("20060102_150405"), ext)
}

// FolderPath returns the virtual storage folder path for Super Clipboard uploads.
func FolderPath(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return "超级剪贴板/" + t.Format("2006-01")
}

func hashBytesHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Watcher monitors the local OS clipboard when enabled and invokes onCapture on new content.
type Watcher struct {
	mu           sync.Mutex
	enabled      bool
	needBaseline bool
	hasSeq       bool
	lastSeq      uint32
	lastHash     string
	pollInterval time.Duration
	seqFn        func() uint32
	readFn       func() (Item, bool)
	onCapture    func(Item)
}

// NewWatcher creates a clipboard watcher using the current OS clipboard backend.
func NewWatcher(onCapture func(Item)) *Watcher {
	return newWatcherWithDeps(defaultPollInterval, clipboardSequence, readClipboard, onCapture)
}

func newWatcherWithDeps(
	pollInterval time.Duration,
	seqFn func() uint32,
	readFn func() (Item, bool),
	onCapture func(Item),
) *Watcher {
	if pollInterval <= 0 {
		pollInterval = 800 * time.Millisecond
	}
	return &Watcher{
		pollInterval: pollInterval,
		seqFn:        seqFn,
		readFn:       readFn,
		onCapture:    onCapture,
	}
}

// Enabled reports whether clipboard capture is currently active.
func (w *Watcher) Enabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enabled
}

// SetEnabled turns clipboard capture on or off. When turned on from off, the next poll records baseline without uploading stale content.
func (w *Watcher) SetEnabled(enabled bool) {
	w.mu.Lock()
	if enabled && !w.enabled {
		w.needBaseline = true
		w.hasSeq = false
	}
	w.enabled = enabled
	if !enabled {
		w.needBaseline = false
	}
	w.mu.Unlock()
}

// Start runs the background polling loop until ctx is canceled.
func (w *Watcher) Start(ctx context.Context) {
	go func() {
		// Record initial baseline immediately if already enabled at startup.
		w.pollOnce(false)
		ticker := time.NewTicker(w.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.pollOnce(true)
			}
		}
	}()
}

// pollOnce inspects the clipboard once. When emitAsync is true, onCapture runs asynchronously.
func (w *Watcher) pollOnce(emitAsync bool) {
	w.mu.Lock()
	enabled := w.enabled
	needBaseline := w.needBaseline
	hasSeq := w.hasSeq
	lastSeq := w.lastSeq
	lastHash := w.lastHash
	seqFn := w.seqFn
	readFn := w.readFn
	cb := w.onCapture
	w.mu.Unlock()

	if !enabled || readFn == nil {
		return
	}

	var curSeq uint32
	if seqFn != nil {
		curSeq = seqFn()
	}
	// On Windows (where sequence number > 0), skip expensive GlobalLock/PNG encoding if unchanged.
	if !needBaseline && curSeq != 0 && hasSeq && curSeq == lastSeq {
		return
	}

	item, ok := readFn()
	if !ok || len(item.Data) == 0 || len(item.Data) > MaxCaptureBytes {
		w.mu.Lock()
		if curSeq != 0 {
			w.lastSeq = curSeq
			w.hasSeq = true
		}
		if w.needBaseline {
			w.lastHash = ""
			w.needBaseline = false
		}
		w.mu.Unlock()
		return
	}

	if item.Hash == "" {
		item.Hash = hashBytesHex(item.Data)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}

	w.mu.Lock()
	shouldEmit := !w.needBaseline && item.Hash != w.lastHash && w.enabled && cb != nil
	if curSeq != 0 {
		w.lastSeq = curSeq
		w.hasSeq = true
	}
	w.lastHash = item.Hash
	w.needBaseline = false
	w.mu.Unlock()

	if shouldEmit && item.Hash != lastHash {
		if emitAsync {
			go cb(item)
		} else {
			cb(item)
		}
	}
}
