package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"lfh/internal/auth"
)

const (
	TaskStatusScheduled  = "scheduled"
	TaskStatusPending    = "pending"
	TaskStatusInProgress = "in_progress"
	TaskStatusPaused     = "paused"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"

	SubtaskStatusPending = "pending"
	SubtaskStatusClaimed = "claimed"
	SubtaskStatusDone    = "done"

	DefaultTaskLeaseSec     = 600
	MinTaskLeaseSec         = 30
	MaxTaskLeaseSec         = 3600
	DefaultMaxCommentRounds = 15
	MaxCommentRoundsCap     = 50
	MaxSubtasksPerTask      = 20
	MaxTasksStored          = 2000
	taskSweepInterval       = 5 * time.Second
)

var (
	ErrTaskNotFound            = errors.New("任务不存在")
	ErrSubtaskNotFound         = errors.New("子任务不存在")
	ErrTaskNotClaimable        = errors.New("当前任务状态不可认领")
	ErrTaskAssignedToOther     = errors.New("该任务已定向指派给其他 Agent")
	ErrTaskScheduledInFuture   = errors.New("定时任务尚未到约定执行时间")
	ErrSubtaskBlockedByDeps    = errors.New("前置依赖子任务尚未全部完成")
	ErrTaskAlreadyClaimed      = errors.New("该任务已被其他 Agent 抢单认领")
	ErrTaskLeaseNotHeld        = errors.New("当前调用方未持有该任务执行租约")
	ErrTaskSplitNotAllowed     = errors.New("该任务未开启自动拆分协同")
	ErrTaskAlreadySplit        = errors.New("该任务已经拆分子任务")
	ErrTaskCommentLimitReached = errors.New("已达到该任务讨论轮次上限，请等待人工拍板")
	ErrTaskForbidden           = errors.New("无权访问或操作该任务")
	ErrTaskInvalidInput        = errors.New("任务参数不合法")

	mentionPattern = regexp.MustCompile(`@([^\s@,:;!?()（）【】<>]+)`)
)

// TaskActor identifies the human or Agent principal operating on tasks.
type TaskActor struct {
	Username string
	Role     string // "admin" | "user"
	IsAgent  bool
	KeyID    string
	KeyName  string
	Alias    string
}

func (a TaskActor) ActorID() string {
	if a.IsAgent && strings.TrimSpace(a.KeyID) != "" {
		return strings.TrimSpace(a.KeyID)
	}
	return "user:" + strings.TrimSpace(a.Username)
}

func (a TaskActor) DisplayName() string {
	if strings.TrimSpace(a.Alias) != "" {
		return strings.TrimSpace(a.Alias)
	}
	if strings.TrimSpace(a.KeyName) != "" {
		return strings.TrimSpace(a.KeyName)
	}
	return strings.TrimSpace(a.Username)
}

func (a TaskActor) RoleTag() string {
	if a.IsAgent {
		return "agent"
	}
	return "user"
}

type TaskSubtask struct {
	ID                 string     `json:"id"`
	Seq                int        `json:"seq"`
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	AssignedAgent      string     `json:"assigned_agent,omitempty"`
	AssignedAgentLabel string     `json:"assigned_agent_label,omitempty"`
	Status             string     `json:"status"` // "pending" | "claimed" | "done"
	DependsOn          []int      `json:"depends_on,omitempty"`
	ClaimedByID        string     `json:"claimed_by_id,omitempty"`
	ClaimedByName      string     `json:"claimed_by_name,omitempty"`
	ClaimedAt          *time.Time `json:"claimed_at,omitempty"`
	LeaseExpiresAt     *time.Time `json:"lease_expires_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	OutputFile         string     `json:"output_file,omitempty"`
	ResultSummary      string     `json:"result_summary,omitempty"`
}

type TaskComment struct {
	ID         string    `json:"id"`
	AuthorID   string    `json:"author_id,omitempty"`
	AuthorName string    `json:"author_name"`
	AuthorRole string    `json:"author_role"` // "agent" | "user" | "system"
	Mentions   []string  `json:"mentions,omitempty"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type AgentTask struct {
	ID                 string        `json:"id"`
	Title              string        `json:"title"`
	Description        string        `json:"description,omitempty"`
	Status             string        `json:"status"`   // "scheduled" | "pending" | "in_progress" | "paused" | "done" | "cancelled"
	Priority           string        `json:"priority"` // "normal" | "high" | "urgent"
	OwnerUsername      string        `json:"owner_username"`
	CreatedBy          string        `json:"created_by"`
	AssignedAgent      string        `json:"assigned_agent,omitempty"`
	AssignedAgentLabel string        `json:"assigned_agent_label,omitempty"`
	FolderPrefix       string        `json:"folder_prefix,omitempty"`
	AllowSplit         bool          `json:"allow_split"`
	ScheduledAt        *time.Time    `json:"scheduled_at,omitempty"`
	ClaimedByID        string        `json:"claimed_by_id,omitempty"`
	ClaimedByName      string        `json:"claimed_by_name,omitempty"`
	ClaimedAt          *time.Time    `json:"claimed_at,omitempty"`
	LeaseExpiresAt     *time.Time    `json:"lease_expires_at,omitempty"`
	CompletedAt        *time.Time    `json:"completed_at,omitempty"`
	OutputFiles        []string      `json:"output_files,omitempty"`
	ResultSummary      string        `json:"result_summary,omitempty"`
	Subtasks           []TaskSubtask `json:"subtasks,omitempty"`
	Comments           []TaskComment `json:"comments,omitempty"`
	MaxCommentRounds   int           `json:"max_comment_rounds"`
	PauseReason        string        `json:"pause_reason,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

type TaskListFilter struct {
	Status        string `json:"status,omitempty"`
	AssignedAgent string `json:"assigned_agent,omitempty"`
	FolderPrefix  string `json:"folder_prefix,omitempty"`
	ClaimableOnly bool   `json:"claimable_only,omitempty"`
	MentionedMe   bool   `json:"mentioned_me,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type TaskListSummary struct {
	Total          int `json:"total"`
	Scheduled      int `json:"scheduled"`
	Pending        int `json:"pending"`
	InProgress     int `json:"in_progress"`
	Paused         int `json:"paused"`
	Done           int `json:"done"`
	ClaimableForMe int `json:"claimable_for_me"`
	MentionedForMe int `json:"mentioned_for_me"`
}

type TaskListResult struct {
	Summary TaskListSummary `json:"summary"`
	Tasks   []AgentTask     `json:"tasks"`
}

type CreateTaskInput struct {
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	Priority           string     `json:"priority,omitempty"`
	AssignedAgent      string     `json:"assigned_agent,omitempty"`
	AssignedAgentLabel string     `json:"assigned_agent_label,omitempty"`
	FolderPrefix       string     `json:"folder_prefix,omitempty"`
	AllowSplit         *bool      `json:"allow_split,omitempty"`
	ScheduledAt        *time.Time `json:"scheduled_at,omitempty"`
	MaxCommentRounds   int        `json:"max_comment_rounds,omitempty"`
}

type UpdateTaskInput struct {
	Title              *string    `json:"title,omitempty"`
	Description        *string    `json:"description,omitempty"`
	Priority           *string    `json:"priority,omitempty"`
	AssignedAgent      *string    `json:"assigned_agent,omitempty"`
	AssignedAgentLabel *string    `json:"assigned_agent_label,omitempty"`
	FolderPrefix       *string    `json:"folder_prefix,omitempty"`
	AllowSplit         *bool      `json:"allow_split,omitempty"`
	ScheduledAt        *time.Time `json:"scheduled_at,omitempty"`
	ClearSchedule      bool       `json:"clear_schedule,omitempty"`
	Status             *string    `json:"status,omitempty"`
	MaxCommentRounds   *int       `json:"max_comment_rounds,omitempty"`
}

type ClaimTaskInput struct {
	TaskID       string `json:"task_id"`
	SubtaskID    string `json:"subtask_id,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type SplitSubtaskSpec struct {
	Title              string `json:"title"`
	Description        string `json:"description,omitempty"`
	AssignedAgent      string `json:"assigned_agent,omitempty"`
	AssignedAgentLabel string `json:"assigned_agent_label,omitempty"`
	DependsOn          []int  `json:"depends_on,omitempty"`
}

type SplitTaskInput struct {
	TaskID       string             `json:"task_id"`
	Subtasks     []SplitSubtaskSpec `json:"subtasks"`
	Note         string             `json:"note,omitempty"`
	ClaimFirst   *bool              `json:"claim_first,omitempty"`
	LeaseSeconds int                `json:"lease_seconds,omitempty"`
}

type PostCommentInput struct {
	TaskID string `json:"task_id"`
	Body   string `json:"body"`
}

type CompleteTaskInput struct {
	TaskID        string   `json:"task_id"`
	SubtaskID     string   `json:"subtask_id,omitempty"`
	OutputFiles   []string `json:"output_files,omitempty"`
	ResultSummary string   `json:"result_summary,omitempty"`
}

type ReleaseTaskInput struct {
	TaskID             string `json:"task_id"`
	SubtaskID          string `json:"subtask_id,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Pause              bool   `json:"pause,omitempty"`
	ReassignAgent      string `json:"reassign_agent,omitempty"`
	ReassignAgentLabel string `json:"reassign_agent_label,omitempty"`
}

type taskStoreSnapshot struct {
	Tasks []AgentTask `json:"tasks"`
}

// TaskManager coordinates multi-Agent task scheduling, atomic claiming, splitting, and discussions.
type TaskManager struct {
	mu       sync.Mutex
	path     string
	tasks    map[string]*AgentTask
	order    []string
	waitCh   chan struct{}
	onChange func(event string, task AgentTask)
	nowFn    func() time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func NewTaskManager(dataDir string) (*TaskManager, error) {
	cleanDir := strings.TrimSpace(dataDir)
	if cleanDir == "" {
		return nil, errors.New("数据目录不能为空")
	}
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return nil, err
	}
	m := &TaskManager{
		path:   filepath.Join(cleanDir, "agent-tasks.json"),
		tasks:  make(map[string]*AgentTask),
		waitCh: make(chan struct{}),
		nowFn:  time.Now,
		stopCh: make(chan struct{}),
	}
	if err := m.loadFromDisk(); err != nil {
		return nil, err
	}
	m.wg.Add(1)
	go m.sweepLoop()
	return m, nil
}

func (m *TaskManager) Close() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	m.wg.Wait()
}

