package bridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
)

const (
	EventFileUploaded    = "file.uploaded"
	EventFileUpdated     = "file.updated"
	EventFileBatchCommit = "file.batch_commit"
	EventFileDeleted     = "file.deleted"
	EventVerifyCompleted = "verify.completed"

	webhookQueueCap   = 256
	webhookWorkerPool = 4
)

var validWebhookEvents = map[string]bool{
	EventFileUploaded:    true,
	EventFileUpdated:     true,
	EventFileBatchCommit: true,
	EventFileDeleted:     true,
	EventVerifyCompleted: true,
}

type WebhookRule struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	FolderPrefix          string     `json:"folder_prefix"`
	Events                []string   `json:"events"`
	TargetURL             string     `json:"target_url"`
	Secret                string     `json:"secret,omitempty"`
	Enabled               bool       `json:"enabled"`
	IncludeDownloadTicket bool       `json:"include_download_ticket,omitempty"`
	CreatedBy             string     `json:"created_by,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	LastStatus            string     `json:"last_status,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	LastSentAt            *time.Time `json:"last_sent_at,omitempty"`
}

// PublicWebhookRule omits the signing Secret when exposed over APIs.
type PublicWebhookRule struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	FolderPrefix          string     `json:"folder_prefix"`
	Events                []string   `json:"events"`
	TargetURL             string     `json:"target_url"`
	HasSecret             bool       `json:"has_secret"`
	Enabled               bool       `json:"enabled"`
	IncludeDownloadTicket bool       `json:"include_download_ticket,omitempty"`
	CreatedBy             string     `json:"created_by,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	LastStatus            string     `json:"last_status,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	LastSentAt            *time.Time `json:"last_sent_at,omitempty"`
}

type CreateWebhookInput struct {
	Name                  string   `json:"name"`
	FolderPrefix          string   `json:"folder_prefix"`
	Events                []string `json:"events"`
	TargetURL             string   `json:"target_url"`
	Secret                string   `json:"secret"`
	Enabled               *bool    `json:"enabled,omitempty"`
	IncludeDownloadTicket bool     `json:"include_download_ticket,omitempty"`
}

type UpdateWebhookInput struct {
	Name                  *string   `json:"name,omitempty"`
	FolderPrefix          *string   `json:"folder_prefix,omitempty"`
	Events                *[]string `json:"events,omitempty"`
	TargetURL             *string   `json:"target_url,omitempty"`
	Secret                *string   `json:"secret,omitempty"`
	Enabled               *bool     `json:"enabled,omitempty"`
	IncludeDownloadTicket *bool     `json:"include_download_ticket,omitempty"`
}

type WebhookEvent struct {
	Event             string        `json:"event"`
	Version           uint64        `json:"version,omitempty"`
	Uploader          string        `json:"uploader,omitempty"`
	ActorName         string        `json:"actor_name,omitempty"`
	Count             int           `json:"count,omitempty"`
	FileNames         []string      `json:"file_names,omitempty"`
	FolderPaths       []string      `json:"folder_paths,omitempty"`
	BatchID           string        `json:"batch_id,omitempty"`
	NodeName          string        `json:"node_name,omitempty"`
	Timestamp         int64         `json:"timestamp"`
	DownloadTicketURL string        `json:"download_ticket_url,omitempty"`
	VerifyResult      *VerifyResult `json:"verify_result,omitempty"`
}

type WebhookTestResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type webhookDelivery struct {
	rule WebhookRule
	ev   WebhookEvent
}

type WebhookManager struct {
	mu          sync.RWMutex
	filePath    string
	rules       map[string]WebhookRule
	httpClient  *http.Client
	retryDelays []time.Duration

	queueMu   sync.RWMutex
	closed    bool
	started   bool
	queue     chan webhookDelivery
	wg        sync.WaitGroup
	stopCh    chan struct{}
	closeOnce sync.Once
}

