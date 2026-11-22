package clipboard

import (
	"sync"
	"testing"
	"time"
)

func TestAutoFileNameAndFolderPath(t *testing.T) {
	ts := time.Date(2026, 11, 22, 9, 45, 12, 0, time.Local)
	txtName := AutoFileName(Item{Kind: "text", Ext: ".txt", CreatedAt: ts})
	if txtName != "clip_20261122_094512.txt" {
		t.Fatalf("unexpected text filename: %q", txtName)
	}
	imgName := AutoFileName(Item{Kind: "image", Ext: ".png", CreatedAt: ts})
	if imgName != "clip_20261122_094512.png" {
		t.Fatalf("unexpected image filename: %q", imgName)
	}
	folder := FolderPath(ts)
	if folder != "超级剪贴板/2026-11" {
		t.Fatalf("unexpected folder path: %q", folder)
	}
}

func TestWatcherBaselineDeduplicationAndToggle(t *testing.T) {
	var mu sync.Mutex
	curSeq := uint32(1)
	curText := "stale-before-enable"
	captured := make([]Item, 0)

	w := newWatcherWithDeps(
		20*time.Millisecond,
		func() uint32 {
			mu.Lock()
			defer mu.Unlock()
			return curSeq
		},
		func() (Item, bool) {
			mu.Lock()
			defer mu.Unlock()
			if curText == "" {
				return Item{}, false
			}
			b := []byte(curText)
			return Item{
				Kind:      "text",
				Ext:       ".txt",
				Data:      b,
				Hash:      hashBytesHex(b),
				CreatedAt: time.Now(),
			}, true
		},
		func(it Item) {
			mu.Lock()
			captured = append(captured, it)
			mu.Unlock()
		},
	)

	// 1. Disabled by default: polling captures nothing.
	w.pollOnce(false)
	if len(captured) != 0 {
		t.Fatalf("expected 0 captures while disabled, got %d", len(captured))
	}

	// 2. Enabling records baseline without emitting stale content.
	w.SetEnabled(true)
	w.pollOnce(false)
	if len(captured) != 0 {
		t.Fatalf("expected baseline poll not to emit stale content, got %d", len(captured))
	}

	// 3. New clipboard change emits once; repeated polls with same seq/hash do not duplicate.
	mu.Lock()
	curSeq = 2
	curText = "copied line 1"
	mu.Unlock()

	w.pollOnce(false)
	w.pollOnce(false)
	if len(captured) != 1 || string(captured[0].Data) != "copied line 1" {
		t.Fatalf("expected exactly 1 capture of 'copied line 1', got %+v", captured)
	}

	// 4. Even if seq increments with identical hash, content deduplication skips it.
	mu.Lock()
	curSeq = 3
	mu.Unlock()
	w.pollOnce(false)
	if len(captured) != 1 {
		t.Fatalf("expected identical hash not to re-emit, got %d", len(captured))
	}

	// 5. Disable then change clipboard then re-enable -> does not emit content copied while off.
	w.SetEnabled(false)
	mu.Lock()
	curSeq = 4
	curText = "secret-while-off"
	mu.Unlock()
	w.pollOnce(false)

	w.SetEnabled(true)
	w.pollOnce(false)
	if len(captured) != 1 {
		t.Fatalf("expected re-enable baseline to skip 'secret-while-off', got %d", len(captured))
	}

	// 6. Copying new content after re-enable emits cleanly.
	mu.Lock()
	curSeq = 5
	curText = "after-re-enable"
	mu.Unlock()
	w.pollOnce(false)
	if len(captured) != 2 || string(captured[1].Data) != "after-re-enable" {
		t.Fatalf("expected capture #2 'after-re-enable', got %+v", captured)
	}
}