func (m *TaskManager) SetOnChange(fn func(event string, task AgentTask)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *TaskManager) WaitChangeCh() <-chan struct{} {
	m.mu.Lock()
	ch := m.waitCh
	m.mu.Unlock()
	return ch
}

func (m *TaskManager) notifyWaitersLocked() {
	close(m.waitCh)
	m.waitCh = make(chan struct{})
}

func (m *TaskManager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

func (m *TaskManager) sweepLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(taskSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.RunSweep()
		}
	}
}

type taskChangeNotification struct {
	event string
	task  AgentTask
}

// RunSweep activates due scheduled tasks and releases expired claim leases.
func (m *TaskManager) RunSweep() {
	now := m.now()
	m.mu.Lock()
	dirty, changes := m.sweepLocked(now)
	if dirty {
		_ = m.persistLocked()
		m.notifyWaitersLocked()
	}
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		for _, ch := range changes {
			cb(ch.event, ch.task)
		}
	}
}

func (m *TaskManager) loadFromDisk() error {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var snap taskStoreSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	for _, t := range snap.Tasks {
		copyTask := cloneAgentTask(&t)
		if copyTask.ID == "" {
			continue
		}
		m.tasks[copyTask.ID] = &copyTask
		m.order = append(m.order, copyTask.ID)
	}
	now := m.now()
	if dirty, _ := m.sweepLocked(now); dirty {
		_ = m.persistLocked()
	}
	return nil
}

func (m *TaskManager) persistLocked() error {
	data, err := json.MarshalIndent(m.snapshotLocked(), "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *TaskManager) snapshotLocked() taskStoreSnapshot {
	list := make([]AgentTask, 0, len(m.order))
	for _, id := range m.order {
		if t, ok := m.tasks[id]; ok {
			list = append(list, cloneAgentTask(t))
		}
	}
	return taskStoreSnapshot{Tasks: list}
}

// ExportSnapshot serializes the task board state into canonical JSON for hot-standby cluster sync.
func (m *TaskManager) ExportSnapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Marshal(m.snapshotLocked())
}

// SnapshotHash returns a short deterministic digest of the current task board state.
func (m *TaskManager) SnapshotHash() string {
	raw, err := m.ExportSnapshot()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// ImportSnapshot atomically replaces the local task board from a primary/failback snapshot.
func (m *TaskManager) ImportSnapshot(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	var snap taskStoreSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("无效的任务板快照: %w", err)
	}

	m.mu.Lock()
	oldTasks := m.tasks
	oldOrder := m.order

	nextTasks := make(map[string]*AgentTask, len(snap.Tasks))
	nextOrder := make([]string, 0, len(snap.Tasks))
	for _, t := range snap.Tasks {
		cp := cloneAgentTask(&t)
		if cp.ID == "" {
			continue
		}
		if _, dup := nextTasks[cp.ID]; dup {
			continue
		}
		nextTasks[cp.ID] = &cp
		nextOrder = append(nextOrder, cp.ID)
	}
	m.tasks = nextTasks
	m.order = nextOrder
	if err := m.persistLocked(); err != nil {
		m.tasks = oldTasks
		m.order = oldOrder
		m.mu.Unlock()
		return err
	}
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("sync", AgentTask{})
	}
	return nil
}

func cloneTimeValPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func cloneTaskStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneTaskIntSlice(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func cloneAgentTask(src *AgentTask) AgentTask {
	out := *src
	out.ScheduledAt = cloneTimeValPtr(src.ScheduledAt)
	out.ClaimedAt = cloneTimeValPtr(src.ClaimedAt)
	out.LeaseExpiresAt = cloneTimeValPtr(src.LeaseExpiresAt)
	out.CompletedAt = cloneTimeValPtr(src.CompletedAt)
	out.OutputFiles = cloneTaskStringSlice(src.OutputFiles)

	if len(src.Subtasks) > 0 {
		out.Subtasks = make([]TaskSubtask, len(src.Subtasks))
		for i, s := range src.Subtasks {
			s.DependsOn = cloneTaskIntSlice(s.DependsOn)
			s.ClaimedAt = cloneTimeValPtr(s.ClaimedAt)
			s.LeaseExpiresAt = cloneTimeValPtr(s.LeaseExpiresAt)
			s.CompletedAt = cloneTimeValPtr(s.CompletedAt)
			out.Subtasks[i] = s
		}
	}
	if len(src.Comments) > 0 {
		out.Comments = make([]TaskComment, len(src.Comments))
		for i, c := range src.Comments {
			c.Mentions = cloneTaskStringSlice(c.Mentions)
			out.Comments[i] = c
		}
	}
	return out
}

func cleanTaskText(raw string, maxRunes int, multiline bool) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if multiline && r == '\n' {
				return '\n'
			}
			if r == '\r' || r == '\t' {
				if multiline {
					return ' '
				}
				return -1
			}
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	runes := []rune(cleaned)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return cleaned
}

func normalizePriority(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return "normal", nil
	}
	switch p {
	case "normal", "high", "urgent":
		return p, nil
	default:
		return "", fmt.Errorf("%w: priority 必须为 normal, high 或 urgent", ErrTaskInvalidInput)
	}
}

func normalizeLeaseDuration(sec int) time.Duration {
	if sec <= 0 {
		sec = DefaultTaskLeaseSec
	}
	if sec < MinTaskLeaseSec {
		sec = MinTaskLeaseSec
	}
	if sec > MaxTaskLeaseSec {
		sec = MaxTaskLeaseSec
	}
	return time.Duration(sec) * time.Second
}

func normalizeMaxRounds(rounds int) int {
	if rounds <= 0 {
		return DefaultMaxCommentRounds
	}
	if rounds > MaxCommentRoundsCap {
		return MaxCommentRoundsCap
	}
	return rounds
}

func sanitizeTaskFolderPrefix(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", nil
	}
	validated, err := auth.ValidateFolderRules([]string{trimmed})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTaskInvalidInput, err)
	}
	if len(validated) == 0 || validated[0] == "*" {
		return "", nil
	}
	return validated[0], nil
}

func randomTaskID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func extractMentions(body string) []string {
	matches := mentionPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		tok := strings.TrimSpace(m[1])
		low := strings.ToLower(tok)
		if tok != "" && !seen[low] {
			seen[low] = true
			out = append(out, tok)
		}
	}
	return out
}

func actorMatchesTarget(target string, actor TaskActor) bool {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "@"))
	if t == "" {
		return false
	}
	if actor.KeyID != "" && strings.EqualFold(t, actor.KeyID) {
		return true
	}
	if actor.ActorID() != "" && strings.EqualFold(t, actor.ActorID()) {
		return true
	}
	// If target was not resolved to an ID, allow alias/name match cleanly.
	if !strings.HasPrefix(strings.ToLower(t), "agk_") {
		if actor.Alias != "" && strings.EqualFold(t, actor.Alias) {
			return true
		}
		if actor.KeyName != "" && strings.EqualFold(t, actor.KeyName) {
			return true
		}
	}
	if !actor.IsAgent && actor.Username != "" && strings.EqualFold(t, actor.Username) {
		return true
	}
	return false
}

func taskMentionsActor(t *AgentTask, actor TaskActor) bool {
	if t == nil || t.Status == TaskStatusDone || t.Status == TaskStatusCancelled || t.Status == TaskStatusScheduled {
		return false
	}
	if len(t.Comments) == 0 {
		return false
	}
	actorID := actor.ActorID()
	lastOwnIdx := -1
	for i := len(t.Comments) - 1; i >= 0; i-- {
		if t.Comments[i].AuthorID == actorID {
			lastOwnIdx = i
			break
		}
	}
	for i := lastOwnIdx + 1; i < len(t.Comments); i++ {
		c := t.Comments[i]
		if c.AuthorRole == "system" {
			continue
		}
		for _, m := range c.Mentions {
			if actorMatchesTarget(m, actor) {
				return true
			}
		}
	}
	return false
}

