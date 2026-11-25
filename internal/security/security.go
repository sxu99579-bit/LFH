package security

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultMaxAuditEntries = 1000

// LoginLimiter enforces per-IP brute-force protection using a sliding failure window and lockout.
type LoginLimiter struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	lockout     time.Duration
	entries     map[string]*limitEntry
}

type limitEntry struct {
	failures    []time.Time
	lockedUntil time.Time
}

// NewLoginLimiter constructs an IP login rate limiter.
func NewLoginLimiter(maxFailures int, window, lockout time.Duration) *LoginLimiter {
	if maxFailures <= 0 {
		maxFailures = 8
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	if lockout <= 0 {
		lockout = 5 * time.Minute
	}
	return &LoginLimiter{
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
		entries:     make(map[string]*limitEntry),
	}
}

// ClientIP extracts the normalized client IP address from RemoteAddr (ignoring ports).
func ClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	raw := strings.TrimSpace(r.RemoteAddr)
	if raw == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(raw); err == nil && host != "" {
		return host
	}
	return raw
}

// Allow reports whether ip may attempt login right now and returns remaining lockout duration if blocked.
func (l *LoginLimiter) Allow(ip string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	ent, ok := l.entries[ip]
	if !ok {
		return true, 0
	}
	if !ent.lockedUntil.IsZero() {
		if now.Before(ent.lockedUntil) {
			return false, ent.lockedUntil.Sub(now)
		}
		ent.lockedUntil = time.Time{}
		ent.failures = nil
	}
	return true, 0
}

// RecordFailure records an invalid login attempt for ip and triggers lockout once threshold is reached.
func (l *LoginLimiter) RecordFailure(ip string) {
	if l == nil {
		return
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()
	ent, ok := l.entries[ip]
	if !ok {
		ent = &limitEntry{}
		l.entries[ip] = ent
	}
	kept := ent.failures[:0]
	for _, ts := range ent.failures {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	ent.failures = kept
	if len(ent.failures) >= l.maxFailures {
		ent.lockedUntil = now.Add(l.lockout)
		ent.failures = nil
	}
	if len(l.entries) > 2048 {
		for k, v := range l.entries {
			if (v.lockedUntil.IsZero() || now.After(v.lockedUntil)) &&
				(len(v.failures) == 0 || v.failures[len(v.failures)-1].Before(cutoff)) {
				delete(l.entries, k)
			}
		}
	}
}

// Reset clears failure counters and lockouts for ip after a successful login.
func (l *LoginLimiter) Reset(ip string) {
	if l == nil {
		return
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return
	}
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

// AuditEntry records a security-relevant administrative or authentication event.
type AuditEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	IP        string    `json:"ip,omitempty"`
	Status    string    `json:"status"` // ok, denied, error
	Detail    string    `json:"detail,omitempty"`
}

// AuditLogger maintains a bounded persistent ring of recent AuditEntry records newest-first.
type AuditLogger struct {
	mu         sync.RWMutex
	path       string
	maxEntries int
	entries    []AuditEntry
}

// NewAuditLogger creates or loads a persistent audit log in dataDir/audit.json.
func NewAuditLogger(dataDir string) (*AuditLogger, error) {
	return newAuditLoggerWithCap(dataDir, defaultMaxAuditEntries)
}

func newAuditLoggerWithCap(dataDir string, maxEntries int) (*AuditLogger, error) {
	if maxEntries <= 0 {
		maxEntries = defaultMaxAuditEntries
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	al := &AuditLogger{
		path:       filepath.Join(dataDir, "audit.json"),
		maxEntries: maxEntries,
	}
	if raw, err := os.ReadFile(al.path); err == nil {
		var loaded []AuditEntry
		if json.Unmarshal(raw, &loaded) == nil {
			if len(loaded) > al.maxEntries {
				loaded = loaded[:al.maxEntries]
			}
			al.entries = loaded
		}
	}
	return al, nil
}

func newAuditID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Log appends a new event at the front of the audit log and persists it to disk.
func (al *AuditLogger) Log(actor, action, target, ip, status, detail string) {
	if al == nil {
		return
	}
	entry := AuditEntry{
		ID:        newAuditID(),
		Timestamp: time.Now(),
		Actor:     strings.TrimSpace(actor),
		Action:    strings.TrimSpace(action),
		Target:    strings.TrimSpace(target),
		IP:        strings.TrimSpace(ip),
		Status:    strings.TrimSpace(status),
		Detail:    strings.TrimSpace(detail),
	}
	if entry.Actor == "" {
		entry.Actor = "-"
	}
	if entry.Status == "" {
		entry.Status = "ok"
	}

	al.mu.Lock()
	al.entries = append([]AuditEntry{entry}, al.entries...)
	if len(al.entries) > al.maxEntries {
		al.entries = al.entries[:al.maxEntries]
	}
	raw, err := json.MarshalIndent(al.entries, "", "  ")
	al.mu.Unlock()
	if err == nil && al.path != "" {
		_ = os.WriteFile(al.path, raw, 0644)
	}
}

// List returns up to limit most recent audit entries (newest first).
func (al *AuditLogger) List(limit int) []AuditEntry {
	if al == nil {
		return nil
	}
	al.mu.RLock()
	defer al.mu.RUnlock()
	n := len(al.entries)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]AuditEntry, n)
	copy(out, al.entries[:n])
	return out
}

// RecentByActor returns up to limit newest audit entries where actor or target matches username.
func (al *AuditLogger) RecentByActor(actor string, limit int) []AuditEntry {
	if al == nil {
		return nil
	}
	clean := strings.TrimSpace(actor)
	if clean == "" {
		return al.List(limit)
	}
	if limit <= 0 {
		limit = 50
	}
	al.mu.RLock()
	defer al.mu.RUnlock()
	out := make([]AuditEntry, 0, min(limit, len(al.entries)))
	for _, e := range al.entries {
		if strings.EqualFold(e.Actor, clean) || strings.EqualFold(e.Target, clean) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