func NewWebhookManager(dataDir string) (*WebhookManager, error) {
	m := &WebhookManager{
		filePath:    filepath.Join(dataDir, "webhooks.json"),
		rules:       make(map[string]WebhookRule),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		retryDelays: []time.Duration{1 * time.Second, 4 * time.Second, 12 * time.Second},
		queue:       make(chan webhookDelivery, webhookQueueCap),
		stopCh:      make(chan struct{}),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// SetRetryDelaysForTest overrides backoff delays for fast unit tests.
func (m *WebhookManager) SetRetryDelaysForTest(delays []time.Duration) {
	m.mu.Lock()
	m.retryDelays = append([]time.Duration(nil), delays...)
	m.mu.Unlock()
}

func (m *WebhookManager) ensureWorkersLocked() {
	if m.started || m.closed {
		return
	}
	m.started = true
	for i := 0; i < webhookWorkerPool; i++ {
		m.wg.Add(1)
		go m.runWorker()
	}
}

func (m *WebhookManager) runWorker() {
	defer m.wg.Done()
	for job := range m.queue {
		m.deliverWithRetry(job.rule, job.ev)
	}
}

func (m *WebhookManager) Close() {
	m.closeOnce.Do(func() {
		m.queueMu.Lock()
		m.closed = true
		close(m.stopCh)
		close(m.queue)
		started := m.started
		m.queueMu.Unlock()
		if started {
			m.wg.Wait()
		}
	})
}

func (m *WebhookManager) load() error {
	raw, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []WebhookRule
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for _, r := range list {
		if r.ID != "" {
			m.rules[r.ID] = r
		}
	}
	return nil
}

func (m *WebhookManager) saveLocked() error {
	list := make([]WebhookRule, 0, len(m.rules))
	for _, r := range m.rules {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".webhooks.json.tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, m.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func toPublicWebhook(r WebhookRule) PublicWebhookRule {
	return PublicWebhookRule{
		ID:                    r.ID,
		Name:                  r.Name,
		FolderPrefix:          r.FolderPrefix,
		Events:                append([]string(nil), r.Events...),
		TargetURL:             r.TargetURL,
		HasSecret:             r.Secret != "",
		Enabled:               r.Enabled,
		IncludeDownloadTicket: r.IncludeDownloadTicket,
		CreatedBy:             r.CreatedBy,
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
		LastStatus:            r.LastStatus,
		LastError:             r.LastError,
		LastSentAt:            r.LastSentAt,
	}
}

func validateTargetURL(raw string) (string, error) {
	uStr := strings.TrimSpace(raw)
	u, err := url.Parse(uStr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(u.Host) == "" {
		return "", errors.New("target_url 必须为合法的 http 或 https URL")
	}
	return uStr, nil
}

func normalizeWebhookEvents(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{EventFileUploaded, EventFileBatchCommit, EventFileDeleted, EventVerifyCompleted}, nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, ev := range raw {
		clean := strings.ToLower(strings.TrimSpace(ev))
		if clean == "" {
			continue
		}
		if clean != "*" && !validWebhookEvents[clean] {
			return nil, fmt.Errorf("不支持的 Webhook 事件类型: %s", clean)
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("请至少选择一个 Webhook 触发事件")
	}
	return out, nil
}

func normalizeWebhookPrefix(raw string) (string, error) {
	trimmed := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if trimmed == "" || trimmed == "*" {
		return trimmed, nil
	}
	validated, err := auth.ValidateFolderRules([]string{trimmed})
	if err != nil || len(validated) == 0 {
		return "", errors.New("folder_prefix 格式无效")
	}
	return validated[0], nil
}

func (m *WebhookManager) List() []PublicWebhookRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PublicWebhookRule, 0, len(m.rules))
	for _, r := range m.rules {
		out = append(out, toPublicWebhook(r))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *WebhookManager) Create(in CreateWebhookInput, createdBy string) (PublicWebhookRule, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return PublicWebhookRule{}, errors.New("Webhook 规则名称不能为空")
	}
	targetURL, err := validateTargetURL(in.TargetURL)
	if err != nil {
		return PublicWebhookRule{}, err
	}
	prefix, err := normalizeWebhookPrefix(in.FolderPrefix)
	if err != nil {
		return PublicWebhookRule{}, err
	}
	events, err := normalizeWebhookEvents(in.Events)
	if err != nil {
		return PublicWebhookRule{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now()
	rule := WebhookRule{
		ID:                    "whk_" + randomHexNonce(6),
		Name:                  name,
		FolderPrefix:          prefix,
		Events:                events,
		TargetURL:             targetURL,
		Secret:                strings.TrimSpace(in.Secret),
		Enabled:               enabled,
		IncludeDownloadTicket: in.IncludeDownloadTicket,
		CreatedBy:             strings.TrimSpace(createdBy),
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.rules[rule.ID]
	m.rules[rule.ID] = rule
	if err := m.saveLocked(); err != nil {
		if prev.ID == "" {
			delete(m.rules, rule.ID)
		} else {
			m.rules[rule.ID] = prev
		}
		return PublicWebhookRule{}, err
	}
	return toPublicWebhook(rule), nil
}

func (m *WebhookManager) Update(id string, in UpdateWebhookInput) (PublicWebhookRule, error) {
	wid := strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rules[wid]
	if !ok {
		return PublicWebhookRule{}, errors.New("Webhook 规则不存在")
	}
	prev := cur
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return PublicWebhookRule{}, errors.New("Webhook 规则名称不能为空")
		}
		cur.Name = n
	}
	if in.TargetURL != nil {
		u, err := validateTargetURL(*in.TargetURL)
		if err != nil {
			return PublicWebhookRule{}, err
		}
		cur.TargetURL = u
	}
	if in.FolderPrefix != nil {
		p, err := normalizeWebhookPrefix(*in.FolderPrefix)
		if err != nil {
			return PublicWebhookRule{}, err
		}
		cur.FolderPrefix = p
	}
	if in.Events != nil {
		evs, err := normalizeWebhookEvents(*in.Events)
		if err != nil {
			return PublicWebhookRule{}, err
		}
		cur.Events = evs
	}
	if in.Secret != nil {
		cur.Secret = strings.TrimSpace(*in.Secret)
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.IncludeDownloadTicket != nil {
		cur.IncludeDownloadTicket = *in.IncludeDownloadTicket
	}
	cur.UpdatedAt = time.Now()
	m.rules[wid] = cur
	if err := m.saveLocked(); err != nil {
		m.rules[wid] = prev
		return PublicWebhookRule{}, err
	}
	return toPublicWebhook(cur), nil
}

func (m *WebhookManager) Delete(id string) error {
	wid := strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.rules[wid]
	if !ok {
		return errors.New("Webhook 规则不存在")
	}
	delete(m.rules, wid)
	if err := m.saveLocked(); err != nil {
		m.rules[wid] = prev
		return err
	}
	return nil
}

func ruleMatchesEvent(r WebhookRule, ev WebhookEvent) bool {
	if !r.Enabled {
		return false
	}
	eventMatched := false
	for _, subscribed := range r.Events {
		if subscribed == "*" || subscribed == ev.Event {
			eventMatched = true
			break
		}
	}
	if !eventMatched {
		return false
	}
	if r.FolderPrefix == "" || r.FolderPrefix == "*" {
		return true
	}
	for _, fp := range ev.FolderPaths {
		if auth.FolderMatchesPrefix(fp, r.FolderPrefix) {
			return true
		}
	}
	return false
}

// Dispatch enqueues ev for all matching enabled rules without blocking callers.
func (m *WebhookManager) Dispatch(ev WebhookEvent) {
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	m.mu.RLock()
	var matched []WebhookRule
	for _, r := range m.rules {
		if ruleMatchesEvent(r, ev) {
			matched = append(matched, r)
		}
	}
	m.mu.RUnlock()

	if len(matched) == 0 {
		return
	}

	m.queueMu.Lock()
	defer m.queueMu.Unlock()
	if m.closed {
		return
	}
	m.ensureWorkersLocked()
	for _, r := range matched {
		select {
		case m.queue <- webhookDelivery{rule: r, ev: ev}:
		default:
			log.Printf("[webhook] delivery queue full (cap=%d), dropped rule=%s event=%s", webhookQueueCap, r.Name, ev.Event)
		}
	}
}

// SendTest performs an immediate synchronous delivery to the named rule and records its status.
func (m *WebhookManager) SendTest(ctx context.Context, id string, triggeredBy string) (WebhookTestResult, error) {
	wid := strings.TrimSpace(id)
	m.mu.RLock()
	rule, ok := m.rules[wid]
	m.mu.RUnlock()
	if !ok {
		return WebhookTestResult{}, errors.New("Webhook 规则不存在")
	}

	ev := WebhookEvent{
		Event:       EventFileUploaded,
		Version:     0,
		Uploader:    triggeredBy,
		ActorName:   "LFH Webhook Test",
		Count:       1,
		FileNames:   []string{"webhook-test.txt"},
		FolderPaths: []string{rule.FolderPrefix},
		NodeName:    "LFH",
		Timestamp:   time.Now().UnixMilli(),
	}
	start := time.Now()
	code, err := m.sendOnce(ctx, rule, ev)
	dur := time.Since(start).Milliseconds()

	m.recordDeliveryStatus(rule.ID, code, err)
	if err != nil {
		return WebhookTestResult{
			OK:         false,
			StatusCode: code,
			DurationMS: dur,
			Error:      err.Error(),
		}, nil
	}
	return WebhookTestResult{
		OK:         true,
		StatusCode: code,
		DurationMS: dur,
	}, nil
}

func (m *WebhookManager) deliverWithRetry(rule WebhookRule, ev WebhookEvent) {
	m.mu.RLock()
	delays := append([]time.Duration(nil), m.retryDelays...)
	m.mu.RUnlock()

	attempts := len(delays) + 1
	var lastCode int
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			d := delays[attempt-1]
			timer := time.NewTimer(d)
			select {
			case <-m.stopCh:
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		code, err := m.sendOnce(ctx, rule, ev)
		cancel()
		lastCode = code
		lastErr = err
		if err == nil {
			m.recordDeliveryStatus(rule.ID, code, nil)
			log.Printf("[webhook] delivered event=%s rule=%s status=%d", ev.Event, rule.Name, code)
			return
		}
	}

	m.recordDeliveryStatus(rule.ID, lastCode, lastErr)
	log.Printf("[webhook] delivery failed after %d attempts rule=%s event=%s status=%d err=%v", attempts, rule.Name, ev.Event, lastCode, lastErr)
}

func (m *WebhookManager) recordDeliveryStatus(ruleID string, statusCode int, err error) {
	now := time.Now()
	status := fmt.Sprintf("%d OK", statusCode)
	errText := ""
	if err != nil {
		if statusCode > 0 {
			status = fmt.Sprintf("HTTP %d", statusCode)
		} else {
			status = "ERROR"
		}
		errText = err.Error()
	}

	m.mu.Lock()
	if cur, ok := m.rules[ruleID]; ok {
		cur.LastStatus = status
		cur.LastError = errText
		cur.LastSentAt = &now
		m.rules[ruleID] = cur
		_ = m.saveLocked()
	}
	m.mu.Unlock()
}

func SignWebhookPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (m *WebhookManager) sendOnce(ctx context.Context, rule WebhookRule, ev WebhookEvent) (int, error) {
	if !rule.IncludeDownloadTicket {
		ev.DownloadTicketURL = ""
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rule.TargetURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LFH-Webhook/1.0")
	req.Header.Set("X-LFH-Event", ev.Event)
	req.Header.Set("X-LFH-Delivery", "dlv_"+randomHexNonce(6))
	if strings.TrimSpace(rule.Secret) != "" {
		req.Header.Set("X-LFH-Signature-256", SignWebhookPayload(rule.Secret, body))
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("接收端返回 HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