func subtaskDepsSatisfied(t *AgentTask, sub TaskSubtask) bool {
	if len(sub.DependsOn) == 0 {
		return true
	}
	for _, depSeq := range sub.DependsOn {
		matched := false
		for _, candidate := range t.Subtasks {
			if candidate.Seq == depSeq {
				if candidate.Status != SubtaskStatusDone {
					return false
				}
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func canActorClaimTask(t *AgentTask, actor TaskActor) bool {
	if t == nil {
		return false
	}
	if len(t.Subtasks) > 0 {
		if t.Status != TaskStatusInProgress && t.Status != TaskStatusPending {
			return false
		}
		for _, sub := range t.Subtasks {
			if sub.Status != SubtaskStatusPending || !subtaskDepsSatisfied(t, sub) {
				continue
			}
			target := sub.AssignedAgent
			if target == "" {
				target = t.AssignedAgent
			}
			if target == "" || actorMatchesTarget(target, actor) {
				return true
			}
		}
		return false
	}
	if t.Status != TaskStatusPending {
		return false
	}
	return t.AssignedAgent == "" || actorMatchesTarget(t.AssignedAgent, actor)
}

func canViewTask(t *AgentTask, actor TaskActor) bool {
	if actor.Role == "admin" {
		return true
	}
	if strings.EqualFold(t.OwnerUsername, actor.Username) || strings.EqualFold(t.CreatedBy, actor.Username) {
		return true
	}
	if actor.IsAgent && actorMatchesTarget(t.AssignedAgent, actor) {
		return true
	}
	return false
}

func canManageTask(t *AgentTask, actor TaskActor) bool {
	if actor.Role == "admin" && !actor.IsAgent {
		return true
	}
	if !actor.IsAgent {
		return strings.EqualFold(t.OwnerUsername, actor.Username) || strings.EqualFold(t.CreatedBy, actor.Username)
	}
	return false
}

func (m *TaskManager) sweepLocked(now time.Time) (bool, []taskChangeNotification) {
	dirty := false
	var changes []taskChangeNotification
	for _, id := range m.order {
		t, ok := m.tasks[id]
		if !ok {
			continue
		}
		if t.Status == TaskStatusScheduled && (t.ScheduledAt == nil || !t.ScheduledAt.After(now)) {
			t.Status = TaskStatusPending
			t.UpdatedAt = now
			dirty = true
			changes = append(changes, taskChangeNotification{event: "activated", task: cloneAgentTask(t)})
		}
		if len(t.Subtasks) == 0 {
			if t.Status == TaskStatusInProgress && t.LeaseExpiresAt != nil && !now.Before(*t.LeaseExpiresAt) {
				prev := t.ClaimedByName
				t.Status = TaskStatusPending
				t.ClaimedByID = ""
				t.ClaimedByName = ""
				t.ClaimedAt = nil
				t.LeaseExpiresAt = nil
				t.UpdatedAt = now
				t.Comments = append(t.Comments, TaskComment{
					ID:         randomTaskID("cmt"),
					AuthorName: "系统调度",
					AuthorRole: "system",
					Body:       fmt.Sprintf("%s 的执行租约已过期，任务自动释放回待接单池。", prev),
					CreatedAt:  now,
				})
				dirty = true
				changes = append(changes, taskChangeNotification{event: "lease_expired", task: cloneAgentTask(t)})
			}
			continue
		}
		subChanged := false
		for i := range t.Subtasks {
			sub := &t.Subtasks[i]
			if sub.Status == SubtaskStatusClaimed && sub.LeaseExpiresAt != nil && !now.Before(*sub.LeaseExpiresAt) {
				prev := sub.ClaimedByName
				sub.Status = SubtaskStatusPending
				sub.ClaimedByID = ""
				sub.ClaimedByName = ""
				sub.ClaimedAt = nil
				sub.LeaseExpiresAt = nil
				t.Comments = append(t.Comments, TaskComment{
					ID:         randomTaskID("cmt"),
					AuthorName: "系统调度",
					AuthorRole: "system",
					Body:       fmt.Sprintf("子任务 #%d（原认领：%s）租约已过期，自动释放回待领队列。", sub.Seq, prev),
					CreatedAt:  now,
				})
				subChanged = true
			}
		}
		if subChanged {
			t.UpdatedAt = now
			dirty = true
			changes = append(changes, taskChangeNotification{event: "lease_expired", task: cloneAgentTask(t)})
		}
	}
	return dirty, changes
}

func (m *TaskManager) emitChange(cb func(string, AgentTask), event string, t *AgentTask) {
	if cb != nil && t != nil {
		cb(event, cloneAgentTask(t))
	}
}

func (m *TaskManager) getLocked(id string) (*AgentTask, error) {
	cleanID := strings.TrimSpace(id)
	t, ok := m.tasks[cleanID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return t, nil
}

// NextWakeAt returns the earliest future scheduled activation or lease expiry timestamp visible to actor.
func (m *TaskManager) NextWakeAt(actor TaskActor) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var earliest time.Time
	has := false
	consider := func(t time.Time) {
		if !has || t.Before(earliest) {
			earliest = t
			has = true
		}
	}
	for _, id := range m.order {
		t, ok := m.tasks[id]
		if !ok || !canViewTask(t, actor) {
			continue
		}
		if t.Status == TaskStatusScheduled && t.ScheduledAt != nil {
			consider(*t.ScheduledAt)
		}
		if t.Status == TaskStatusInProgress && len(t.Subtasks) == 0 && t.LeaseExpiresAt != nil {
			consider(*t.LeaseExpiresAt)
		}
		for _, sub := range t.Subtasks {
			if sub.Status == SubtaskStatusClaimed && sub.LeaseExpiresAt != nil {
				consider(*sub.LeaseExpiresAt)
			}
		}
	}
	return earliest, has
}

func (m *TaskManager) ListTasks(actor TaskActor, filter TaskListFilter) TaskListResult {
	now := m.now()
	m.mu.Lock()
	dirty, changes := m.sweepLocked(now)
	if dirty {
		_ = m.persistLocked()
		m.notifyWaitersLocked()
	}

	summary := TaskListSummary{}
	var out []AgentTask
	cleanStatus := strings.ToLower(strings.TrimSpace(filter.Status))
	cleanAssigned := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(filter.AssignedAgent), "@"))
	cleanFolder, _ := sanitizeTaskFolderPrefix(filter.FolderPrefix)

	for i := len(m.order) - 1; i >= 0; i-- {
		id := m.order[i]
		t, ok := m.tasks[id]
		if !ok || !canViewTask(t, actor) {
			continue
		}
		summary.Total++
		switch t.Status {
		case TaskStatusScheduled:
			summary.Scheduled++
		case TaskStatusPending:
			summary.Pending++
		case TaskStatusInProgress:
			summary.InProgress++
		case TaskStatusPaused:
			summary.Paused++
		case TaskStatusDone:
			summary.Done++
		}
		claimable := canActorClaimTask(t, actor)
		mentioned := taskMentionsActor(t, actor)
		if claimable {
			summary.ClaimableForMe++
		}
		if mentioned {
			summary.MentionedForMe++
		}

		if cleanStatus != "" && cleanStatus != "all" {
			if cleanStatus == "waiting" {
				if t.Status != TaskStatusPending && t.Status != TaskStatusScheduled {
					continue
				}
			} else if t.Status != cleanStatus {
				continue
			}
		}
		if cleanAssigned != "" {
			matched := strings.EqualFold(t.AssignedAgent, cleanAssigned) || strings.EqualFold(t.ClaimedByID, cleanAssigned)
			if !matched {
				for _, sub := range t.Subtasks {
					if strings.EqualFold(sub.AssignedAgent, cleanAssigned) || strings.EqualFold(sub.ClaimedByID, cleanAssigned) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}
		if cleanFolder != "" && !auth.FolderMatchesPrefix(t.FolderPrefix, cleanFolder) {
			continue
		}
		if filter.ClaimableOnly && !claimable {
			continue
		}
		if filter.MentionedMe && !mentioned {
			continue
		}
		out = append(out, cloneAgentTask(t))
	}
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		for _, ch := range changes {
			cb(ch.event, ch.task)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		prioRank := func(p string) int {
			switch p {
			case "urgent":
				return 3
			case "high":
				return 2
			default:
				return 1
			}
		}
		if filter.ClaimableOnly && prioRank(out[i].Priority) != prioRank(out[j].Priority) {
			return prioRank(out[i].Priority) > prioRank(out[j].Priority)
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	limit := filter.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return TaskListResult{Summary: summary, Tasks: out}
}

func (m *TaskManager) GetTask(actor TaskActor, taskID string) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	dirty, changes := m.sweepLocked(now)
	if dirty {
		_ = m.persistLocked()
		m.notifyWaitersLocked()
	}
	t, err := m.getLocked(taskID)
	if err != nil {
		cb := m.onChange
		m.mu.Unlock()
		for _, ch := range changes {
			if cb != nil {
				cb(ch.event, ch.task)
			}
		}
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	snap := cloneAgentTask(t)
	cb := m.onChange
	m.mu.Unlock()
	if cb != nil {
		for _, ch := range changes {
			cb(ch.event, ch.task)
		}
	}
	return snap, nil
}

func (m *TaskManager) CreateTask(actor TaskActor, in CreateTaskInput) (AgentTask, error) {
	if actor.IsAgent {
		return AgentTask{}, fmt.Errorf("%w: 仅网页用户可创建主待办任务，Agent 请使用 split_task 拆分子任务", ErrTaskForbidden)
	}
	title := cleanTaskText(in.Title, 160, false)
	if title == "" {
		return AgentTask{}, fmt.Errorf("%w: 任务标题不能为空", ErrTaskInvalidInput)
	}
	desc := cleanTaskText(in.Description, 4000, true)
	prio, err := normalizePriority(in.Priority)
	if err != nil {
		return AgentTask{}, err
	}
	folderPrefix, err := sanitizeTaskFolderPrefix(in.FolderPrefix)
	if err != nil {
		return AgentTask{}, err
	}
	allowSplit := true
	if in.AllowSplit != nil {
		allowSplit = *in.AllowSplit
	}

	now := m.now()
	status := TaskStatusPending
	var schedAt *time.Time
	if in.ScheduledAt != nil && in.ScheduledAt.After(now) {
		status = TaskStatusScheduled
		tVal := *in.ScheduledAt
		schedAt = &tVal
	}

	task := &AgentTask{
		ID:                 randomTaskID("tsk"),
		Title:              title,
		Description:        desc,
		Status:             status,
		Priority:           prio,
		OwnerUsername:      strings.TrimSpace(actor.Username),
		CreatedBy:          strings.TrimSpace(actor.Username),
		AssignedAgent:      strings.TrimSpace(in.AssignedAgent),
		AssignedAgentLabel: cleanTaskText(in.AssignedAgentLabel, 64, false),
		FolderPrefix:       folderPrefix,
		AllowSplit:         allowSplit,
		ScheduledAt:        schedAt,
		MaxCommentRounds:   normalizeMaxRounds(in.MaxCommentRounds),
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	m.mu.Lock()
	if len(m.order) >= MaxTasksStored {
		// Evict oldest done/cancelled task first to respect storage cap.
		evictIdx := -1
		for idx, oldID := range m.order {
			if old, ok := m.tasks[oldID]; ok && (old.Status == TaskStatusDone || old.Status == TaskStatusCancelled) {
				evictIdx = idx
				break
			}
		}
		if evictIdx >= 0 {
			evictID := m.order[evictIdx]
			delete(m.tasks, evictID)
			m.order = append(m.order[:evictIdx], m.order[evictIdx+1:]...)
		} else {
			evictID := m.order[0]
			delete(m.tasks, evictID)
			m.order = m.order[1:]
		}
	}
	m.tasks[task.ID] = task
	m.order = append(m.order, task.ID)
	if err := m.persistLocked(); err != nil {
		delete(m.tasks, task.ID)
		m.order = m.order[:len(m.order)-1]
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(task)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("created", snap)
	}
	return snap, nil
}

func (m *TaskManager) UpdateTask(actor TaskActor, taskID string, in UpdateTaskInput) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(taskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}

	orig := cloneAgentTask(t)
	if in.Title != nil {
		clean := cleanTaskText(*in.Title, 160, false)
		if clean == "" {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 任务标题不能为空", ErrTaskInvalidInput)
		}
		t.Title = clean
	}
	if in.Description != nil {
		t.Description = cleanTaskText(*in.Description, 4000, true)
	}
	if in.Priority != nil {
		p, err := normalizePriority(*in.Priority)
		if err != nil {
			*t = orig
			m.mu.Unlock()
			return AgentTask{}, err
		}
		t.Priority = p
	}
	if in.AssignedAgent != nil {
		t.AssignedAgent = strings.TrimSpace(*in.AssignedAgent)
		if t.AssignedAgent == "" && in.AssignedAgentLabel == nil {
			t.AssignedAgentLabel = ""
		}
	}
	if in.AssignedAgentLabel != nil {
		t.AssignedAgentLabel = cleanTaskText(*in.AssignedAgentLabel, 64, false)
	}
	if in.FolderPrefix != nil {
		fp, err := sanitizeTaskFolderPrefix(*in.FolderPrefix)
		if err != nil {
			*t = orig
			m.mu.Unlock()
			return AgentTask{}, err
		}
		t.FolderPrefix = fp
	}
	if in.AllowSplit != nil {
		t.AllowSplit = *in.AllowSplit
	}
	if in.MaxCommentRounds != nil {
		t.MaxCommentRounds = normalizeMaxRounds(*in.MaxCommentRounds)
	}
	if in.ClearSchedule {
		t.ScheduledAt = nil
		if t.Status == TaskStatusScheduled {
			t.Status = TaskStatusPending
		}
	} else if in.ScheduledAt != nil {
		tv := *in.ScheduledAt
		t.ScheduledAt = &tv
		if tv.After(now) && (t.Status == TaskStatusPending || t.Status == TaskStatusScheduled) {
			t.Status = TaskStatusScheduled
		} else if !tv.After(now) && t.Status == TaskStatusScheduled {
			t.Status = TaskStatusPending
		}
	}
	if in.Status != nil {
		st := strings.ToLower(strings.TrimSpace(*in.Status))
		switch st {
		case TaskStatusPending, TaskStatusPaused, TaskStatusCancelled:
			t.Status = st
			if st == TaskStatusPaused {
				t.PauseReason = "人工暂停"
			} else {
				t.PauseReason = ""
			}
		default:
			*t = orig
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 不支持通过编辑切换至状态 %s", ErrTaskInvalidInput, st)
		}
	}
	t.UpdatedAt = now
	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("updated", snap)
	}
	return snap, nil
}

func (m *TaskManager) ActivateTask(actor TaskActor, taskID string) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	t, err := m.getLocked(taskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)
	if t.Status != TaskStatusScheduled && t.Status != TaskStatusPaused {
		m.mu.Unlock()
		return cloneAgentTask(t), nil
	}
	if len(t.Subtasks) > 0 {
		t.Status = TaskStatusInProgress
	} else {
		t.Status = TaskStatusPending
	}
	t.PauseReason = ""
	t.UpdatedAt = now
	t.Comments = append(t.Comments, TaskComment{
		ID:         randomTaskID("cmt"),
		AuthorName: actor.DisplayName(),
		AuthorRole: "system",
		Body:       "已人工提前激活任务，开放给 Agent 认领执行。",
		CreatedAt:  now,
	})
	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("activated", snap)
	}
	return snap, nil
}

func (m *TaskManager) ReopenTask(actor TaskActor, taskID string) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	t, err := m.getLocked(taskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)
	t.Status = TaskStatusPending
	t.PauseReason = ""
	t.ClaimedByID = ""
	t.ClaimedByName = ""
	t.ClaimedAt = nil
	t.LeaseExpiresAt = nil
	t.CompletedAt = nil
	if len(t.Subtasks) > 0 {
		t.Status = TaskStatusInProgress
		for i := range t.Subtasks {
			if t.Subtasks[i].Status == SubtaskStatusDone {
				t.Subtasks[i].Status = SubtaskStatusPending
				t.Subtasks[i].ClaimedByID = ""
				t.Subtasks[i].ClaimedByName = ""
				t.Subtasks[i].ClaimedAt = nil
				t.Subtasks[i].LeaseExpiresAt = nil
				t.Subtasks[i].CompletedAt = nil
			}
		}
	}
	t.UpdatedAt = now
	t.Comments = append(t.Comments, TaskComment{
		ID:         randomTaskID("cmt"),
		AuthorName: actor.DisplayName(),
		AuthorRole: "system",
		Body:       "任务已被重新打开，进入待接单池。",
		CreatedAt:  now,
	})
	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("reopened", snap)
	}
	return snap, nil
}

func (m *TaskManager) DeleteTask(actor TaskActor, taskID string) (AgentTask, error) {
	m.mu.Lock()
	t, err := m.getLocked(taskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	snap := cloneAgentTask(t)
	delete(m.tasks, t.ID)
	oldOrder := append([]string(nil), m.order...)
	newOrder := make([]string, 0, len(m.order))
	for _, id := range m.order {
		if id != t.ID {
			newOrder = append(newOrder, id)
		}
	}
	m.order = newOrder
	if err := m.persistLocked(); err != nil {
		m.tasks[snap.ID] = &snap
		m.order = oldOrder
		m.mu.Unlock()
		return AgentTask{}, err
	}
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("deleted", snap)
	}
	return snap, nil
}

// ClaimTask atomically claims a standalone task or unlocked subtask via CAS lock semantics.
func (m *TaskManager) ClaimTask(actor TaskActor, in ClaimTaskInput) (AgentTask, error) {
	now := m.now()
	leaseDur := normalizeLeaseDuration(in.LeaseSeconds)

	m.mu.Lock()
	dirty, changes := m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		if dirty {
			_ = m.persistLocked()
			m.notifyWaitersLocked()
		}
		cb := m.onChange
		m.mu.Unlock()
		for _, ch := range changes {
			if cb != nil {
				cb(ch.event, ch.task)
			}
		}
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)

	if t.Status == TaskStatusScheduled {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskScheduledInFuture
	}
	if t.Status == TaskStatusDone || t.Status == TaskStatusCancelled || t.Status == TaskStatusPaused {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskNotClaimable
	}

	subID := strings.TrimSpace(in.SubtaskID)
	actorID := actor.ActorID()
	actorName := actor.DisplayName()

	if len(t.Subtasks) > 0 {
		if subID == "" {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 该任务已拆分，请指定要认领的 subtask_id", ErrTaskInvalidInput)
		}
		idx := -1
		for i := range t.Subtasks {
			if t.Subtasks[i].ID == subID {
				idx = i
				break
			}
		}
		if idx < 0 {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskNotFound
		}
		sub := &t.Subtasks[idx]
		target := sub.AssignedAgent
		if target == "" {
			target = t.AssignedAgent
		}
		if target != "" && !actorMatchesTarget(target, actor) {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 仅限 %s 接单", ErrTaskAssignedToOther, target)
		}
		if sub.Status == SubtaskStatusDone {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskNotClaimable
		}
		if sub.Status == SubtaskStatusClaimed && sub.ClaimedByID != actorID {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 已被 %s 认领", ErrTaskAlreadyClaimed, sub.ClaimedByName)
		}
		if !subtaskDepsSatisfied(t, *sub) {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskBlockedByDeps
		}
		exp := now.Add(leaseDur)
		if sub.ClaimedAt == nil {
			ts := now
			sub.ClaimedAt = &ts
		}
		sub.Status = SubtaskStatusClaimed
		sub.ClaimedByID = actorID
		sub.ClaimedByName = actorName
		sub.LeaseExpiresAt = &exp
		t.Status = TaskStatusInProgress
		t.UpdatedAt = now
	} else {
		if subID != "" {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskNotFound
		}
		if t.AssignedAgent != "" && !actorMatchesTarget(t.AssignedAgent, actor) {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 仅限 %s 接单", ErrTaskAssignedToOther, t.AssignedAgent)
		}
		if t.Status == TaskStatusInProgress && t.ClaimedByID != actorID {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 已被 %s 认领", ErrTaskAlreadyClaimed, t.ClaimedByName)
		}
		exp := now.Add(leaseDur)
		if t.ClaimedAt == nil {
			ts := now
			t.ClaimedAt = &ts
		}
		t.Status = TaskStatusInProgress
		t.ClaimedByID = actorID
		t.ClaimedByName = actorName
		t.LeaseExpiresAt = &exp
		t.UpdatedAt = now
	}

	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("claimed", snap)
	}
	return snap, nil
}

