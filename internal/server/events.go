package server

import (
	"sync"
	"time"

	"lfh/internal/updater"
)

type FileChangeEvent struct {
	Version      uint64          `json:"version"`
	Kind         string          `json:"kind"` // "upload", "delete", "extract", "cluster_sync", "entry_switched", "policy", "access", "role_change", "update_available", "update_progress", "upgrading"
	Uploader     string          `json:"uploader,omitempty"`
	ActorName    string          `json:"actor_name,omitempty"`
	Count        int             `json:"count,omitempty"`
	FileNames    []string        `json:"file_names,omitempty"`
	FolderPaths  []string        `json:"folder_paths,omitempty"`
	BatchID      string          `json:"batch_id,omitempty"`
	NodeName     string          `json:"node_name,omitempty"`
	EntryURL     string          `json:"entry_url,omitempty"`
	AllowedUsers []string        `json:"-"`
	UpdateStatus *updater.Status `json:"update_status,omitempty"`
	Timestamp    int64           `json:"timestamp"`
}

const maxEventHistory = 64

type EventHub struct {
	mu        sync.Mutex
	version   uint64
	subs      map[uint64]chan FileChangeEvent
	history   []FileChangeEvent
	nextID    uint64
	stopOnce  sync.Once
	stopCh    chan struct{}
	onPublish func(FileChangeEvent)
}

func NewEventHub() *EventHub {
	return &EventHub{
		version: 1,
		subs:    make(map[uint64]chan FileChangeEvent),
		history: make([]FileChangeEvent, 0, maxEventHistory),
		stopCh:  make(chan struct{}),
	}
}

// SetOnPublish installs an optional hook invoked synchronously outside h.mu after each published event.
func (h *EventHub) SetOnPublish(fn func(FileChangeEvent)) {
	h.mu.Lock()
	h.onPublish = fn
	h.mu.Unlock()
}

func (h *EventHub) Done() <-chan struct{} {
	return h.stopCh
}

func (h *EventHub) Close() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
}

func (h *EventHub) Version() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

func (h *EventHub) Subscribe() (<-chan FileChangeEvent, func()) {
	ch := make(chan FileChangeEvent, 16)
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.subs[id] = ch
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

func (h *EventHub) Publish(ev FileChangeEvent) uint64 {
	if len(ev.FileNames) > 3 {
		clipped := make([]string, 3)
		copy(clipped, ev.FileNames[:3])
		ev.FileNames = clipped
	}

	h.mu.Lock()
	h.version++
	ev.Version = h.version
	ev.Timestamp = time.Now().UnixMilli()
	h.history = append(h.history, ev)
	if len(h.history) > maxEventHistory {
		copy(h.history, h.history[len(h.history)-maxEventHistory:])
		h.history = h.history[:maxEventHistory]
	}
	subs := make([]chan FileChangeEvent, 0, len(h.subs))
	for _, ch := range h.subs {
		subs = append(subs, ch)
	}
	onPub := h.onPublish
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Drop oldest queued event for slow readers so writers never block.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
	if onPub != nil {
		onPub(ev)
	}
	return ev.Version
}

func (h *EventHub) EventsSince(sinceVersion uint64) (events []FileChangeEvent, currentVersion uint64, gap bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	currentVersion = h.version
	if sinceVersion == 0 || sinceVersion >= currentVersion {
		return nil, currentVersion, false
	}
	if len(h.history) == 0 || h.history[0].Version > sinceVersion+1 {
		gap = true
	}
	for _, ev := range h.history {
		if ev.Version > sinceVersion {
			events = append(events, ev)
		}
	}
	return events, currentVersion, gap
}
