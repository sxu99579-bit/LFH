package server

import (
	"sync"
	"time"

	"lfh/internal/updater"
)

type FileChangeEvent struct {
	Version      uint64          `json:"version"`
	Kind         string          `json:"kind"` // "upload", "delete", "extract", "cluster_sync", "policy", "update_available", "update_progress", "upgrading"
	Uploader     string          `json:"uploader,omitempty"`
	Count        int             `json:"count,omitempty"`
	FileNames    []string        `json:"file_names,omitempty"`
	NodeName     string          `json:"node_name,omitempty"`
	UpdateStatus *updater.Status `json:"update_status,omitempty"`
	Timestamp    int64           `json:"timestamp"`
}

type EventHub struct {
	mu       sync.Mutex
	version  uint64
	subs     map[uint64]chan FileChangeEvent
	nextID   uint64
	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewEventHub() *EventHub {
	return &EventHub{
		version: 1,
		subs:    make(map[uint64]chan FileChangeEvent),
		stopCh:  make(chan struct{}),
	}
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
	subs := make([]chan FileChangeEvent, 0, len(h.subs))
	for _, ch := range h.subs {
		subs = append(subs, ch)
	}
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
	return ev.Version
}