// HeartbeatTask extends the execution lease on a currently claimed task or subtask.
func (m *TaskManager) HeartbeatTask(actor TaskActor, in ClaimTaskInput) (AgentTask, error) {
	now := m.now()
	leaseDur := normalizeLeaseDuration(in.LeaseSeconds)

	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)
	actorID := actor.ActorID()
	subID := strings.TrimSpace(in.SubtaskID)

	if subID != "" {
		found := false
		for i := range t.Subtasks {
			if t.Subtasks[i].ID == subID {
				found = true
				if t.Subtasks[i].Status != SubtaskStatusClaimed || t.Subtasks[i].ClaimedByID != actorID {
					m.mu.Unlock()
					return AgentTask{}, ErrTaskLeaseNotHeld
				}
				exp := now.Add(leaseDur)
				t.Subtasks[i].LeaseExpiresAt = &exp
				break
			}
		}
		if !found {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskNotFound
		}
	} else {
		if t.Status != TaskStatusInProgress || t.ClaimedByID != actorID {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		exp := now.Add(leaseDur)
		t.LeaseExpiresAt = &exp
	}
	t.UpdatedAt = now
	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("heartbeat", snap)
	}
	return snap, nil
}

// SplitTask breaks a complex task into concurrent or dependency-ordered subtasks.
func (m *TaskManager) SplitTask(actor TaskActor, in SplitTaskInput) (AgentTask, error) {
	if len(in.Subtasks) < 2 || len(in.Subtasks) > MaxSubtasksPerTask {
		return AgentTask{}, fmt.Errorf("%w: 子任务数量须在 2 到 %d 之间", ErrTaskInvalidInput, MaxSubtasksPerTask)
	}
	now := m.now()
	leaseDur := normalizeLeaseDuration(in.LeaseSeconds)

	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	if !t.AllowSplit {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskSplitNotAllowed
	}
	if len(t.Subtasks) > 0 {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskAlreadySplit
	}
	if t.Status != TaskStatusPending && t.Status != TaskStatusInProgress {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskNotClaimable
	}
	if t.AssignedAgent != "" && !actorMatchesTarget(t.AssignedAgent, actor) && !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, fmt.Errorf("%w: 仅 %s 或管理员可拆分该定向任务", ErrTaskAssignedToOther, t.AssignedAgent)
	}
	if t.Status == TaskStatusInProgress && t.ClaimedByID != "" && t.ClaimedByID != actor.ActorID() && !canManageTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskAlreadyClaimed
	}

	orig := cloneAgentTask(t)
	subs := make([]TaskSubtask, 0, len(in.Subtasks))
	for i, spec := range in.Subtasks {
		seq := i + 1
		sTitle := cleanTaskText(spec.Title, 160, false)
		if sTitle == "" {
			m.mu.Unlock()
			return AgentTask{}, fmt.Errorf("%w: 子任务 #%d 标题不能为空", ErrTaskInvalidInput, seq)
		}
		var deps []int
		seenDep := make(map[int]bool)
		for _, dep := range spec.DependsOn {
			if dep < 1 || dep >= seq {
				m.mu.Unlock()
				return AgentTask{}, fmt.Errorf("%w: 子任务 #%d 的前置依赖 #%d 必须小于自身序号且 >= 1", ErrTaskInvalidInput, seq, dep)
			}
			if !seenDep[dep] {
				seenDep[dep] = true
				deps = append(deps, dep)
			}
		}
		sort.Ints(deps)
		subs = append(subs, TaskSubtask{
			ID:                 fmt.Sprintf("sub_%d", seq),
			Seq:                seq,
			Title:              sTitle,
			Description:        cleanTaskText(spec.Description, 1000, true),
			AssignedAgent:      strings.TrimSpace(spec.AssignedAgent),
			AssignedAgentLabel: cleanTaskText(spec.AssignedAgentLabel, 64, false),
			Status:             SubtaskStatusPending,
			DependsOn:          deps,
		})
	}

	claimFirst := actor.IsAgent
	if in.ClaimFirst != nil {
		claimFirst = *in.ClaimFirst
	}
	if claimFirst {
		firstTarget := subs[0].AssignedAgent
		if firstTarget == "" {
			firstTarget = t.AssignedAgent
		}
		if (firstTarget == "" || actorMatchesTarget(firstTarget, actor)) && len(subs[0].DependsOn) == 0 {
			ts := now
			exp := now.Add(leaseDur)
			subs[0].Status = SubtaskStatusClaimed
			subs[0].ClaimedByID = actor.ActorID()
			subs[0].ClaimedByName = actor.DisplayName()
			subs[0].ClaimedAt = &ts
			subs[0].LeaseExpiresAt = &exp
		}
	}

	t.Subtasks = subs
	t.Status = TaskStatusInProgress
	t.LeaseExpiresAt = nil
	if t.ClaimedByID == "" {
		t.ClaimedByID = actor.ActorID()
		t.ClaimedByName = actor.DisplayName()
		ts := now
		t.ClaimedAt = &ts
	}
	t.UpdatedAt = now
	if note := cleanTaskText(in.Note, 2000, true); note != "" {
		t.Comments = append(t.Comments, TaskComment{
			ID:         randomTaskID("cmt"),
			AuthorID:   actor.ActorID(),
			AuthorName: actor.DisplayName(),
			AuthorRole: actor.RoleTag(),
			Mentions:   extractMentions(note),
			Body:       note,
			CreatedAt:  now,
		})
	}

	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("split", snap)
	}
	return snap, nil
}

// PostComment appends a collaborative discussion message and enforces the anti-loop round cap for Agents.
func (m *TaskManager) PostComment(actor TaskActor, in PostCommentInput) (AgentTask, TaskComment, error) {
	body := cleanTaskText(in.Body, 2000, true)
	if body == "" {
		return AgentTask{}, TaskComment{}, fmt.Errorf("%w: 留言内容不能为空", ErrTaskInvalidInput)
	}
	now := m.now()

	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, TaskComment{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, TaskComment{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)

	maxRounds := normalizeMaxRounds(t.MaxCommentRounds)
	if actor.IsAgent {
		agentCount := 0
		for _, c := range t.Comments {
			if c.AuthorRole == "agent" {
				agentCount++
			}
		}
		if agentCount >= maxRounds {
			t.Status = TaskStatusPaused
			t.PauseReason = fmt.Sprintf("已触发协同防互怼熔断（Agent 讨论达 %d 轮上限），等待人工确认", maxRounds)
			t.UpdatedAt = now
			_ = m.persistLocked()
			snap := cloneAgentTask(t)
			m.notifyWaitersLocked()
			cb := m.onChange
			m.mu.Unlock()
			if cb != nil {
				cb("paused", snap)
			}
			return AgentTask{}, TaskComment{}, ErrTaskCommentLimitReached
		}
	}

	cmt := TaskComment{
		ID:         randomTaskID("cmt"),
		AuthorID:   actor.ActorID(),
		AuthorName: actor.DisplayName(),
		AuthorRole: actor.RoleTag(),
		Mentions:   extractMentions(body),
		Body:       body,
		CreatedAt:  now,
	}
	t.Comments = append(t.Comments, cmt)
	t.UpdatedAt = now

	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, TaskComment{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("comment", snap)
	}
	return snap, cmt, nil
}

// CompleteTask checks off a claimed subtask (auto-completing the parent when all are done) or standalone task.
func (m *TaskManager) CompleteTask(actor TaskActor, in CompleteTaskInput) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)
	actorID := actor.ActorID()
	isAdmin := canManageTask(t, actor)
	summary := cleanTaskText(in.ResultSummary, 2000, true)

	seenOut := make(map[string]bool, len(t.OutputFiles))
	for _, f := range t.OutputFiles {
		seenOut[f] = true
	}
	appendOut := func(raw string) string {
		f := cleanTaskText(raw, 240, false)
		if f != "" && !seenOut[f] {
			seenOut[f] = true
			t.OutputFiles = append(t.OutputFiles, f)
		}
		return f
	}
	for _, f := range in.OutputFiles {
		appendOut(f)
	}

	subID := strings.TrimSpace(in.SubtaskID)
	if len(t.Subtasks) > 0 {
		if subID == "" {
			// Allow direct parent completion ONLY if all subtasks are done or admin forces finish.
			allDone := true
			for _, sub := range t.Subtasks {
				if sub.Status != SubtaskStatusDone {
					allDone = false
					break
				}
			}
			if !allDone && !isAdmin {
				m.mu.Unlock()
				return AgentTask{}, fmt.Errorf("%w: 该任务有未打勾的子任务，请指定 subtask_id 逐一完成", ErrTaskInvalidInput)
			}
			t.Status = TaskStatusDone
			ts := now
			t.CompletedAt = &ts
			t.LeaseExpiresAt = nil
			if summary != "" {
				t.ResultSummary = summary
			}
		} else {
			idx := -1
			for i := range t.Subtasks {
				if t.Subtasks[i].ID == subID {
					idx = i
					break
				}
			}
			if idx < 0 {
				m.mu.Unlock()
				return AgentTask{}, ErrSubtaskNotFound
			}
			sub := &t.Subtasks[idx]
			if sub.Status != SubtaskStatusClaimed && !(isAdmin && sub.Status == SubtaskStatusPending) {
				if sub.Status == SubtaskStatusDone {
					m.mu.Unlock()
					return cloneAgentTask(t), nil
				}
				m.mu.Unlock()
				return AgentTask{}, ErrTaskLeaseNotHeld
			}
			if sub.Status == SubtaskStatusClaimed && sub.ClaimedByID != actorID && !isAdmin {
				m.mu.Unlock()
				return AgentTask{}, ErrTaskLeaseNotHeld
			}
			if sub.ClaimedByID == "" {
				sub.ClaimedByID = actorID
				sub.ClaimedByName = actor.DisplayName()
			}
			ts := now
			sub.Status = SubtaskStatusDone
			sub.CompletedAt = &ts
			sub.LeaseExpiresAt = nil
			if len(in.OutputFiles) > 0 {
				sub.OutputFile = cleanTaskText(in.OutputFiles[0], 240, false)
			}
			if summary != "" {
				sub.ResultSummary = summary
			}
			allDone := true
			for _, s := range t.Subtasks {
				if s.Status != SubtaskStatusDone {
					allDone = false
					break
				}
			}
			if allDone {
				t.Status = TaskStatusDone
				t.CompletedAt = &ts
				t.LeaseExpiresAt = nil
				if t.ResultSummary == "" {
					t.ResultSummary = summary
					if t.ResultSummary == "" {
						t.ResultSummary = fmt.Sprintf("全部 %d 个协同子任务已完成打勾。", len(t.Subtasks))
					}
				}
			} else {
				t.Status = TaskStatusInProgress
			}
		}
	} else {
		if subID != "" {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskNotFound
		}
		if t.Status == TaskStatusDone {
			m.mu.Unlock()
			return cloneAgentTask(t), nil
		}
		if t.Status == TaskStatusInProgress && t.ClaimedByID != actorID && !isAdmin {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		if t.Status == TaskStatusPending && !isAdmin {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		if t.Status != TaskStatusInProgress && t.Status != TaskStatusPending {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskNotClaimable
		}
		if t.ClaimedByID == "" {
			t.ClaimedByID = actorID
			t.ClaimedByName = actor.DisplayName()
		}
		ts := now
		t.Status = TaskStatusDone
		t.CompletedAt = &ts
		t.LeaseExpiresAt = nil
		if summary != "" {
			t.ResultSummary = summary
		}
	}
	t.UpdatedAt = now

	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("completed", snap)
	}
	return snap, nil
}

// ReleaseTask returns a claimed task/subtask back to pending (or pauses / reassigns it).
func (m *TaskManager) ReleaseTask(actor TaskActor, in ReleaseTaskInput) (AgentTask, error) {
	now := m.now()
	m.mu.Lock()
	_, _ = m.sweepLocked(now)
	t, err := m.getLocked(in.TaskID)
	if err != nil {
		m.mu.Unlock()
		return AgentTask{}, err
	}
	if !canViewTask(t, actor) {
		m.mu.Unlock()
		return AgentTask{}, ErrTaskForbidden
	}
	orig := cloneAgentTask(t)
	actorID := actor.ActorID()
	isAdmin := canManageTask(t, actor)
	reason := cleanTaskText(in.Reason, 1000, true)

	subID := strings.TrimSpace(in.SubtaskID)
	if subID != "" {
		idx := -1
		for i := range t.Subtasks {
			if t.Subtasks[i].ID == subID {
				idx = i
				break
			}
		}
		if idx < 0 {
			m.mu.Unlock()
			return AgentTask{}, ErrSubtaskNotFound
		}
		sub := &t.Subtasks[idx]
		if sub.Status != SubtaskStatusClaimed {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		if sub.ClaimedByID != actorID && !isAdmin {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		sub.Status = SubtaskStatusPending
		sub.ClaimedByID = ""
		sub.ClaimedByName = ""
		sub.ClaimedAt = nil
		sub.LeaseExpiresAt = nil
		if strings.TrimSpace(in.ReassignAgent) != "" {
			sub.AssignedAgent = strings.TrimSpace(in.ReassignAgent)
			sub.AssignedAgentLabel = cleanTaskText(in.ReassignAgentLabel, 64, false)
		}
	} else {
		if t.Status != TaskStatusInProgress || (t.ClaimedByID != actorID && !isAdmin) {
			m.mu.Unlock()
			return AgentTask{}, ErrTaskLeaseNotHeld
		}
		t.ClaimedByID = ""
		t.ClaimedByName = ""
		t.ClaimedAt = nil
		t.LeaseExpiresAt = nil
		if in.Pause {
			t.Status = TaskStatusPaused
			if reason != "" {
				t.PauseReason = reason
			} else {
				t.PauseReason = actor.DisplayName() + " 暂停并交回任务"
			}
		} else {
			t.Status = TaskStatusPending
		}
		if strings.TrimSpace(in.ReassignAgent) != "" {
			t.AssignedAgent = strings.TrimSpace(in.ReassignAgent)
			t.AssignedAgentLabel = cleanTaskText(in.ReassignAgentLabel, 64, false)
		}
	}
	if reason != "" {
		t.Comments = append(t.Comments, TaskComment{
			ID:         randomTaskID("cmt"),
			AuthorID:   actor.ActorID(),
			AuthorName: actor.DisplayName(),
			AuthorRole: actor.RoleTag(),
			Mentions:   extractMentions(reason),
			Body:       "交回任务说明：" + reason,
			CreatedAt:  now,
		})
	}
	t.UpdatedAt = now
	if err := m.persistLocked(); err != nil {
		*t = orig
		m.mu.Unlock()
		return AgentTask{}, err
	}
	snap := cloneAgentTask(t)
	m.notifyWaitersLocked()
	cb := m.onChange
	m.mu.Unlock()

	if cb != nil {
		cb("released", snap)
	}
	return snap, nil
}
